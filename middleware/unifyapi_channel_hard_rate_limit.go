/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// UNIFYAPI-FORK: what a caller sees when the only channels that could serve a
// request are hard-limited and full (model/unifyapi_channel_hard_rate_limit.go).
//
// It is 429 with Retry-After, in the shape of the API the caller is using,
// because that is what OpenAI and Anthropic SDKs back off on by themselves. A
// 503 "no available channel" reads as our outage and counts in our 5xx alarm.
//
// Like the per-user limiter's refusal it writes no row to `logs`: nothing was
// sent upstream, nothing is billed, and an error row per refused request would
// make a customer's burst look like a failing model to anything that counts
// error rows. It is one line in the process log instead.

const (
	channelHardRateLimitType types.ErrorCode = "rate_limit_error"
	channelHardRateLimitCode types.ErrorCode = "rate_limit_exceeded"
)

func channelHardRateLimitMessage(modelName string, retryAfter int) string {
	return fmt.Sprintf("Rate limit reached for model %s: its per-minute limit is used up. Please retry in %d seconds.", modelName, retryAfter)
}

func isClaudeMessagesPath(c *gin.Context) bool {
	return c.Request != nil && c.Request.URL != nil && strings.HasPrefix(c.Request.URL.Path, "/v1/messages")
}

func noteChannelHardRateLimited(c *gin.Context, modelName string, retryAfter int) {
	c.Header("Retry-After", strconv.Itoa(retryAfter))
	logger.LogWarn(c, fmt.Sprintf("channel hard rate limit: refused user %d, model %q, path %s, retry after %ds",
		c.GetInt("id"), modelName, c.Request.URL.Path, retryAfter))
}

// abortChannelHardRateLimited answers a request that has not reached the
// relay yet.
func abortChannelHardRateLimited(c *gin.Context, modelName string) {
	retryAfter := model.ChannelRateLimitRetryAfter()
	noteChannelHardRateLimited(c, modelName, retryAfter)
	message := common.MessageWithRequestId(channelHardRateLimitMessage(modelName, retryAfter), c.GetString(common.RequestIdKey))
	if isClaudeMessagesPath(c) {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    channelHardRateLimitType,
				"message": message,
			},
		})
	} else {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error": gin.H{
				"message": message,
				"type":    channelHardRateLimitType,
				"code":    channelHardRateLimitCode,
			},
		})
	}
	c.Abort()
}

// ChannelHardRateLimitedError is the same refusal for a request already inside
// the relay, which renders the error itself: a retry that found nothing but
// full hard-limited channels. It is not retried and not recorded as a channel
// error.
func ChannelHardRateLimitedError(c *gin.Context, modelName string) *types.NewAPIError {
	retryAfter := model.ChannelRateLimitRetryAfter()
	noteChannelHardRateLimited(c, modelName, retryAfter)
	message := channelHardRateLimitMessage(modelName, retryAfter)
	ops := []types.NewAPIErrorOptions{types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog()}
	if isClaudeMessagesPath(c) {
		return types.WithClaudeError(types.ClaudeError{Type: string(channelHardRateLimitType), Message: message}, http.StatusTooManyRequests, ops...)
	}
	return types.WithOpenAIError(types.OpenAIError{
		Message: message,
		Type:    string(channelHardRateLimitType),
		Code:    string(channelHardRateLimitCode),
	}, http.StatusTooManyRequests, ops...)
}
