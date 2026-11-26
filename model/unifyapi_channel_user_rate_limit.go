/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/go-redis/redis/v8"
)

// UNIFYAPI-FORK: what one user may send through a channel.
//
// The channel limit (channel_rate_limit.go, and its hard mode) bounds the
// total a channel sends upstream. It says nothing about who sends it: one
// customer can take the whole allowance. On a channel that goes straight to a
// provider on our own key, one account pulling a model at volume is exactly
// what the provider reads as distillation, and it is our key that is banned.
//
// The existing per-user limiters cannot express this. ModelRequestRateLimit*
// and ModelRequestTokenLimit* count a user across every model and channel,
// and the per-group override is looked up by the key's token group first, so
// a key in group `auto` falls back to the global number.
//
// So the limit is a channel setting, user_rate_limit, counted per (user,
// channel) -- never per token group. Channels that are the same supply under
// two rows name the same user_limit_bucket and share one count per user. A
// channel without the setting is not touched, which is every reseller channel:
// what a reseller's upstream tolerates is the reseller's to manage.
//
// A user who is over is not routed to the channel; any other channel that can
// serve still does. Only when nothing else can is the request refused, with
// 429 and Retry-After.
//
// Requests are counted at admission, before anything is sent, and the count
// of a refused request is taken back: a client that keeps calling through a
// closed minute must not use up its day. Tokens are known when a request
// finishes, so a token limit refuses once the recorded total has reached it
// and whatever was in flight lands on top.
//
// The minute windows are clock minutes. The daily windows are UTC days.
//
// Counters are in Redis, or in this process when Redis is not enabled. A
// Redis call that fails admits the request and logs: this limit protects a
// provider relationship over days, and losing a minute of it is cheaper than
// refusing every customer of a guarded channel while a cache is down.

// ErrChannelUserRateLimited is returned by selection when the request could
// have been served, but only by channels this user has used up.
var ErrChannelUserRateLimited = errors.New("this user is at their limit on every channel for this model")

// ChannelUserLimitRefusal says which allowance is used up and when it is back.
type ChannelUserLimitRefusal struct {
	Limit      string // "requests per minute", "tokens per minute", "requests per day", "tokens per day"
	RetryAfter int    // seconds
	Daily      bool
}

const (
	channelUserLimitMinuteTTL = 3 * time.Minute
	channelUserLimitDayTTL    = 25 * time.Hour
)

// channelUserLimitNow is the clock, replaceable so a test can cross midnight.
var channelUserLimitNow = time.Now

// UserRateLimit is the channel's per-user limit and the name its counters are
// kept under. Read without GetOtherSettings for the reason excludedGroups is.
func (channel *Channel) UserRateLimit() (*dto.ChannelUserRateLimit, string) {
	if channel == nil || !strings.Contains(channel.OtherSettings, "user_rate_limit") {
		return nil, ""
	}
	settings := dto.ChannelOtherSettings{}
	if err := common.UnmarshalJsonStr(channel.OtherSettings, &settings); err != nil {
		return nil, ""
	}
	if !settings.UserRateLimit.Limited() || settings.UserRateLimit.Validate() != nil {
		return nil, ""
	}
	if bucket := strings.TrimSpace(settings.UserLimitBucket); bucket != "" && dto.ValidateUserLimitBucket(bucket) == nil {
		return settings.UserRateLimit, "b:" + bucket
	}
	return settings.UserRateLimit, fmt.Sprintf("c:%d", channel.Id)
}

type channelUserLimitKeys struct {
	requestsMinute, tokensMinute, requestsDay, tokensDay string
}

func channelUserLimitKeysFor(scope string, userId int, now time.Time) channelUserLimitKeys {
	now = now.UTC()
	minute := now.Unix() / 60
	day := now.Format("20060102")
	key := func(kind string, window any) string {
		return fmt.Sprintf("chUserLimit:%s:%s:%d:%v", kind, scope, userId, window)
	}
	return channelUserLimitKeys{
		requestsMinute: key("rpm", minute),
		tokensMinute:   key("tpm", minute),
		requestsDay:    key("rpd", day),
		tokensDay:      key("tpd", day),
	}
}

func secondsToNextMinute(now time.Time) int {
	return int(60 - now.UTC().Unix()%60)
}

func secondsToNextUTCDay(now time.Time) int {
	now = now.UTC()
	next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	seconds := int(next.Sub(now).Seconds())
	if seconds < 1 {
		seconds = 1
	}
	return seconds
}

// userLimitOp is one counter touched by an admission: delta 0 reads it.
type userLimitOp struct {
	key   string
	delta int64
	ttl   time.Duration
}

// userLimitLocal is the counter store of a deployment without Redis.
var userLimitLocal struct {
	sync.Mutex
	values map[string]userLimitLocalValue
}

type userLimitLocalValue struct {
	value   int64
	expires time.Time
}

func userLimitApplyLocal(ops []userLimitOp, now time.Time) []int64 {
	userLimitLocal.Lock()
	defer userLimitLocal.Unlock()
	if userLimitLocal.values == nil {
		userLimitLocal.values = map[string]userLimitLocalValue{}
	}
	if len(userLimitLocal.values) > 8192 {
		for key, entry := range userLimitLocal.values {
			if now.After(entry.expires) {
				delete(userLimitLocal.values, key)
			}
		}
	}
	out := make([]int64, len(ops))
	for i, op := range ops {
		entry := userLimitLocal.values[op.key]
		if op.delta != 0 {
			entry.value += op.delta
			entry.expires = now.Add(op.ttl)
			userLimitLocal.values[op.key] = entry
		}
		out[i] = entry.value
	}
	return out
}

var userLimitRedisWarnedAt atomic.Int64

// userLimitApply runs the ops and returns each counter's value afterwards.
// An error means Redis could not answer; the caller admits.
func userLimitApply(ctx context.Context, ops []userLimitOp, now time.Time) ([]int64, error) {
	if len(ops) == 0 {
		return nil, nil
	}
	if !common.RedisEnabled {
		return userLimitApplyLocal(ops, now), nil
	}
	pipe := common.RDB.Pipeline()
	incrs := make([]*redis.IntCmd, len(ops))
	gets := make([]*redis.StringCmd, len(ops))
	for i, op := range ops {
		if op.delta != 0 {
			incrs[i] = pipe.IncrBy(ctx, op.key, op.delta)
			pipe.Expire(ctx, op.key, op.ttl)
		} else {
			gets[i] = pipe.Get(ctx, op.key)
		}
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		last, at := userLimitRedisWarnedAt.Load(), time.Now().Unix()
		if at-last >= 60 && userLimitRedisWarnedAt.CompareAndSwap(last, at) {
			common.SysError(fmt.Sprintf("channel per-user rate limit is not being enforced, Redis failed: %v", err))
		}
		return nil, err
	}
	out := make([]int64, len(ops))
	for i := range ops {
		if incrs[i] != nil {
			out[i] = incrs[i].Val()
		} else {
			out[i] = counterValue(gets[i])
		}
	}
	return out, nil
}

func channelUserRequestOps(limit *dto.ChannelUserRateLimit, keys channelUserLimitKeys, delta int64) []userLimitOp {
	ops := make([]userLimitOp, 0, 2)
	if limit.RPM > 0 {
		ops = append(ops, userLimitOp{keys.requestsMinute, delta, channelUserLimitMinuteTTL})
	}
	if limit.DailyRequests > 0 {
		ops = append(ops, userLimitOp{keys.requestsDay, delta, channelUserLimitDayTTL})
	}
	return ops
}

// AdmitChannelUserRequest counts one request by this user against the
// channel's per-user limit and returns nil when it fits. A refusal leaves the
// counters as they were. A channel without a per-user limit always admits.
func AdmitChannelUserRequest(ctx context.Context, userId int, channel *Channel) *ChannelUserLimitRefusal {
	limit, scope := channel.UserRateLimit()
	if limit == nil || userId <= 0 {
		return nil
	}
	now := channelUserLimitNow()
	keys := channelUserLimitKeysFor(scope, userId, now)

	ops := channelUserRequestOps(limit, keys, 1)
	counted := len(ops)
	if limit.TPM > 0 {
		ops = append(ops, userLimitOp{key: keys.tokensMinute})
	}
	if limit.DailyTokens > 0 {
		ops = append(ops, userLimitOp{key: keys.tokensDay})
	}
	values, err := userLimitApply(ctx, ops, now)
	if err != nil {
		return nil
	}
	used := map[string]int64{}
	for i, op := range ops {
		used[op.key] = values[i]
	}

	var refusal *ChannelUserLimitRefusal
	switch {
	// The longest wait first: telling a caller to retry in 20 seconds when
	// their day is used up would only make them call again.
	case limit.DailyTokens > 0 && used[keys.tokensDay] >= limit.DailyTokens:
		refusal = &ChannelUserLimitRefusal{Limit: "tokens per day", RetryAfter: secondsToNextUTCDay(now), Daily: true}
	case limit.DailyRequests > 0 && used[keys.requestsDay] > limit.DailyRequests:
		refusal = &ChannelUserLimitRefusal{Limit: "requests per day", RetryAfter: secondsToNextUTCDay(now), Daily: true}
	case limit.TPM > 0 && used[keys.tokensMinute] >= limit.TPM:
		refusal = &ChannelUserLimitRefusal{Limit: "tokens per minute", RetryAfter: secondsToNextMinute(now)}
	case limit.RPM > 0 && used[keys.requestsMinute] > limit.RPM:
		refusal = &ChannelUserLimitRefusal{Limit: "requests per minute", RetryAfter: secondsToNextMinute(now)}
	}
	if refusal != nil && counted > 0 {
		_, _ = userLimitApply(ctx, channelUserRequestOps(limit, keys, -1), now)
	}
	return refusal
}

// ReleaseChannelUserRequest takes back a request AdmitChannelUserRequest
// counted, for a channel that then turned out not to be usable.
func ReleaseChannelUserRequest(ctx context.Context, userId int, channel *Channel) {
	limit, scope := channel.UserRateLimit()
	if limit == nil || userId <= 0 {
		return
	}
	now := channelUserLimitNow()
	_, _ = userLimitApply(ctx, channelUserRequestOps(limit, channelUserLimitKeysFor(scope, userId, now), -1), now)
}

// RecordChannelUserTokens adds a finished request's tokens to the user's
// allowance on the channel that served it.
func RecordChannelUserTokens(ctx context.Context, userId int, channelId int, tokens int) {
	if tokens <= 0 || userId <= 0 {
		return
	}
	limit, scope := getChannelForLimit(channelId).UserRateLimit()
	if limit == nil {
		return
	}
	now := channelUserLimitNow()
	keys := channelUserLimitKeysFor(scope, userId, now)
	ops := make([]userLimitOp, 0, 2)
	if limit.TPM > 0 {
		ops = append(ops, userLimitOp{keys.tokensMinute, int64(tokens), channelUserLimitMinuteTTL})
	}
	if limit.DailyTokens > 0 {
		ops = append(ops, userLimitOp{keys.tokensDay, int64(tokens), channelUserLimitDayTTL})
	}
	_, _ = userLimitApply(ctx, ops, now)
}

// dropChannelIds removes the given channels from a candidate list without
// modifying it.
func dropChannelIds(channelIds []int, excluded map[int]struct{}) []int {
	if len(excluded) == 0 {
		return channelIds
	}
	open := make([]int, 0, len(channelIds))
	for _, id := range channelIds {
		if _, skip := excluded[id]; !skip {
			open = append(open, id)
		}
	}
	return open
}
