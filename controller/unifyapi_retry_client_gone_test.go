package controller

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// UNIFYAPI-FORK: once the caller has hung up, a retry cannot deliver anything.
//
// Measured on 2026-11-08: an end client with a 45-second request timeout
// abandoned requests that had not produced a first token. Our relay cancels the
// upstream when that happens, the attempt fails, and upstream new-api then
// spends the remaining RetryTimes on a response nobody is waiting for. All
// three log rows for such a request landed in the SAME second, because attempts
// 2 and 3 fail instantly on the already-cancelled context -- so the cost is up
// to 3x the upstream calls, and the provider sees them as `context canceled`.

func ctxWithRequest(t *testing.T, cancelled bool) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req, err := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	require.NoError(t, err)
	reqCtx, cancel := context.WithCancel(context.Background())
	if cancelled {
		cancel()
	} else {
		t.Cleanup(cancel)
	}
	c.Request = req.WithContext(reqCtx)
	return c
}

// A channel error is the strongest "retry" signal in shouldRetry: IsChannelError
// returns true unconditionally, ahead of every other check. The client-gone
// guard has to sit in front of it, so this is the case that proves placement.
func TestShouldRetryStopsWhenClientHasGone(t *testing.T) {
	// A "channel:"-prefixed code is what IsChannelError recognises, and that
	// check returns true unconditionally -- ahead of SkipRetry, ahead of
	// retryTimes, ahead of the status-code ranges. If the client-gone guard
	// were placed after it, it would never run for this class of error.
	apiErr := types.NewErrorWithStatusCode(errors.New("no available key"),
		types.ErrorCodeChannelNoAvailableKey, http.StatusInternalServerError)
	require.True(t, types.IsChannelError(apiErr),
		"guard precondition: this must be an error shouldRetry would otherwise retry")

	t.Run("client still connected -> retry", func(t *testing.T) {
		require.True(t, shouldRetry(ctxWithRequest(t, false), apiErr, 2))
	})

	t.Run("client gone -> no retry", func(t *testing.T) {
		require.False(t, shouldRetry(ctxWithRequest(t, true), apiErr, 2),
			"a cancelled request context means nobody is waiting for the answer")
	})
}

// The same for a plain 500, which reaches shouldRetry through the status-code
// ranges rather than through IsChannelError. This is the shape produced by
// ErrorCodeDoRequestFailed, i.e. the 2026-11-08 failures themselves.
func TestShouldRetryStopsWhenClientHasGoneOnDoRequestFailed(t *testing.T) {
	apiErr := types.NewError(errors.New("upstream error: do request failed"),
		types.ErrorCodeDoRequestFailed)
	require.Equal(t, http.StatusInternalServerError, apiErr.StatusCode)

	require.True(t, shouldRetry(ctxWithRequest(t, false), apiErr, 2),
		"a live caller should still get the retry that rescues ~87% of these")
	require.False(t, shouldRetry(ctxWithRequest(t, true), apiErr, 2))
}

// The guard must not fire on a request we cannot inspect, or it would silently
// disable retry everywhere those paths are used.
func TestClientGoneIsFalseWithoutARequestContext(t *testing.T) {
	require.False(t, clientGone(nil))

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.False(t, clientGone(c), "no Request set at all")
}
