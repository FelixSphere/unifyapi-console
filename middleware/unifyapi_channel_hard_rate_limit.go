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
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// UNIFYAPI-FORK: what a caller sees when the only channels that could serve a
// request are hard-limited and full (model/unifyapi_channel_hard_rate_limit.go)
// or are ones their account has used up its own allowance on
// (model/unifyapi_channel_user_rate_limit.go).
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
	// Two codes, so that our own logs and a caller can tell a model that is
	// full for everyone from one account that has used up its own allowance.
	channelHardRateLimitCode types.ErrorCode = "rate_limit_exceeded"
	channelUserRateLimitCode types.ErrorCode = "user_rate_limit_exceeded"
)

type channelRateLimitRefusal struct {
	code       types.ErrorCode
	message    string
	retryAfter int
}

func channelHardRefusal(modelName string) channelRateLimitRefusal {
	retryAfter := model.ChannelRateLimitRetryAfter()
	return channelRateLimitRefusal{
		code:       channelHardRateLimitCode,
		message:    fmt.Sprintf("Rate limit reached for model %s: its per-minute limit is used up. Please retry in %d seconds.", modelName, retryAfter),
		retryAfter: retryAfter,
	}
}

// channelUserRefusal names the allowance that is used up. A daily one is a
// fixed UTC day, and says so: "retry in 40000 seconds" alone reads as a bug.
func channelUserRefusal(c *gin.Context, modelName string) channelRateLimitRefusal {
	refusal := service.ChannelUserLimitRefusal(c)
	if refusal == nil {
		refusal = &model.ChannelUserLimitRefusal{Limit: "requests per minute", RetryAfter: model.ChannelRateLimitRetryAfter()}
	}
	message := fmt.Sprintf("Rate limit reached for model %s: your account has used its %s allowance for this model.", modelName, refusal.Limit)
	if refusal.Daily {
		message += " It resets at 00:00 UTC."
	}
	return channelRateLimitRefusal{
		code:       channelUserRateLimitCode,
		message:    fmt.Sprintf("%s Please retry in %d seconds.", message, refusal.RetryAfter),
		retryAfter: refusal.RetryAfter,
	}
}

func isClaudeMessagesPath(c *gin.Context) bool {
	return c.Request != nil && c.Request.URL != nil && strings.HasPrefix(c.Request.URL.Path, "/v1/messages")
}

func noteChannelRateLimited(c *gin.Context, modelName string, refusal channelRateLimitRefusal) {
	c.Header("Retry-After", strconv.Itoa(refusal.retryAfter))
	logger.LogWarn(c, fmt.Sprintf("channel rate limit: refused user %d, model %q, path %s, code %s, retry after %ds",
		c.GetInt("id"), modelName, c.Request.URL.Path, refusal.code, refusal.retryAfter))
}

// abortChannelRateLimited answers a request that has not reached the relay.
func abortChannelRateLimited(c *gin.Context, modelName string, refusal channelRateLimitRefusal) {
	noteChannelRateLimited(c, modelName, refusal)
	noteRequestRefusal(c, refusal.message, string(refusal.code))
	message := common.MessageWithRequestId(refusal.message, c.GetString(common.RequestIdKey))
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
				"code":    refusal.code,
			},
		})
	}
	c.Abort()
}

func abortChannelHardRateLimited(c *gin.Context, modelName string) {
	abortChannelRateLimited(c, modelName, channelHardRefusal(modelName))
}

func abortChannelUserRateLimited(c *gin.Context, modelName string) {
	abortChannelRateLimited(c, modelName, channelUserRefusal(c, modelName))
}

// channelRateLimitedError is the same refusal for a request already inside
// the relay, which renders the error itself: a retry that found no channel it
// may use. It is not retried and not recorded as a channel error.
func channelRateLimitedError(c *gin.Context, modelName string, refusal channelRateLimitRefusal) *types.NewAPIError {
	noteChannelRateLimited(c, modelName, refusal)
	ops := []types.NewAPIErrorOptions{types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog()}
	if isClaudeMessagesPath(c) {
		return types.WithClaudeError(types.ClaudeError{Type: string(channelHardRateLimitType), Message: refusal.message}, http.StatusTooManyRequests, ops...)
	}
	return types.WithOpenAIError(types.OpenAIError{
		Message: refusal.message,
		Type:    string(channelHardRateLimitType),
		Code:    string(refusal.code),
	}, http.StatusTooManyRequests, ops...)
}

func ChannelHardRateLimitedError(c *gin.Context, modelName string) *types.NewAPIError {
	return channelRateLimitedError(c, modelName, channelHardRefusal(modelName))
}

func ChannelUserRateLimitedError(c *gin.Context, modelName string) *types.NewAPIError {
	return channelRateLimitedError(c, modelName, channelUserRefusal(c, modelName))
}
