/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package controller

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
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
)

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
		return nil, errors.New("expired assertion")
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
		body, err := verifyComputeBridgeRequest(c, secret, time.Now().Unix())
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "message": "invalid_signature"})
			return
		}
		c.Set(computeBridgeBodyKey, body)
		c.Next()
	}
}

func computeBridgeOK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": data})
}

func computeBridgeFail(c *gin.Context, status int, message string, data any) {
	body := gin.H{"success": false, "message": message}
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
		computeBridgeFail(c, http.StatusPaymentRequired, "insufficient_quota", nil)
	case errors.Is(err, model.ErrComputeHoldNotFound):
		computeBridgeFail(c, http.StatusNotFound, err.Error(), nil)
	case errors.Is(err, model.ErrComputeQuotaOutOfRange):
		computeBridgeFail(c, http.StatusBadRequest, err.Error(), nil)
	case errors.Is(err, model.ErrComputeHoldExpired),
		errors.Is(err, model.ErrComputeHoldClosed),
		errors.Is(err, model.ErrComputeHoldExceeded),
		errors.Is(err, model.ErrComputeHoldConflict),
		errors.Is(err, model.ErrComputeEventConflict),
		errors.Is(err, model.ErrComputeSettlementRegressed):
		computeBridgeFail(c, http.StatusConflict, err.Error(), nil)
	default:
		common.SysError("compute bridge: " + err.Error())
		computeBridgeFail(c, http.StatusInternalServerError, common.TranslateMessage(c, i18n.MsgDatabaseError), nil)
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
			computeBridgeFail(c, http.StatusForbidden, common.TranslateMessage(c, i18n.MsgAuthUserBanned), gin.H{"status": "suspended"})
			return nil, false
		}
	}
	if userCache.Status != common.UserStatusEnabled {
		computeBridgeFail(c, http.StatusForbidden, common.TranslateMessage(c, i18n.MsgAuthUserBanned), gin.H{"status": "disabled"})
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
		computeBridgeFail(c, http.StatusUnauthorized, common.TranslateMessage(c, i18n.MsgTokenInvalid), nil)
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
		computeBridgeFail(c, http.StatusUnauthorized, common.TranslateMessage(c, i18n.MsgTokenInvalid), nil)
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
		computeBridgeFail(c, http.StatusBadRequest, "sku_mismatch", nil)
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
		AdditionalQuota int64 `json:"additional_quota"`
	}
	if !readComputeBridgeBody(c, &request) {
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
	token, ok := computeHoldToken(c, hold.TokenId, hold.UserId)
	if !ok {
		return
	}
	if _, ok := computeBillingAccess(c, hold.UserId); !ok {
		return
	}
	extended, err := model.ExtendComputeHold(hold.HoldId, int(request.AdditionalQuota), token, common.GetTimestamp())
	if err != nil {
		writeComputeBridgeError(c, err)
		return
	}
	computeBridgeOK(c, gin.H{
		"hold_id":      extended.HoldId,
		"quota":        extended.Quota,
		"remain_quota": computeEntityQuota(extended.UserId),
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
