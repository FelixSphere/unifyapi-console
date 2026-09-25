// UNIFYAPI-BRAND: ours. Channel-level rate limiting, so one customer's batch
// job cannot exhaust a shared upstream key and 429 everybody else.
//
// The per-customer limiter (middleware/model-rate-limit.go) caps the blast
// radius of one runaway key. It cannot make customers fair to each other,
// because the thing actually being contended is an UPSTREAM provider's quota,
// which several customers share through one channel. This limits that.
//
// WHY THE COUNTERS ARE IN REDIS BUT THE CHECK IS NOT.
//
// Channel selection runs inside channelSyncLock.RLock() over an in-memory cache
// and may weigh many candidates per request. A Redis round trip per candidate,
// inside that lock, would put network latency on the hot path of every relay.
//
// So Redis holds the authoritative counters -- correct across instances, which
// matters because this codebase supports NODE_TYPE=slave even though production
// runs one box today -- and selection consults a process-local snapshot of which
// channels are currently over their limit. The snapshot is refreshed at most
// once a second, outside the lock.
//
// The cost of that is bounded and deliberate: for up to one second a channel
// that has just crossed its limit still looks available. Over-admitting for a
// second is a far better failure than adding a network hop to every request.
package model

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
)

const (
	// One window, not a sliding log: a provider's published RPM/TPM is itself
	// stated per minute, so matching that shape keeps the configured number
	// meaning what an operator thinks it means.
	channelLimitWindow = time.Minute

	// How stale the selection snapshot may be. See the note above.
	channelLimitSnapshotTTL = time.Second

	// Counters outlive their window so a refresh that lands just after a
	// rollover still sees the window it is reporting on.
	channelLimitKeyTTL = 3 * time.Minute
)

// RateLimitRPM and RateLimitTPM are nil when the channel is unlimited, which is
// also the default. They are pointers for the same reason Weight, Priority and
// AutoBan are: Channel.Update() calls GORM Updates() with a struct, which skips
// zero-value fields. A plain int could never be set back to 0 through the admin
// UI, and a nil pointer is correctly left alone when the client does not send it.

func (channel *Channel) GetRateLimitRPM() int {
	if channel == nil || channel.RateLimitRPM == nil {
		return 0
	}
	return *channel.RateLimitRPM
}

func (channel *Channel) GetRateLimitTPM() int {
	if channel == nil || channel.RateLimitTPM == nil {
		return 0
	}
	return *channel.RateLimitTPM
}

// HasRateLimit reports whether this channel is limited at all. Zero means
// unlimited, matching the per-customer limiter's convention.
func (channel *Channel) HasRateLimit() bool {
	return channel.GetRateLimitRPM() > 0 || channel.GetRateLimitTPM() > 0
}

func channelWindowKey(kind string, channelId int, now time.Time) string {
	return fmt.Sprintf("chLimit:%s:%d:%d", kind, channelId, now.UTC().Unix()/int64(channelLimitWindow.Seconds()))
}

// saturated is the snapshot selection reads. Stored whole so readers never hold
// a lock and never see a half-built map.
var (
	channelSaturated   atomic.Value // map[int]struct{}
	channelRefreshedAt atomic.Int64 // unix nanos
	channelRefreshMu   sync.Mutex
)

// ChannelIsRateLimited answers from the snapshot. No I/O, safe to call inside
// channelSyncLock.
func ChannelIsRateLimited(channelId int) bool {
	snapshot, _ := channelSaturated.Load().(map[int]struct{})
	if snapshot == nil {
		return false
	}
	_, over := snapshot[channelId]
	return over
}

// RefreshChannelRateLimits rebuilds the snapshot, at most once per TTL. Cheap to
// call often; it returns immediately when a refresh is not due.
func RefreshChannelRateLimits(ctx context.Context) {
	if !common.RedisEnabled {
		return
	}
	last := channelRefreshedAt.Load()
	if time.Since(time.Unix(0, last)) < channelLimitSnapshotTTL {
		return
	}
	if !channelRefreshMu.TryLock() {
		return // another goroutine is already refreshing
	}
	defer channelRefreshMu.Unlock()

	// Re-check under the mutex: several goroutines can pass the TTL test.
	if time.Since(time.Unix(0, channelRefreshedAt.Load())) < channelLimitSnapshotTTL {
		return
	}

	limited := limitedChannels()
	if len(limited) == 0 {
		channelSaturated.Store(map[int]struct{}{})
		channelRefreshedAt.Store(time.Now().UnixNano())
		return
	}

	now := time.Now()
	pipe := common.RDB.Pipeline()
	type probe struct {
		channel        *Channel
		rpmCmd, tpmCmd *redis.StringCmd
	}
	probes := make([]probe, 0, len(limited))
	for _, ch := range limited {
		p := probe{channel: ch}
		if ch.GetRateLimitRPM() > 0 {
			p.rpmCmd = pipe.Get(ctx, channelWindowKey("rpm", ch.Id, now))
		}
		if ch.GetRateLimitTPM() > 0 {
			p.tpmCmd = pipe.Get(ctx, channelWindowKey("tpm", ch.Id, now))
		}
		probes = append(probes, p)
	}
	// Errors here are per-key (redis.Nil for an untouched window), so the
	// pipeline's aggregate error is deliberately ignored.
	_, _ = pipe.Exec(ctx)

	next := make(map[int]struct{})
	for _, p := range probes {
		if p.rpmCmd != nil && counterValue(p.rpmCmd) >= int64(p.channel.GetRateLimitRPM()) {
			next[p.channel.Id] = struct{}{}
			continue
		}
		if p.tpmCmd != nil && counterValue(p.tpmCmd) >= int64(p.channel.GetRateLimitTPM()) {
			next[p.channel.Id] = struct{}{}
		}
	}
	channelSaturated.Store(next)
	channelRefreshedAt.Store(time.Now().UnixNano())
}

// RecordChannelRequest counts one request against a channel's RPM window.
// Called after a channel is chosen, outside the selection lock.
func RecordChannelRequest(ctx context.Context, channelId int) {
	if !common.RedisEnabled {
		return
	}
	ch := getChannelForLimit(channelId)
	if ch == nil || ch.GetRateLimitRPM() <= 0 {
		return
	}
	key := channelWindowKey("rpm", channelId, time.Now())
	pipe := common.RDB.Pipeline()
	pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, channelLimitKeyTTL)
	_, _ = pipe.Exec(ctx)
}

// RecordChannelTokens adds a completed request's tokens to a channel's TPM
// window. TPM can only be accounted after the fact -- the completion length is
// not knowable before the upstream answers -- so a channel can overshoot its
// token budget by at most one request. That is inherent, not an oversight.
func RecordChannelTokens(ctx context.Context, channelId int, tokens int) {
	if !common.RedisEnabled || tokens <= 0 {
		return
	}
	ch := getChannelForLimit(channelId)
	if ch == nil || ch.GetRateLimitTPM() <= 0 {
		return
	}
	key := channelWindowKey("tpm", channelId, time.Now())
	pipe := common.RDB.Pipeline()
	pipe.IncrBy(ctx, key, int64(tokens))
	pipe.Expire(ctx, key, channelLimitKeyTTL)
	_, _ = pipe.Exec(ctx)
}

// counterValue treats a missing key as zero: an untouched window is not a
// failure, it is a channel nobody has used this minute.
func counterValue(cmd *redis.StringCmd) int64 {
	v, err := cmd.Int64()
	if err != nil {
		return 0
	}
	return v
}

// limitedChannels returns only the channels that actually carry a limit, so a
// deployment with none pays nothing for this beyond one map store per second.
func limitedChannels() []*Channel {
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	out := make([]*Channel, 0)
	for _, ch := range channelsIDM {
		if ch != nil && ch.HasRateLimit() {
			out = append(out, ch)
		}
	}
	return out
}

func getChannelForLimit(channelId int) *Channel {
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()
	return channelsIDM[channelId]
}

// dropRateLimitedChannels removes saturated candidates. Returns a slice that
// may be empty; the caller decides what an all-saturated priority means.
func dropRateLimitedChannels(candidates []*Channel) []*Channel {
	snapshot, _ := channelSaturated.Load().(map[int]struct{})
	if len(snapshot) == 0 {
		return candidates
	}
	out := make([]*Channel, 0, len(candidates))
	for _, ch := range candidates {
		if _, over := snapshot[ch.Id]; over {
			continue
		}
		out = append(out, ch)
	}
	return out
}
