package middleware

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withRelayLimit runs fn with the cap set to limit and restores it afterwards,
// so one test's ceiling cannot leak into the next.
func withRelayLimit(t *testing.T, limit int64, fn func()) {
	t.Helper()
	previous := common.GetMaxConcurrentRelays()
	common.SetMaxConcurrentRelays(limit)
	t.Cleanup(func() { common.SetMaxConcurrentRelays(previous) })
	require.EqualValues(t, 0, inFlightRelays.Load(), "a previous test leaked a permit")
	fn()
}

// waitForDrain gives in-flight handlers a moment to unwind before asserting the
// counter is back to zero. It fails rather than passing on a timeout.
func waitForDrain(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if inFlightRelays.Load() == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	require.FailNowf(t, "permit leak", "in-flight count never returned to 0, stuck at %d", inFlightRelays.Load())
}

func TestZeroMeansTheCapIsOffAndNothingIsCounted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withRelayLimit(t, 0, func() {
		var observed int64
		router := gin.New()
		router.Use(RelayConcurrencyLimit())
		router.POST("/v1/chat/completions", func(c *gin.Context) {
			// With the cap off the middleware must not even reserve a permit,
			// or an operator turning it on later inherits a bogus baseline.
			observed = inFlightRelays.Load()
			c.String(http.StatusOK, "ok")
		})

		for i := 0; i < 50; i++ {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
			require.Equal(t, http.StatusOK, w.Code)
		}
		assert.EqualValues(t, 0, observed, "the cap being off must not touch the counter")
	})
}

func TestTheCapHoldsUnderRealConcurrency(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const limit = 4
	const callers = 60

	withRelayLimit(t, limit, func() {
		var inHandler, peak, admitted, refused atomic.Int64
		release := make(chan struct{})

		router := gin.New()
		router.Use(RelayConcurrencyLimit())
		router.POST("/v1/chat/completions", func(c *gin.Context) {
			current := inHandler.Add(1)
			for {
				was := peak.Load()
				if current <= was || peak.CompareAndSwap(was, current) {
					break
				}
			}
			<-release // hold the slot like a streaming response does
			inHandler.Add(-1)
			c.String(http.StatusOK, "ok")
		})

		var wg sync.WaitGroup
		for i := 0; i < callers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
				if w.Code == http.StatusServiceUnavailable {
					refused.Add(1)
					return
				}
				admitted.Add(1)
			}()
		}

		// Let the admitted callers pile up against the cap before releasing.
		time.Sleep(150 * time.Millisecond)
		close(release)
		wg.Wait()

		assert.LessOrEqual(t, peak.Load(), int64(limit),
			"more handlers ran at once than the cap allows")
		assert.EqualValues(t, callers, admitted.Load()+refused.Load(), "every caller got an answer")
		assert.Positive(t, refused.Load(), "with 60 callers against a cap of 4 something must be shed")
		waitForDrain(t)
	})
}

// TestCallersArrivingTogetherCannotBothTakeTheLastSlot pins the ORDERING inside
// the middleware, not just the cap. Reading the count and then incrementing it
// leaves a window in which every caller sees room and every caller takes it.
//
// A race cannot be forced from outside, so this releases many callers from one
// barrier against a cap of 1 and repeats it: mutating the middleware to
// check-then-increment breaches the cap on ~70% of single rounds, which over
// these rounds is a certainty, while correct code cannot breach it even once.
// The 60-caller test above does not catch that mutation reliably, because it
// needs five simultaneous arrivals rather than two.
func TestCallersArrivingTogetherCannotBothTakeTheLastSlot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const callersPerRound = 100
	const rounds = 12

	withRelayLimit(t, 1, func() {
		var inHandler, peak atomic.Int64

		for round := 0; round < rounds; round++ {
			start := make(chan struct{})
			release := make(chan struct{})

			// Built per round so the handler closes over this round's channel
			// rather than sharing one variable across goroutines.
			router := gin.New()
			router.Use(RelayConcurrencyLimit())
			router.POST("/v1/chat/completions", func(c *gin.Context) {
				current := inHandler.Add(1)
				for {
					was := peak.Load()
					if current <= was || peak.CompareAndSwap(was, current) {
						break
					}
				}
				<-release
				inHandler.Add(-1)
				c.String(http.StatusOK, "ok")
			})

			var ready, wg sync.WaitGroup
			ready.Add(callersPerRound)
			for i := 0; i < callersPerRound; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					ready.Done()
					<-start // every caller enters the middleware at the same instant
					w := httptest.NewRecorder()
					router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
				}()
			}
			ready.Wait()
			close(start)
			time.Sleep(20 * time.Millisecond)
			close(release)
			wg.Wait()

			require.EqualValues(t, 1, peak.Load(),
				"round %d: a cap of 1 admitted %d at once — the count is read before it is reserved",
				round+1, peak.Load())
		}
		waitForDrain(t)
	})
}

func TestCapacityComesBackAfterABurstIsShed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withRelayLimit(t, 2, func() {
		release := make(chan struct{})
		entered := make(chan struct{}, 8)

		router := gin.New()
		router.Use(RelayConcurrencyLimit())
		router.POST("/v1/chat/completions", func(c *gin.Context) {
			entered <- struct{}{}
			<-release
			c.String(http.StatusOK, "ok")
		})

		var wg sync.WaitGroup
		var refused atomic.Int64
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
				if w.Code == http.StatusServiceUnavailable {
					refused.Add(1)
				}
			}()
		}
		time.Sleep(100 * time.Millisecond)
		close(release)
		wg.Wait()
		require.Positive(t, refused.Load(), "the burst should have overflowed the cap")
		waitForDrain(t)

		// The rejected requests must not have consumed anything permanently:
		// a fresh caller is served immediately.
		w := httptest.NewRecorder()
		router2 := gin.New()
		router2.Use(RelayConcurrencyLimit())
		router2.POST("/v1/chat/completions", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
		router2.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
		assert.Equal(t, http.StatusOK, w.Code, "capacity did not recover after the burst")
	})
}

func TestThePermitIsReleasedOnEveryExitPath(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name    string
		handler gin.HandlerFunc
	}{
		{"normal finish", func(c *gin.Context) { c.String(http.StatusOK, "ok") }},
		{"handler aborts with an error", func(c *gin.Context) {
			c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": "upstream said no"})
		}},
		{"handler writes nothing at all", func(c *gin.Context) {}},
		{"handler panics", func(c *gin.Context) { panic("upstream adaptor blew up") }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withRelayLimit(t, 1, func() {
				router := gin.New()
				router.Use(gin.Recovery()) // as the real server does, outside our middleware
				router.Use(RelayConcurrencyLimit())
				router.POST("/v1/chat/completions", tc.handler)

				for i := 0; i < 3; i++ {
					w := httptest.NewRecorder()
					router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
					require.NotEqual(t, http.StatusServiceUnavailable, w.Code,
						"request %d was refused, so the previous one never gave its permit back", i+1)
				}
				waitForDrain(t)
			})
		})
	}
}

func TestAClientHangingUpMidStreamGivesThePermitBack(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withRelayLimit(t, 1, func() {
		handlerReturned := make(chan struct{})

		router := gin.New()
		router.POST("/v1/chat/completions", RelayConcurrencyLimit(), func(c *gin.Context) {
			defer close(handlerReturned)
			c.Writer.Header().Set("Content-Type", "text/event-stream")
			c.Writer.WriteHeader(http.StatusOK)
			_, _ = c.Writer.Write([]byte("data: {}\n\n"))
			c.Writer.Flush()
			// The real stream scanner ends on this signal; see
			// StreamEndReasonClientGone in relay/helper/stream_scanner.go.
			<-c.Request.Context().Done()
		})

		server := httptest.NewServer(router)
		defer server.Close()

		ctx, cancel := context.WithCancel(context.Background())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/chat/completions", nil)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)

		buf := make([]byte, 10)
		_, err = io.ReadFull(resp.Body, buf)
		require.NoError(t, err, "the stream should have started")
		require.EqualValues(t, 1, inFlightRelays.Load(), "the streaming request should hold a permit")

		cancel() // the caller hangs up mid-stream
		_ = resp.Body.Close()

		select {
		case <-handlerReturned:
		case <-time.After(2 * time.Second):
			require.FailNow(t, "the handler never noticed the client was gone")
		}
		waitForDrain(t)
	})
}

func TestTheRefusalIsAUsableErrorInBothEnvelopes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withRelayLimit(t, 1, func() {
		release := make(chan struct{})
		started := make(chan struct{})

		router := gin.New()
		router.Use(RelayConcurrencyLimit())
		handler := func(c *gin.Context) {
			select {
			case started <- struct{}{}:
			default:
			}
			<-release
			c.String(http.StatusOK, "ok")
		}
		router.POST("/v1/chat/completions", handler)
		router.POST("/v1/messages", handler)

		go func() {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
		}()
		<-started

		// The two SDK families parse different shapes. The OpenAI envelope
		// carries param/code, the Claude one carries only type/message; sending
		// the wrong one — or a bare 503 with no body at all, which is what an
		// earlier limiter here did — leaves the client with no error to raise.
		for _, tc := range []struct {
			path      string
			wantsCode bool
		}{
			{"/v1/chat/completions", true},
			{"/v1/messages", false},
		} {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, tc.path, nil))
			require.Equal(t, http.StatusServiceUnavailable, w.Code, tc.path)
			assert.Equal(t, "1", w.Header().Get("Retry-After"), "%s: an SDK needs to know when to retry", tc.path)

			var body map[string]any
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &body), "%s: the refusal must be JSON, not empty", tc.path)
			errObj, ok := body["error"].(map[string]any)
			require.True(t, ok, "%s: refusal has no error object: %s", tc.path, w.Body.String())
			assert.Contains(t, fmt.Sprint(errObj["message"]), "concurrency limit",
				"%s: the message must say why, or an operator cannot tell it from an upstream 503", tc.path)

			if tc.wantsCode {
				assert.Equal(t, "server_busy", errObj["code"],
					"%s: the OpenAI envelope carries the machine-readable code", tc.path)
				assert.Contains(t, errObj, "param", "%s: OpenAI clients expect the full envelope", tc.path)
				continue
			}
			assert.NotContains(t, errObj, "code", "%s: this is the Claude envelope, not the OpenAI one", tc.path)
			assert.Equal(t, "new_api_error", errObj["type"], tc.path)
		}

		close(release)
		waitForDrain(t)
	})
}

func TestANegativeSettingTurnsTheCapOffRatherThanRefusingEverything(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withRelayLimit(t, -5, func() {
		require.EqualValues(t, 0, common.GetMaxConcurrentRelays())

		router := gin.New()
		router.Use(RelayConcurrencyLimit())
		router.POST("/v1/chat/completions", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
		assert.Equal(t, http.StatusOK, w.Code, "a mistyped cap must degrade to off, not to an outage")
	})
}
