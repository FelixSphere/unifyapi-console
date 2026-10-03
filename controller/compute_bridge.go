/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package controller

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// Compute billing bridge: server-to-server calls from compute-api, which has no
// users or wallet of its own and moves quota only through these routes. The
// contract is docs/compute-bridge.md. The HMAC is the only credential, so the
// routes are dark (404) until COMPUTE_BRIDGE_SECRET is set.

const (
	computeBridgeTimestampHeader = "X-Compute-Timestamp"
	computeBridgeSignatureHeader = "X-Compute-Signature"
	computeBridgeMaxBody         = 32768
	computeBridgeWindowSeconds   = 30
	computeBridgeBodyKey         = "compute_bridge_body"
	computeBridgeIdMaxLen        = 64
	computeHoldSweepInterval     = time.Minute
	computeHoldSweepBatch        = 100
	computeBridgeDefaultRate     = 50
	computeBridgeDefaultBurst    = 100
)

var errComputeBridgeTimestamp = errors.New("timestamp missing or outside the window")

func computeBridgeSecret() string {
	secret := os.Getenv("COMPUTE_BRIDGE_SECRET")
	if len(secret) < 32 {
		return ""
	}
	return secret
}

// verifyComputeBridgeRequest checks the signature over
// METHOD \n PATH \n TIMESTAMP \n BODY and returns the exact body it covered.
func verifyComputeBridgeRequest(c *gin.Context, secret string, now int64) ([]byte, error) {
	timestamp := c.GetHeader(computeBridgeTimestampHeader)
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || seconds < now-computeBridgeWindowSeconds || seconds > now+computeBridgeWindowSeconds {
		return nil, errComputeBridgeTimestamp
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, computeBridgeMaxBody+1))
	if err != nil || len(body) > computeBridgeMaxBody {
		return nil, errors.New("invalid body")
	}
	signature, err := hex.DecodeString(c.GetHeader(computeBridgeSignatureHeader))
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(c.Request.Method + "\n" + c.Request.URL.Path + "\n" + timestamp + "\n"))
	mac.Write(body)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return nil, errors.New("invalid signature")
	}
	return body, nil
}

// computeBridgeRateLimiter is one token bucket for the whole bridge on this
// node. The global API limiter counts per client IP, and every bridge call
// comes from compute-api's one address, so it would cap all compute billing at
// a few requests a second; this replaces it for these routes.
type computeBridgeRateLimiter struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

func newComputeBridgeRateLimiter(rate float64, burst float64) *computeBridgeRateLimiter {
	return &computeBridgeRateLimiter{rate: rate, burst: burst, tokens: burst}
}

// take spends one token, or reports how long until one is available.
func (l *computeBridgeRateLimiter) take(now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.last.IsZero() {
		l.tokens = math.Min(l.burst, l.tokens+now.Sub(l.last).Seconds()*l.rate)
	}
	l.last = now
	if l.tokens >= 1 {
		l.tokens--
		return 0
	}
	return time.Duration((1 - l.tokens) / l.rate * float64(time.Second))
}

// parseComputeBridgeRateLimit reads COMPUTE_BRIDGE_RATE_LIMIT as "RATE" or
// "RATE:BURST" in requests per second. A malformed value keeps the default
// rather than leaving the bridge unlimited or shut.
func parseComputeBridgeRateLimit(value string) (float64, float64) {
	rate, burst := float64(computeBridgeDefaultRate), float64(computeBridgeDefaultBurst)
	value = strings.TrimSpace(value)
	if value == "" {
		return rate, burst
	}
	rateText, burstText, hasBurst := strings.Cut(value, ":")
	parsedRate, err := strconv.ParseFloat(rateText, 64)
	if err != nil || parsedRate <= 0 {
		common.SysError("compute bridge: ignoring malformed COMPUTE_BRIDGE_RATE_LIMIT " + strconv.Quote(value))
		return rate, burst
	}
	parsedBurst := parsedRate * 2
	if hasBurst {
		if parsedBurst, err = strconv.ParseFloat(burstText, 64); err != nil || parsedBurst < 1 {
			common.SysError("compute bridge: ignoring malformed COMPUTE_BRIDGE_RATE_LIMIT " + strconv.Quote(value))
			return rate, burst
		}
	}
	return parsedRate, math.Max(parsedBurst, 1)
}

// The limiter is built on first use rather than at package init, because the
// environment (including a .env file) is loaded by main after init has run.
var (
	computeBridgeLimiter     *computeBridgeRateLimiter
	computeBridgeLimiterOnce sync.Once
)

func computeBridgeRateLimit() *computeBridgeRateLimiter {
	computeBridgeLimiterOnce.Do(func() {
		computeBridgeLimiter = newComputeBridgeRateLimiter(parseComputeBridgeRateLimit(os.Getenv("COMPUTE_BRIDGE_RATE_LIMIT")))
	})
	return computeBridgeLimiter
}

// ComputeBridgeAuth gates every /api/compute/v1 route. It replaces session and
// token auth for these routes rather than adding to them.
func ComputeBridgeAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		secret := computeBridgeSecret()
		if secret == "" {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		// The server-wide recovery answers a panic with its own body, which
		// carries the panic text and no machine code. compute-api reads only
		// message, so a bridge panic is internal_error and the detail is logged.
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			if recovered == http.ErrAbortHandler {
				panic(recovered)
			}
			common.SysError(fmt.Sprintf("compute bridge: panic: %v", recovered))
			if c.Writer.Written() {
				c.Abort()
				return
			}
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"success": false, "message": "internal_error", "code": "internal_error"})
		}()
		if wait := computeBridgeRateLimit().take(time.Now()); wait > 0 {
			c.Header("Retry-After", strconv.FormatInt(int64(math.Ceil(wait.Seconds())), 10))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"success": false, "message": "rate_limited", "code": "rate_limited"})
			return
		}
		// Size is settled before the timestamp or signature is looked at: an
		// oversized body used to surface as 401 invalid_signature, which sends
		// the caller hunting for a secret mismatch that is not there.
		raw, err := io.ReadAll(io.LimitReader(c.Request.Body, computeBridgeMaxBody+1))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid_request", "code": "invalid_request"})
			return
		}
		if len(raw) > computeBridgeMaxBody {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"success": false, "message": "payload_too_large", "code": "payload_too_large"})
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(raw))
		body, err := verifyComputeBridgeRequest(c, secret, time.Now().Unix())
		if err != nil {
			reason := "invalid_signature"
			if errors.Is(err, errComputeBridgeTimestamp) {
				reason = "invalid_timestamp"
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "message": reason, "code": reason})
			return
		}
		c.Set(computeBridgeBodyKey, body)
		c.Next()
	}
}

func computeBridgeOK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": data})
}

// computeBridgeFail answers with a stable machine-readable code in message,
// which is what compute-api reads, and the same value in code.
func computeBridgeFail(c *gin.Context, status int, message string, data any) {
	body := gin.H{"success": false, "message": message, "code": message}
	if data != nil {
		body["data"] = data
	}
	c.JSON(status, body)
}

func readComputeBridgeBody(c *gin.Context, v any) bool {
	body, _ := c.Get(computeBridgeBodyKey)
	raw, _ := body.([]byte)
	if err := common.Unmarshal(raw, v); err != nil {
		computeBridgeFail(c, http.StatusBadRequest, "invalid_request", nil)
		return false
	}
	return true
}

func validComputeBridgeId(id string) bool {
	return id != "" && len(id) <= computeBridgeIdMaxLen
}

func writeComputeBridgeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrInsufficientBillingQuota), errors.Is(err, model.ErrComputeTokenQuotaShort):
		computeBridgeFail(c, http.StatusPaymentRequired, "insufficient_balance", nil)
	case errors.Is(err, model.ErrComputeHoldNotFound):
		computeBridgeFail(c, http.StatusNotFound, err.Error(), nil)
	case errors.Is(err, model.ErrComputeQuotaOutOfRange):
		computeBridgeFail(c, http.StatusBadRequest, "invalid_request", nil)
	case errors.Is(err, model.ErrComputeSettlementRegressed):
		computeBridgeFail(c, http.StatusConflict, "cumulative_quota_regressed", nil)
	case errors.Is(err, model.ErrComputeHoldExpired),
		errors.Is(err, model.ErrComputeHoldClosed),
		errors.Is(err, model.ErrComputeHoldExceeded),
		errors.Is(err, model.ErrComputeHoldConflict),
		errors.Is(err, model.ErrComputeEventConflict),
		errors.Is(err, model.ErrComputeIdempotencyConflict):
		computeBridgeFail(c, http.StatusConflict, err.Error(), nil)
	default:
		// The detail goes to the log; message stays a machine code.
		common.SysError("compute bridge: " + err.Error())
		computeBridgeFail(c, http.StatusInternalServerError, "internal_error", nil)
	}
}

// computeBillingAccess applies the user half of TokenAuth: a disabled login is
// refused, and so is every member of a suspended tenant. Suspension disables
// member logins (model/tenant_lifecycle.go); the tenant row is read too so a
// member the suspension skipped (an admin role) is still refused.
func computeBillingAccess(c *gin.Context, userId int) (*model.UserBase, bool) {
	userCache, err := model.GetUserCache(userId)
	if err != nil {
		writeComputeBridgeError(c, err)
		return nil, false
	}
	if userCache.TenantId > 0 {
		tenant, err := model.GetTenantById(userCache.TenantId)
		if err != nil {
			writeComputeBridgeError(c, err)
			return nil, false
		}
		if tenant.IsSuspended() {
			computeBridgeFail(c, http.StatusForbidden, "suspended", gin.H{"status": "suspended"})
			return nil, false
		}
	}
	if userCache.Status != common.UserStatusEnabled {
		computeBridgeFail(c, http.StatusForbidden, "disabled", gin.H{"status": "disabled"})
		return nil, false
	}
	return userCache, true
}

// computeHoldToken loads the token a hold draws on and refuses it on the same
// terms ValidateUserToken refuses a key, minus the exhaustion check, which the
// hold itself makes against the amount requested.
func computeHoldToken(c *gin.Context, tokenId int, userId int) (*model.Token, bool) {
	token, err := model.GetTokenById(tokenId)
	if err != nil || token.UserId != userId || token.Status != common.TokenStatusEnabled ||
		(token.ExpiredTime != -1 && token.ExpiredTime < common.GetTimestamp()) {
		computeBridgeFail(c, http.StatusUnauthorized, "invalid_key", nil)
		return nil, false
	}
	return token, true
}

func computeEntityQuota(userId int) int {
	quota, err := model.GetUserQuota(userId, true)
	if err != nil {
		common.SysError("compute bridge: reading balance failed: " + err.Error())
	}
	return quota
}

func ComputeVerify(c *gin.Context) {
	var request struct {
		Key string `json:"key"`
	}
	if !readComputeBridgeBody(c, &request) {
		return
	}
	// Same key normalisation as TokenAuth: optional sk- prefix, and anything
	// after the first dash is a channel selector, not part of the key.
	key := strings.TrimPrefix(strings.TrimSpace(request.Key), "sk-")
	key = strings.Split(key, "-")[0]
	token, err := model.ValidateUserToken(key)
	if err != nil {
		if errors.Is(err, model.ErrDatabase) {
			writeComputeBridgeError(c, err)
			return
		}
		computeBridgeFail(c, http.StatusUnauthorized, "invalid_key", nil)
		return
	}
	userCache, ok := computeBillingAccess(c, token.UserId)
	if !ok {
		return
	}
	remain, err := model.GetUserQuota(token.UserId, false)
	if err != nil {
		writeComputeBridgeError(c, err)
		return
	}
	computeBridgeOK(c, gin.H{
		"user_id":            token.UserId,
		"tenant_id":          userCache.TenantId,
		"token_id":           token.Id,
		"group":              userCache.Group,
		"status":             "active",
		"remain_quota":       remain,
		"token_unlimited":    token.UnlimitedQuota,
		"token_remain_quota": token.RemainQuota,
	})
}

func ComputeCreateHold(c *gin.Context) {
	var request struct {
		HoldId    string `json:"hold_id"`
		UserId    int    `json:"user_id"`
		TokenId   int    `json:"token_id"`
		Quota     int64  `json:"quota"`
		JobId     string `json:"job_id"`
		Sku       string `json:"sku"`
		ExpiresAt int64  `json:"expires_at"`
	}
	if !readComputeBridgeBody(c, &request) {
		return
	}
	now := common.GetTimestamp()
	if !validComputeBridgeId(request.HoldId) || !validComputeBridgeId(request.JobId) || !validComputeBridgeId(request.Sku) ||
		request.UserId <= 0 || request.TokenId <= 0 || request.ExpiresAt <= now {
		computeBridgeFail(c, http.StatusBadRequest, "invalid_request", nil)
		return
	}
	if request.Quota <= 0 || request.Quota > model.MaxComputeQuota {
		writeComputeBridgeError(c, model.ErrComputeQuotaOutOfRange)
		return
	}
	requested := model.ComputeHold{
		HoldId:    request.HoldId,
		UserId:    request.UserId,
		TokenId:   request.TokenId,
		JobId:     request.JobId,
		Sku:       request.Sku,
		Quota:     int(request.Quota),
		ExpiresAt: request.ExpiresAt,
	}
	// A replay is answered before the token and login are re-checked: the
	// quota is already held, and refusing the retry would leave the caller
	// believing it holds nothing until the sweeper returns it.
	var token *model.Token
	if _, err := model.GetComputeHold(request.HoldId); errors.Is(err, model.ErrComputeHoldNotFound) {
		var ok bool
		if token, ok = computeHoldToken(c, request.TokenId, request.UserId); !ok {
			return
		}
		if _, ok = computeBillingAccess(c, request.UserId); !ok {
			return
		}
	} else if err != nil {
		writeComputeBridgeError(c, err)
		return
	}
	hold, _, err := model.CreateComputeHold(requested, token, now)
	if err != nil {
		writeComputeBridgeError(c, err)
		return
	}
	computeBridgeOK(c, gin.H{
		"hold_id":      hold.HoldId,
		"quota":        hold.Quota,
		"remain_quota": computeEntityQuota(hold.UserId),
	})
}

func ComputeSettleHold(c *gin.Context) {
	var request struct {
		EventId         string `json:"event_id"`
		CumulativeQuota int64  `json:"cumulative_quota"`
		GpuSeconds      int64  `json:"gpu_seconds"`
		Sku             string `json:"sku"`
		Final           bool   `json:"final"`
	}
	if !readComputeBridgeBody(c, &request) {
		return
	}
	holdId := c.Param("hold_id")
	if !validComputeBridgeId(request.EventId) || !validComputeBridgeId(holdId) {
		computeBridgeFail(c, http.StatusBadRequest, "invalid_request", nil)
		return
	}
	if request.CumulativeQuota < 0 || request.CumulativeQuota > model.MaxComputeQuota || request.GpuSeconds < 0 {
		writeComputeBridgeError(c, model.ErrComputeQuotaOutOfRange)
		return
	}
	hold, err := model.GetComputeHold(holdId)
	if err != nil {
		writeComputeBridgeError(c, err)
		return
	}
	// The consume log is labelled with the hold's SKU; a settlement claiming a
	// different one is a caller bug that would otherwise be logged silently.
	if request.Sku != "" && request.Sku != hold.Sku {
		computeBridgeFail(c, http.StatusConflict, "sku_mismatch", nil)
		return
	}
	result, err := model.SettleComputeHold(holdId, model.ComputeSettlement{
		EventId:         request.EventId,
		CumulativeQuota: int(request.CumulativeQuota),
		GpuSeconds:      request.GpuSeconds,
	}, request.Final, common.GetTimestamp())
	if err != nil {
		writeComputeBridgeError(c, err)
		return
	}
	if !result.Replayed && result.Delta > 0 {
		recordComputeConsumption(c, result.Hold, result.Delta, request.EventId, request.GpuSeconds)
	}
	settled := result.Hold
	computeBridgeOK(c, gin.H{
		"hold_id":        settled.HoldId,
		"settled_quota":  settled.SettledQuota,
		"hold_remaining": settled.Quota - settled.SettledQuota - settled.ReleasedQuota,
	})
}

// recordComputeConsumption writes the spend through the same consume-log and
// used-quota paths the relay uses, so statements, tenant usage and
// reconciliation pick compute up without changes. Channel 0 keeps the
// supplier credit-lot draw-down and channel TPM accounting out of it.
func recordComputeConsumption(c *gin.Context, hold *model.ComputeHold, delta int, eventId string, gpuSeconds int64) {
	group := ""
	if userCache, err := model.GetUserCache(hold.UserId); err == nil {
		userCache.WriteContext(c)
		group = userCache.Group
	}
	tokenName := ""
	if token, err := model.GetTokenById(hold.TokenId); err == nil {
		tokenName = token.Name
	}
	model.RecordConsumeLog(c, hold.UserId, model.RecordConsumeLogParams{
		ModelName: "compute/" + hold.Sku,
		TokenName: tokenName,
		TokenId:   hold.TokenId,
		Quota:     delta,
		Group:     group,
		Other: map[string]interface{}{
			"gpu_seconds": gpuSeconds,
			"job_id":      hold.JobId,
			"hold_id":     hold.HoldId,
			"event_id":    eventId,
		},
	})
	model.UpdateUserUsedQuotaAndRequestCount(hold.UserId, delta)
}

func ComputeExtendHold(c *gin.Context) {
	var request struct {
		ExtendId        string `json:"extend_id"`
		AdditionalQuota int64  `json:"additional_quota"`
	}
	if !readComputeBridgeBody(c, &request) {
		return
	}
	if !validComputeBridgeId(request.ExtendId) {
		computeBridgeFail(c, http.StatusBadRequest, "invalid_request", nil)
		return
	}
	if request.AdditionalQuota <= 0 || request.AdditionalQuota > model.MaxComputeQuota {
		writeComputeBridgeError(c, model.ErrComputeQuotaOutOfRange)
		return
	}
	hold, err := model.GetComputeHold(c.Param("hold_id"))
	if err != nil {
		writeComputeBridgeError(c, err)
		return
	}
	// As with holds, a replayed extend id is answered before the token and
	// login are re-checked: its reservation already exists.
	var token *model.Token
	seen, err := model.GetComputeExtension(request.ExtendId)
	if err != nil {
		writeComputeBridgeError(c, err)
		return
	}
	if seen == nil {
		var ok bool
		if token, ok = computeHoldToken(c, hold.TokenId, hold.UserId); !ok {
			return
		}
		if _, ok = computeBillingAccess(c, hold.UserId); !ok {
			return
		}
	}
	extension, replayed, err := model.ExtendComputeHold(hold.HoldId, request.ExtendId, int(request.AdditionalQuota), token, common.GetTimestamp())
	if err != nil {
		writeComputeBridgeError(c, err)
		return
	}
	// The contract answers a replay with the hold as it stands now, which may
	// have grown since through later extends.
	if replayed {
		current, err := model.GetComputeHold(hold.HoldId)
		if err != nil {
			writeComputeBridgeError(c, err)
			return
		}
		computeBridgeOK(c, gin.H{
			"hold_id":      current.HoldId,
			"quota":        current.Quota,
			"remain_quota": computeEntityQuota(current.UserId),
		})
		return
	}
	computeBridgeOK(c, gin.H{
		"hold_id":      extension.HoldId,
		"quota":        extension.QuotaAfter,
		"remain_quota": extension.RemainQuota,
	})
}

func ComputeReleaseHold(c *gin.Context) {
	released, err := model.ReleaseComputeHold(c.Param("hold_id"), common.GetTimestamp())
	if err != nil {
		writeComputeBridgeError(c, err)
		return
	}
	computeBridgeOK(c, gin.H{
		"released_quota": released.ReleasedQuota,
		"remain_quota":   computeEntityQuota(released.UserId),
	})
}

var computeHoldSweeperOnce sync.Once

// StartComputeHoldSweeper returns expired holds' remainders on the master
// node. It runs whether or not the bridge secret is set: switching the bridge
// off must not strand quota in holds that were open at the time.
func StartComputeHoldSweeper() {
	if !common.IsMasterNode {
		return
	}
	computeHoldSweeperOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(computeHoldSweepInterval)
			defer ticker.Stop()
			for range ticker.C {
				if _, err := model.SweepExpiredComputeHolds(common.GetTimestamp(), computeHoldSweepBatch); err != nil {
					common.SysError("compute bridge: hold sweep failed: " + err.Error())
				}
			}
		}()
	})
}
