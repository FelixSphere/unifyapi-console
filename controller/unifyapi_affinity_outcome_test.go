package controller

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type outcomeCase struct {
	// what the upstream attempt returned, and what the request ended with
	lastErr, finalErr *types.NewAPIError
	streamErrors      int
	noOutput          bool
	clientGone        bool
	elapsed           time.Duration
}

func (tc outcomeCase) run(t *testing.T) service.ChannelAffinityOutcome {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
	if tc.clientGone {
		cancel()
	}
	info := &relaycommon.RelayInfo{StartTime: time.Now().Add(-tc.elapsed), LastError: tc.lastErr}
	if tc.streamErrors > 0 {
		info.StreamStatus = relaycommon.NewStreamStatus()
		for i := 0; i < tc.streamErrors; i++ {
			info.StreamStatus.RecordError("upstream_stream_error: server_error: Internal server error")
		}
	}
	if tc.noOutput {
		// what PostTextConsumeQuota does when there is nothing to bill
		c.Set("unifyapi_channel_affinity_no_output", true)
		require.True(t, service.ChannelAffinityRequestHadNoOutput(c))
	}
	return affinityOutcome(c, info, tc.finalErr)
}

func upstreamStatus(code types.ErrorCode, status int) *types.NewAPIError {
	return types.NewOpenAIError(errors.New("upstream said no"), code, status)
}

func TestAffinityOutcome(t *testing.T) {
	doRequestFailed := upstreamStatus(types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	encrypted := types.WithOpenAIError(types.OpenAIError{
		Message: "The encrypted content for item rs_0 could not be verified.",
		Type:    "invalid_request_error",
		Code:    "invalid_encrypted_content",
	}, http.StatusBadRequest)
	ourOwn403 := types.NewErrorWithStatusCode(errors.New("quota"), types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry())
	noChannelOnRetry := types.NewError(errors.New("no channel"), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())

	const served, failed, unknown = service.ChannelAffinityOutcomeServed, service.ChannelAffinityOutcomeChannelFailed, service.ChannelAffinityOutcomeUnknown
	cases := []struct {
		name string
		tc   outcomeCase
		want service.ChannelAffinityOutcome
	}{
		{"a clean answer", outcomeCase{elapsed: 4 * time.Second}, served},
		{"interrupted after output had arrived (log 119546)", outcomeCase{clientGone: true, elapsed: 4 * time.Second}, served},

		// The three shapes of 2026-11-13, all of which answered HTTP 200.
		{"abandoned after 165 s with no output (log 410137)", outcomeCase{noOutput: true, clientGone: true, elapsed: 165 * time.Second}, failed},
		{"error inside the stream (log 410150)", outcomeCase{streamErrors: 2, noOutput: true, elapsed: 20 * time.Second}, failed},
		{"error inside the stream after some output", outcomeCase{streamErrors: 1, elapsed: 20 * time.Second}, failed},

		{"upstream 500", outcomeCase{lastErr: doRequestFailed, finalErr: doRequestFailed}, failed},
		{"upstream 520", outcomeCase{lastErr: upstreamStatus(types.ErrorCodeBadResponseStatusCode, 520), finalErr: upstreamStatus(types.ErrorCodeBadResponseStatusCode, 520)}, failed},
		{"upstream 429", outcomeCase{lastErr: upstreamStatus(types.ErrorCodeBadResponseStatusCode, 429), finalErr: upstreamStatus(types.ErrorCodeBadResponseStatusCode, 429)}, failed},
		{"upstream 504, which is not retried but is the channel's doing", outcomeCase{lastErr: upstreamStatus(types.ErrorCodeBadResponseStatusCode, 504), finalErr: upstreamStatus(types.ErrorCodeBadResponseStatusCode, 504)}, failed},
		{"upstream 524", outcomeCase{lastErr: upstreamStatus(types.ErrorCodeBadResponseStatusCode, 524), finalErr: upstreamStatus(types.ErrorCodeBadResponseStatusCode, 524)}, failed},
		{"a channel-level error", outcomeCase{lastErr: types.NewError(errors.New("bad key"), types.ErrorCodeChannelInvalidKey), finalErr: types.NewError(errors.New("bad key"), types.ErrorCodeChannelInvalidKey)}, failed},
		{"reasoning the new channel cannot decrypt", outcomeCase{lastErr: encrypted, finalErr: encrypted}, failed},
		{"retry found no channel after an upstream failure", outcomeCase{lastErr: doRequestFailed, finalErr: noChannelOnRetry}, failed},
		{"the caller waited 45 s for headers and hung up", outcomeCase{lastErr: doRequestFailed, finalErr: doRequestFailed, clientGone: true, elapsed: 45 * time.Second}, failed},

		// Nothing here is evidence against the channel.
		{"our own 403 before any upstream was tried", outcomeCase{finalErr: ourOwn403}, unknown},
		{"upstream 400: the request itself is wrong", outcomeCase{lastErr: upstreamStatus(types.ErrorCodeBadResponseStatusCode, 400), finalErr: upstreamStatus(types.ErrorCodeBadResponseStatusCode, 400)}, unknown},
		{"a body we could not parse", outcomeCase{lastErr: upstreamStatus(types.ErrorCodeBadResponseBody, 500), finalErr: upstreamStatus(types.ErrorCodeBadResponseBody, 500)}, unknown},
		{"an error marked skip-retry", outcomeCase{lastErr: types.NewErrorWithStatusCode(errors.New("x"), types.ErrorCodeBadResponseStatusCode, 500, types.ErrOptionWithSkipRetry()), finalErr: doRequestFailed}, unknown},
		{"Esc before anything arrived", outcomeCase{noOutput: true, clientGone: true, elapsed: 3 * time.Second}, unknown},
		{"Esc while the request was failing anyway", outcomeCase{lastErr: doRequestFailed, finalErr: doRequestFailed, clientGone: true, elapsed: 3 * time.Second}, unknown},
		{"Esc during a stream that had reported an error", outcomeCase{streamErrors: 1, clientGone: true, elapsed: 3 * time.Second}, unknown},
		{"an empty answer the caller did receive", outcomeCase{noOutput: true, elapsed: 2 * time.Second}, unknown},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.tc.run(t))
		})
	}

	require.Equal(t, unknown, affinityOutcome(nil, nil, nil))
}

func TestAbandonFloorBoundary(t *testing.T) {
	require.Equal(t, service.ChannelAffinityOutcomeUnknown,
		outcomeCase{noOutput: true, clientGone: true, elapsed: affinityAbandonFloor - time.Second}.run(t))
	require.Equal(t, service.ChannelAffinityOutcomeChannelFailed,
		outcomeCase{noOutput: true, clientGone: true, elapsed: affinityAbandonFloor + time.Second}.run(t))
}
