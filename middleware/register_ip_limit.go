/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package middleware

import (
	"context"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/gin-gonic/gin"
)

// UNIFYAPI-FORK: per-IP cap on account creation (common.RegisterIPDailyLimit).
// It counts accounts actually created, not attempts: a signup that fails
// validation, hits a taken username, or loses a database race gives its slot
// back. The existing CriticalRateLimit already bounds raw attempts.
//
// It is not a middleware because OAuth shares one callback between login and
// signup; only the signup branch may spend the budget, or a returning user
// behind a busy NAT could be locked out of logging in.

const (
	RegisterIPLimitMark          = "RG"
	RegisterIPLimitWindowSeconds = int64(24 * 60 * 60)
)

// Redis DECR on a key that expired since the reservation would recreate it at
// -1 with no TTL, which is a permanent extra slot. Only decrement a live key.
const redisReleaseScript = `
if redis.call('EXISTS', KEYS[1]) == 1 then
  return redis.call('DECR', KEYS[1])
end
return 0
`

var registerIPMemoryLimiter common.InMemoryRateLimiter

// Initialised up front: signup is reached from several handlers, and the
// limiter's lazy Init is not safe against a concurrent first use.
func init() {
	registerIPMemoryLimiter.Init(time.Duration(RegisterIPLimitWindowSeconds) * time.Second)
}

// RegistrationSlot is one account creation reserved against the client IP's
// daily budget. Callers Commit it once the account exists and defer Release,
// which gives the slot back unless it was committed.
type RegistrationSlot struct {
	redisKey  string
	memoryKey string
	done      bool
}

// ReserveRegistrationSlot takes one slot from the client IP's daily signup
// budget. ok is false when the budget is spent. A disabled limit, or a request
// without a client address, yields a slot that does nothing.
func ReserveRegistrationSlot(c *gin.Context) (slot *RegistrationSlot, ok bool) {
	limit := common.RegisterIPDailyLimit
	if limit <= 0 || c == nil || c.Request == nil {
		return &RegistrationSlot{done: true}, true
	}
	clientIP := c.ClientIP()
	if clientIP == "" {
		return &RegistrationSlot{done: true}, true
	}

	if common.RedisEnabled {
		key := redisIPRateLimitKey(RegisterIPLimitMark, clientIP)
		allowed, _, _, err := redisFixedWindowTake(c.Request.Context(), key, limit, RegisterIPLimitWindowSeconds)
		if err == nil {
			if !allowed {
				return nil, false
			}
			return &RegistrationSlot{redisKey: key}, true
		}
		logger.LogError(c.Request.Context(), fmt.Sprintf("register IP limit check failed, falling back to in-memory limiter: %v", err))
	}

	key := RegisterIPLimitMark + ":" + clientIP
	if !registerIPMemoryLimiter.Request(key, limit, RegisterIPLimitWindowSeconds) {
		return nil, false
	}
	return &RegistrationSlot{memoryKey: key}, true
}

// Commit keeps the slot spent: the account was created.
func (s *RegistrationSlot) Commit() {
	if s != nil {
		s.done = true
	}
}

// Release returns an uncommitted slot to the budget. Safe to call after
// Commit and more than once.
func (s *RegistrationSlot) Release() {
	if s == nil || s.done {
		return
	}
	s.done = true
	if s.memoryKey != "" {
		registerIPMemoryLimiter.Release(s.memoryKey)
		return
	}
	if s.redisKey == "" || common.RDB == nil {
		return
	}
	if err := common.RDB.Eval(context.Background(), redisReleaseScript, []string{s.redisKey}).Err(); err != nil {
		common.SysError(fmt.Sprintf("failed to release register IP limit slot: %v", err))
	}
}
