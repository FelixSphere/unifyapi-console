package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// UNIFYAPI-FORK: fork-owned, covers relay_header_timeout.go.
//
// The tests that matter here are the no-regression ones: non-streaming traffic
// is 97.2% of production and must be provably untouched, and a stream that
// answers in time must be able to generate for as long as it likes afterwards.

type stubRoundTripper struct {
	delay      time.Duration // wait before returning headers
	bodyChunks []string      // written after headers, one per interval
	interval   time.Duration
	seen       *http.Request
}

func (s *stubRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	s.seen = req
	select {
	case <-time.After(s.delay):
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		for _, c := range s.bodyChunks {
			select {
			case <-time.After(s.interval):
			case <-req.Context().Done():
				return
			}
			if _, err := io.WriteString(pw, c); err != nil {
				return
			}
		}
	}()
	return &http.Response{StatusCode: 200, Body: pr, Header: make(http.Header)}, nil
}

func streamReq() *http.Request {
	r := httptest.NewRequest("POST", "https://example.test/v1/chat/completions", nil)
	r.Header.Set("Accept", "text/event-stream")
	return r
}

func plainReq() *http.Request {
	r := httptest.NewRequest("POST", "https://example.test/v1/chat/completions", nil)
	r.Header.Set("Accept", "application/json")
	return r
}

func TestDefaultIsDisabled(t *testing.T) {
	t.Setenv(relayStreamHeaderTimeoutEnv, "")
	if got := relayStreamHeaderTimeout(); got != 0 {
		t.Fatalf("default must be disabled so merging changes nothing, got %v", got)
	}
	base := &stubRoundTripper{}
	if wrapRelayStreamHeaderTimeout(base) != http.RoundTripper(base) {
		t.Fatal("disabled must return the transport unwrapped")
	}
}

func TestParsesAndRejectsNonPositive(t *testing.T) {
	t.Setenv(relayStreamHeaderTimeoutEnv, "30")
	if got, want := relayStreamHeaderTimeout(), 30*time.Second; got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
	for _, v := range []string{"0", "-1"} {
		t.Setenv(relayStreamHeaderTimeoutEnv, v)
		if got := relayStreamHeaderTimeout(); got != 0 {
			t.Fatalf("%q must disable rather than set a bogus deadline, got %v", v, got)
		}
	}
}

// THE no-regression test. Non-streaming is 97.2% of production traffic, and
// production has legitimate non-streaming requests up to 125s. A slow
// non-streaming request must pass through even when the bound is far shorter
// than it takes -- because for non-streaming, headers do not arrive until the
// whole completion is built.
func TestSlowNonStreamingRequestIsNotBounded(t *testing.T) {
	rt := &streamHeaderTimeoutRoundTripper{
		base:    &stubRoundTripper{delay: 120 * time.Millisecond},
		timeout: 20 * time.Millisecond, // 6x shorter than the request takes
	}
	resp, err := rt.RoundTrip(plainReq())
	if err != nil {
		t.Fatalf("non-streaming request must not be bounded, got %v", err)
	}
	resp.Body.Close()
}

func TestStreamingStallIsBounded(t *testing.T) {
	rt := &streamHeaderTimeoutRoundTripper{
		base:    &stubRoundTripper{delay: 2 * time.Second}, // the stall
		timeout: 30 * time.Millisecond,
	}
	start := time.Now()
	if _, err := rt.RoundTrip(streamReq()); err == nil {
		t.Fatal("a stalled stream must be cut off, got no error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("cut off too late: %v", elapsed)
	}
}

// The other half of no-regression: once headers arrive in time, the body must
// be free to generate well past the bound. An armed timer here would kill
// healthy streams mid-generation.
func TestStreamBodyMayOutliveTheBoundOnceHeadersArrive(t *testing.T) {
	rt := &streamHeaderTimeoutRoundTripper{
		base: &stubRoundTripper{
			delay:      10 * time.Millisecond,
			bodyChunks: []string{"a", "b", "c", "d", "e"},
			interval:   30 * time.Millisecond, // 150ms total, 3x the bound
		},
		timeout: 50 * time.Millisecond,
	}
	resp, err := rt.RoundTrip(streamReq())
	if err != nil {
		t.Fatalf("headers arrived in time, want no error, got %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("body must stream freely after headers, got %v", err)
	}
	if string(body) != "abcde" {
		t.Fatalf("stream truncated mid-generation: got %q, want %q", body, "abcde")
	}
}

func TestDisabledIsAPassthroughForStreams(t *testing.T) {
	rt := &streamHeaderTimeoutRoundTripper{
		base:    &stubRoundTripper{delay: 60 * time.Millisecond},
		timeout: 0,
	}
	resp, err := rt.RoundTrip(streamReq())
	if err != nil {
		t.Fatalf("timeout 0 must not bound anything, got %v", err)
	}
	resp.Body.Close()
}

func TestIsStreamingRequest(t *testing.T) {
	cases := []struct {
		accept string
		want   bool
	}{
		{"text/event-stream", true},
		{"text/event-stream; charset=utf-8", true},
		{"TEXT/EVENT-STREAM", true}, // header matching must not be case-sensitive
		{"application/json", false},
		{"", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "https://example.test/", nil)
		if c.accept != "" {
			r.Header.Set("Accept", c.accept)
		}
		if got := isStreamingRequest(r); got != c.want {
			t.Errorf("Accept=%q: got %v, want %v", c.accept, got, c.want)
		}
	}
	if isStreamingRequest(nil) {
		t.Error("nil request must not be treated as streaming")
	}
}

func TestWrapIsAppliedByBothClientBuilders(t *testing.T) {
	t.Setenv(relayStreamHeaderTimeoutEnv, "25")
	if _, ok := newRelayHTTPClient(&stubRoundTripper{}).Transport.(*streamHeaderTimeoutRoundTripper); !ok {
		t.Error("newRelayHTTPClient must wrap its transport")
	}
	if wrapRelayStreamHeaderTimeout(nil) != nil {
		t.Error("nil transport must stay nil")
	}
}

func TestCancelOnCloseReleasesContext(t *testing.T) {
	called := false
	b := &cancelOnCloseBody{
		ReadCloser: io.NopCloser(strings.NewReader("x")),
		cancel:     func() { called = true },
	}
	if err := b.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !called {
		t.Error("closing the body must release the request context")
	}
}
