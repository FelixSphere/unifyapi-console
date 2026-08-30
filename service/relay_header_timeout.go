package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// UNIFYAPI-FORK: fork-owned. Bounds how long a STREAMING relay request waits
// for upstream response headers. Upstream new-api bounds this nowhere.
//
// # The problem
//
// Measured 49.3s time-to-first-token on qwen3.7-max where generation, once it
// started, took 3.5s -- identical to the healthy samples. 93% of the request
// was spent waiting for the first byte, and nothing bounds that wait:
//
//   - RELAY_TIMEOUT sets http.Client.Timeout, which per the net/http docs
//     covers "reading the response body". On a stream that is the entire
//     generation, so any value low enough to catch a stall also truncates
//     healthy long responses mid-flight.
//   - STREAMING_TIMEOUT (300s) is an inactivity watchdog -- stream_scanner
//     calls ticker.Reset on every chunk -- so it fires only after a stream has
//     started and then gone quiet. It cannot see a stall before the first chunk.
//
// # Why this is streaming-only, and why that is not a shortcut
//
// http.Transport.ResponseHeaderTimeout would be the obvious knob, but it is
// per-transport and cannot distinguish request types, and the two types have
// opposite semantics:
//
//   - Streaming: the upstream sends headers before generating, so a bound here
//     measures time-to-first-byte. Safe and precise.
//   - Non-streaming: the upstream withholds headers until the completion is
//     fully built, so a bound here is a TOTAL timeout on generation.
//
// Production over 17,547 completions is 97.2% NON-streaming, averaging 7.5s,
// with 616 requests exceeding 30s and a legitimate maximum of 125s. A
// transport-wide 30s bound would therefore have failed those 616 requests --
// turning working requests into errors, which is precisely the regression this
// must not cause. So the bound is applied per request, and only to requests
// carrying Accept: text/event-stream (set by relay/channel/api_request.go).
// Non-streaming requests take an early return and are bit-for-bit unaffected.
//
// # Why the deadline stops at the headers
//
// The timer is cancelled the moment RoundTrip returns. It must be: leaving it
// armed would cancel the context mid-body and kill the stream during
// generation -- the same regression from a different direction. After headers
// arrive the body may take as long as it likes; STREAMING_TIMEOUT remains the
// watchdog for a stream that stalls partway.
const relayStreamHeaderTimeoutEnv = "RELAY_STREAM_HEADER_TIMEOUT"

// relayStreamHeaderTimeout returns the configured bound, or 0 when disabled.
//
// The default is 0 (disabled) so merging this changes nothing at runtime.
// Enabling it is a deliberate tuning decision that needs current numbers:
// the value must clear the slowest healthy time-to-first-token, not the
// average. Measured healthy streaming TTFT across 15 models was 1.0-4.7s
// (p95 up to ~8s), against the 49.3s stall -- so 30s separates them with
// roughly 4x headroom over the worst healthy case.
func relayStreamHeaderTimeout() time.Duration {
	seconds := common.GetEnvOrDefault(relayStreamHeaderTimeoutEnv, 0)
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// isStreamingRequest reports whether this upstream request expects SSE.
// relay/channel/api_request.go sets this Accept header for streaming relays.
func isStreamingRequest(req *http.Request) bool {
	if req == nil {
		return false
	}
	return strings.Contains(strings.ToLower(req.Header.Get("Accept")), "text/event-stream")
}

// cancelOnCloseBody releases the request context when the caller closes the
// body, so a stream that is abandoned does not leak its context.
type cancelOnCloseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelOnCloseBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// streamHeaderTimeoutRoundTripper applies the bound described above.
type streamHeaderTimeoutRoundTripper struct {
	base    http.RoundTripper
	timeout time.Duration
}

func (rt *streamHeaderTimeoutRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Non-streaming and disabled both take the untouched path. This is the
	// guarantee that 97.2% of traffic cannot regress.
	if rt.timeout <= 0 || !isStreamingRequest(req) {
		return rt.base.RoundTrip(req)
	}

	// WithCancel + AfterFunc rather than WithTimeout: the timer has to be
	// stoppable once headers arrive, and WithTimeout's deadline would keep
	// running and cancel the body mid-stream.
	ctx, cancel := context.WithCancel(req.Context())
	timer := time.AfterFunc(rt.timeout, cancel)

	resp, err := rt.base.RoundTrip(req.WithContext(ctx))
	if err != nil {
		timer.Stop()
		cancel()
		return nil, err
	}

	// Headers are in. Disarm before the body is read; from here the stream is
	// bounded only by STREAMING_TIMEOUT's inactivity watchdog.
	timer.Stop()
	resp.Body = &cancelOnCloseBody{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

// wrapRelayStreamHeaderTimeout wraps a relay transport when configured, and
// returns it untouched otherwise -- so at the default this is a no-op and the
// transport behaves exactly as upstream builds it.
func wrapRelayStreamHeaderTimeout(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		return nil
	}
	timeout := relayStreamHeaderTimeout()
	if timeout <= 0 {
		return base
	}
	return &streamHeaderTimeoutRoundTripper{base: base, timeout: timeout}
}
