package controller

import (
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

// UNIFYAPI-FORK: what one finished request says about the channel that served
// it, for service.SettleChannelAffinity. See that file for why it exists.

// affinityAbandonFloor separates a caller who gave up on a silent channel from
// one who simply interrupted. In the fifteen days to 2026-11-13 every request
// that was abandoned with no output had waited at least 160 s; a user pressing
// Esc does it within seconds. Below the floor an abandoned request says nothing
// about the channel.
const affinityAbandonFloor = 30 * time.Second

// errorCodeInvalidEncryptedContent is what an OpenAI upstream answers when a
// Responses request carries reasoning items another account encrypted, which
// is what a conversation looks like to the channel it has just been moved to.
// It is a 400 and so not retryable, but for an affinity key it means "this
// channel cannot continue this conversation", and counting it lets a key that
// was moved to such a channel move again.
const errorCodeInvalidEncryptedContent types.ErrorCode = "invalid_encrypted_content"

func affinityOutcome(c *gin.Context, info *relaycommon.RelayInfo, finalErr *types.NewAPIError) service.ChannelAffinityOutcome {
	if info == nil {
		return service.ChannelAffinityOutcomeUnknown
	}
	failed := false
	switch {
	case finalErr != nil:
		// LastError is the last error an upstream attempt returned. An error
		// raised without trying an upstream leaves it nil.
		if info.LastError == nil || !channelAtFault(info.LastError) {
			return service.ChannelAffinityOutcomeUnknown
		}
		failed = true
	case info.StreamStatus.HasErrors():
		failed = true
	case service.ChannelAffinityRequestHadNoOutput(c):
		if !clientGone(c) {
			return service.ChannelAffinityOutcomeUnknown
		}
		failed = true
	}
	if !failed {
		return service.ChannelAffinityOutcomeServed
	}
	if clientGone(c) && time.Since(info.StartTime) < affinityAbandonFloor {
		return service.ChannelAffinityOutcomeUnknown
	}
	return service.ChannelAffinityOutcomeChannelFailed
}

// channelAtFault reports whether another channel could have answered where
// this one returned err. It follows shouldRetry, with two differences: 504 and
// 524 count, because they are excluded from retry only so that one request
// does not wait out a second timeout, and so does invalid_encrypted_content.
func channelAtFault(err *types.NewAPIError) bool {
	if types.IsChannelError(err) {
		return true
	}
	if err.GetErrorCode() == errorCodeInvalidEncryptedContent {
		return true
	}
	if types.IsSkipRetryError(err) {
		return false
	}
	code := err.StatusCode
	if code >= 200 && code < 300 {
		return false
	}
	if code < 100 || code > 599 {
		return true
	}
	if operation_setting.IsAlwaysSkipRetryCode(err.GetErrorCode()) {
		return false
	}
	return operation_setting.ShouldRetryByStatusCode(code) || operation_setting.IsAlwaysSkipRetryStatusCode(code)
}
