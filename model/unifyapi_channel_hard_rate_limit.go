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

// UNIFYAPI-FORK: a channel's RPM/TPM can be a real cap.
//
// The channel limiter in channel_rate_limit.go is load balancing: a saturated
// channel is skipped only while another candidate at the same priority can
// take the request, and is used anyway when none can. That is right for a
// limit that mirrors an upstream's own quota -- the upstream sheds the excess.
// It is wrong for a limit the operator sets BELOW the upstream's quota on
// purpose, to keep the volume one provider key sends under what that provider
// would read as abuse. There, the request that does not fit must not be sent.
//
// A channel whose settings carry hard_rate_limit is therefore refused once it
// is over either window, by every path that can choose a channel: selection,
// affinity, a channel-pinned key and retry. Other channels still take the
// traffic when they can; when none can the caller gets 429 with Retry-After.
//
// WHAT IS EXACT AND WHAT IS NOT.
//
// RPM is exact. Admission increments the Redis window counter and compares
// the value that increment returned, so with a limit of N the N+1th request
// of a window is refused however many arrive at once and however many
// instances serve them. The increment of a refused request is not undone: a
// refusal only happens in a window that is already full, both counters roll
// over on the same minute boundary, and nothing reads the excess.
//
// TPM cannot be exact. A request's tokens are known when it finishes, so the
// window closes once the recorded total reaches the limit and every request
// admitted before that -- including ones still running -- lands on top. The
// overshoot is bounded by what was in flight, not by one request.
//
// WITHOUT REDIS the same counters are kept in this process. That is exact on
// one instance, which is how production runs, and per-instance otherwise. The
// alternative for a deployment without Redis, or during a Redis outage, was to
// refuse everything on a hard channel or to stop enforcing; the first turns a
// cache outage into a model outage and the second silently removes the cap
// the operator asked for. Falling back is logged, at most once a minute.

// ErrChannelHardRateLimited is returned by selection when the request could
// have been served, but only by channels that are hard-limited and full.
var ErrChannelHardRateLimited = errors.New("every channel for this model is at its hard rate limit")

// MaxHardLimitReselects bounds how many full hard channels one selection may
// step past before giving up with ErrChannelHardRateLimited.
const MaxHardLimitReselects = 16

// HardRateLimit reports whether this channel's RPM/TPM is a cap. Read here
// rather than through GetOtherSettings for the reason excludedGroups is:
// routing must never write the channel row.
func (channel *Channel) HardRateLimit() bool {
	if channel == nil || !channel.HasRateLimit() || !strings.Contains(channel.OtherSettings, "hard_rate_limit") {
		return false
	}
	settings := dto.ChannelOtherSettings{}
	if err := common.UnmarshalJsonStr(channel.OtherSettings, &settings); err != nil {
		return false
	}
	return settings.HardRateLimit
}

func channelLimitWindowIndex(now time.Time) int64 {
	return now.UTC().Unix() / int64(channelLimitWindow.Seconds())
}

// ChannelRateLimitRetryAfter is the number of seconds until the current
// window ends, which is when a full channel takes requests again.
func ChannelRateLimitRetryAfter() int {
	window := int64(channelLimitWindow.Seconds())
	return int(window - time.Now().UTC().Unix()%window)
}

// channelHardClosed maps a channel to the window it is full for. An entry for
// an earlier window is stale and ignored, so nothing has to expire it.
var (
	channelHardClosed   atomic.Value // map[int]int64
	channelHardClosedMu sync.Mutex
)

// ChannelIsHardRateLimited answers from process memory. No I/O, safe to call
// inside channelSyncLock.
func ChannelIsHardRateLimited(channelId int) bool {
	closed, _ := channelHardClosed.Load().(map[int]int64)
	window, ok := closed[channelId]
	return ok && window == channelLimitWindowIndex(time.Now())
}

func closeHardChannel(channelId int, window int64) {
	channelHardClosedMu.Lock()
	defer channelHardClosedMu.Unlock()
	closed, _ := channelHardClosed.Load().(map[int]int64)
	if closed[channelId] == window {
		return
	}
	next := make(map[int]int64, len(closed)+1)
	for id, w := range closed {
		if w >= window {
			next[id] = w
		}
	}
	next[channelId] = window
	channelHardClosed.Store(next)
}

// dropHardRateLimitedChannelIds removes channels that are closed for the
// current window. It never modifies its argument, which is the cache's slice.
func dropHardRateLimitedChannelIds(channelIds []int) []int {
	closed, _ := channelHardClosed.Load().(map[int]int64)
	if len(closed) == 0 {
		return channelIds
	}
	window := channelLimitWindowIndex(time.Now())
	var open []int
	for i, id := range channelIds {
		if w, ok := closed[id]; ok && w == window {
			if open == nil {
				open = append(make([]int, 0, len(channelIds)), channelIds[:i]...)
			}
			continue
		}
		if open != nil {
			open = append(open, id)
		}
	}
	if open == nil {
		return channelIds
	}
	return open
}

// hardLocal is the in-process copy of the hard channels' window counters, the
// fallback for a deployment without Redis and for a Redis call that fails.
var hardLocal struct {
	sync.Mutex
	window   int64
	requests map[int]int64
	tokens   map[int]int64
}

func hardLocalAdd(channelId int, window int64, requests int64, tokens int64) (int64, int64) {
	hardLocal.Lock()
	defer hardLocal.Unlock()
	if hardLocal.window != window || hardLocal.requests == nil {
		hardLocal.window = window
		hardLocal.requests = map[int]int64{}
		hardLocal.tokens = map[int]int64{}
	}
	hardLocal.requests[channelId] += requests
	hardLocal.tokens[channelId] += tokens
	return hardLocal.requests[channelId], hardLocal.tokens[channelId]
}

var hardLocalWarnedAt atomic.Int64

func warnHardLimitIsLocal(reason string) {
	now := time.Now().Unix()
	last := hardLocalWarnedAt.Load()
	if now-last < 60 || !hardLocalWarnedAt.CompareAndSwap(last, now) {
		return
	}
	common.SysError("channel hard rate limit is counting in this process only, so it is per instance: " + reason)
}

// AdmitChannelRequest decides whether a request may be sent through a channel
// and, when the channel is hard-limited, counts it. It returns true without
// doing anything for every other channel, which keeps their accounting where
// it was. Call it once per attempt, after the channel is chosen and outside
// channelSyncLock.
func AdmitChannelRequest(ctx context.Context, channel *Channel) bool {
	if !channel.HardRateLimit() {
		return true
	}
	rpmLimit, tpmLimit := int64(channel.GetRateLimitRPM()), int64(channel.GetRateLimitTPM())
	now := time.Now()
	window := channelLimitWindowIndex(now)
	requests, tokens := hardLocalAdd(channel.Id, window, 1, 0)

	if !common.RedisEnabled {
		warnHardLimitIsLocal("Redis is not enabled")
	} else {
		pipe := common.RDB.Pipeline()
		var incr *redis.IntCmd
		var used *redis.StringCmd
		if rpmLimit > 0 {
			key := channelWindowKey("rpm", channel.Id, now)
			incr = pipe.Incr(ctx, key)
			pipe.Expire(ctx, key, channelLimitKeyTTL)
		}
		if tpmLimit > 0 {
			used = pipe.Get(ctx, channelWindowKey("tpm", channel.Id, now))
		}
		// A missing token key is redis.Nil, an untouched window, not a failure.
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			warnHardLimitIsLocal(fmt.Sprintf("Redis failed for channel #%d: %v", channel.Id, err))
		} else {
			if incr != nil {
				requests = incr.Val()
			}
			if used != nil {
				tokens = counterValue(used)
			}
		}
	}

	over := (rpmLimit > 0 && requests > rpmLimit) || (tpmLimit > 0 && tokens >= tpmLimit)
	if over || (rpmLimit > 0 && requests == rpmLimit) {
		// Closing on the request that fills the window saves the next one a
		// round trip: selection skips the channel without asking Redis.
		closeHardChannel(channel.Id, window)
	}
	return !over
}

// recordHardChannelTokensLocal keeps the in-process token counter in step, so
// the fallback has something to compare against.
func recordHardChannelTokensLocal(channelId int, tokens int) {
	if tokens <= 0 {
		return
	}
	ch := getChannelForLimit(channelId)
	if ch.GetRateLimitTPM() <= 0 || !ch.HardRateLimit() {
		return
	}
	hardLocalAdd(channelId, channelLimitWindowIndex(time.Now()), 0, int64(tokens))
}
