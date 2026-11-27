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
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// UNIFYAPI-FORK: a request refused before the relay is written to its owner's
// usage log.
//
// On 2026-11-27 a customer's requests were refused 403 by their key's IP allow
// list, and later 503 model_not_found, and none of it appeared on their usage
// log page: refusals in middleware never wrote a `logs` row, while the same
// customer's relay-stage errors did. They saw requests vanish.
//
// TokenAuth registers recordRejectedRequest once the key is known to be valid.
// It runs when the chain unwinds and writes a row only when a middleware
// aborted the request before Distribute handed it to the relay; past that
// point controller/relay.go writes its own error row, so marking the hand-off
// is what keeps a relay error from being logged twice.
const (
	rejectedRequestReachedRelayKey = "unifyapi_rejected_request_reached_relay"
	rejectedRequestMessageKey      = "unifyapi_rejected_request_message"
	rejectedRequestCodeKey         = "unifyapi_rejected_request_code"
	rejectedRequestModelKey        = "unifyapi_rejected_request_model"
)

// A key that leaked and is being tried from the wrong address is refused on
// every request, and each refusal would be a row in its owner's log. The cap
// keeps that from flooding their log table; the host log still has every one.
const rejectedRequestLogsPerTokenPerMinute = 60

// Tests replace these to make the write synchronous and the cap's minute fixed.
var (
	runRejectedRequestLogWrite = func(write func()) { go write() }
	rejectedRequestLogClock    = time.Now
)

type rejectedRequestLogLimiter struct {
	mu     sync.Mutex
	window int64
	counts map[int]int
}

// allow is a fixed one-minute window shared by every token, so the map holds
// only tokens refused in the current minute and is dropped when it turns.
func (l *rejectedRequestLogLimiter) allow(tokenId int, now time.Time) bool {
	window := now.Unix() / 60
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.counts == nil || window != l.window {
		l.window = window
		l.counts = make(map[int]int)
	}
	if l.counts[tokenId] >= rejectedRequestLogsPerTokenPerMinute {
		return false
	}
	l.counts[tokenId]++
	return true
}

var rejectedRequestLogs = &rejectedRequestLogLimiter{}

func noteRequestRefusal(c *gin.Context, message string, code string) {
	c.Set(rejectedRequestMessageKey, message)
	c.Set(rejectedRequestCodeKey, code)
}

func noteRequestedModel(c *gin.Context, modelName string) {
	c.Set(rejectedRequestModelKey, modelName)
}

func markReachedRelay(c *gin.Context) {
	c.Set(rejectedRequestReachedRelayKey, true)
}

func recordRejectedRequest(c *gin.Context, token *model.Token) {
	// Same switch as the relay's error rows: a deployment that keeps no error
	// rows gets none from here either.
	if !constant.ErrorLogEnabled || token == nil || !c.IsAborted() || c.GetBool(rejectedRequestReachedRelayKey) {
		return
	}
	status := c.Writer.Status()
	if status < http.StatusBadRequest {
		return
	}
	if !rejectedRequestLogs.allow(token.Id, rejectedRequestLogClock()) {
		return
	}
	message := c.GetString(rejectedRequestMessageKey)
	if message == "" {
		// The in-memory model rate limiter refuses with a bare status.
		message = fmt.Sprintf("HTTP %d", status)
	}
	row := &model.Log{
		UserId:    token.UserId,
		Username:  common.GetContextKeyString(c, constant.ContextKeyUserName),
		CreatedAt: common.GetTimestamp(),
		Type:      model.LogTypeError,
		Content:   message,
		TokenName: token.Name,
		TokenId:   token.Id,
		ModelName: c.GetString(rejectedRequestModelKey),
		Group:     common.GetContextKeyString(c, constant.ContextKeyUsingGroup),
		Ip:        c.ClientIP(),
		RequestId: c.GetString(common.RequestIdKey),
		Other: common.MapToJsonStr(map[string]interface{}{
			"rejected":     true,
			"status_code":  status,
			"error_code":   c.GetString(rejectedRequestCodeKey),
			"request_path": c.Request.URL.Path,
		}),
	}
	runRejectedRequestLogWrite(func() { model.RecordRejectedRequestLog(row) })
}
