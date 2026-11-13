package service

// UNIFYAPI-FORK: move an affinity key off a channel that keeps failing it.
//
// An affinity key pins one conversation to one channel so the upstream prompt
// cache keeps hitting. Upstream new-api releases the pin only when the channel
// is disabled or the entry expires, and it refreshes the entry after every
// request that ended below HTTP 400. On 2026-11-13 one conversation failed ten
// times in a row on its pinned channel over 21 minutes, while a healthy channel
// served the same model: the failures were streams that had already answered
// 200 (the caller hung up after ~165 s with no output, or the upstream reported
// an error inside the stream), so each one refreshed the pin it should have
// counted against.
//
// The fix is per key, not per channel. After FailoverAfterFailures consecutive
// failed requests on a key, the key is pointed at the best channel that the
// failing request did not use. Dropping the pin would not do it: selection would
// hand back the same top-priority channel. A request that was served resets the
// count, so a single bad response never costs a conversation its cache.

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/cachex"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/samber/hot"
)

type ChannelAffinityOutcome int

const (
	// ChannelAffinityOutcomeUnknown is every request this file has no opinion
	// on: rejected before an upstream was tried, refused for the caller's own
	// request, or abandoned too early to blame anyone. It keeps the upstream
	// behaviour exactly and leaves the failure count alone.
	ChannelAffinityOutcomeUnknown ChannelAffinityOutcome = iota
	ChannelAffinityOutcomeServed
	ChannelAffinityOutcomeChannelFailed
)

const (
	ginKeyChannelAffinityOutcome  = "unifyapi_channel_affinity_outcome"
	ginKeyChannelAffinityNoOutput = "unifyapi_channel_affinity_no_output"

	channelAffinityFailuresNamespace = "unifyapi:channel_affinity_failures:v1"
)

var (
	channelAffinityFailuresCache     *cachex.HybridCache[int]
	channelAffinityFailuresCacheOnce sync.Once

	// A variable so tests can stand in for the channel cache.
	pickFailoverChannel = model.PickFailoverChannel
)

func getChannelAffinityFailuresCache() *cachex.HybridCache[int] {
	channelAffinityFailuresCacheOnce.Do(func() {
		channelAffinityFailuresCache = cachex.NewHybridCache[int](cachex.HybridCacheConfig[int]{
			Namespace: cachex.Namespace(channelAffinityFailuresNamespace),
			Redis:     common.RDB,
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			RedisCodec: cachex.IntCodec{},
			Memory: func() *hot.HotCache[string, int] {
				return hot.NewHotCache[string, int](hot.LRU, 10_000).
					WithTTL(time.Hour).
					WithJanitor().
					Build()
			},
		})
	})
	return channelAffinityFailuresCache
}

func channelAffinityFailuresKey(cacheKey string) string {
	return strings.TrimPrefix(cacheKey, channelAffinityCacheNamespace+":")
}

func SetChannelAffinityOutcome(c *gin.Context, outcome ChannelAffinityOutcome) {
	if c == nil {
		return
	}
	c.Set(ginKeyChannelAffinityOutcome, outcome)
}

func channelAffinityOutcomeFrom(c *gin.Context) ChannelAffinityOutcome {
	if c == nil {
		return ChannelAffinityOutcomeUnknown
	}
	if v, ok := c.Get(ginKeyChannelAffinityOutcome); ok {
		if outcome, ok := v.(ChannelAffinityOutcome); ok {
			return outcome
		}
	}
	return ChannelAffinityOutcomeUnknown
}

// noteChannelAffinityNoOutput records that settlement found nothing to bill:
// the upstream returned neither usage nor any text to count.
func noteChannelAffinityNoOutput(c *gin.Context) {
	if c == nil {
		return
	}
	c.Set(ginKeyChannelAffinityNoOutput, true)
}

func ChannelAffinityRequestHadNoOutput(c *gin.Context) bool {
	return c != nil && c.GetBool(ginKeyChannelAffinityNoOutput)
}

// SettleChannelAffinity runs once the request is over. selectedChannelID is the
// channel the distributor chose and group the routing group it chose it from.
func SettleChannelAffinity(c *gin.Context, selectedChannelID int, group string) {
	if c == nil || c.Writer == nil || selectedChannelID <= 0 {
		return
	}
	belowError := c.Writer.Status() < 400

	threshold := 0
	if setting := operation_setting.GetChannelAffinitySetting(); setting != nil && setting.Enabled {
		threshold = setting.FailoverAfterFailures
	}
	outcome := channelAffinityOutcomeFrom(c)
	if threshold <= 0 || outcome == ChannelAffinityOutcomeUnknown {
		if belowError {
			RecordChannelAffinity(c, selectedChannelID)
		}
		return
	}

	cacheKey, ttlSeconds, ok := getChannelAffinityContext(c)
	if !ok || cacheKey == "" {
		return
	}
	failuresKey := channelAffinityFailuresKey(cacheKey)
	failures := getChannelAffinityFailuresCache()
	lock := channelAffinityUsageCacheStatsLock(failuresKey)
	lock.Lock()
	defer lock.Unlock()

	if outcome == ChannelAffinityOutcomeServed {
		if belowError {
			RecordChannelAffinity(c, selectedChannelID)
		}
		if _, err := failures.DeleteMany([]string{failuresKey}); err != nil {
			common.SysError(fmt.Sprintf("channel affinity failure count reset failed: key=%s, err=%v", cacheKey, err))
		}
		return
	}

	// A failed request does not refresh the pin: the entry's lifetime is
	// counted from the last time the channel actually served this key.
	if ttlSeconds <= 0 {
		ttlSeconds = operation_setting.GetChannelAffinitySetting().DefaultTTLSeconds
	}
	if ttlSeconds <= 0 {
		ttlSeconds = 3600
	}
	ttl := time.Duration(ttlSeconds) * time.Second
	count, _, err := failures.Get(failuresKey)
	if err != nil {
		common.SysError(fmt.Sprintf("channel affinity failure count read failed: key=%s, err=%v", cacheKey, err))
		return
	}
	count++
	if count < threshold {
		if err := failures.SetWithTTL(failuresKey, count, ttl); err != nil {
			common.SysError(fmt.Sprintf("channel affinity failure count write failed: key=%s, err=%v", cacheKey, err))
		}
		return
	}

	// A promotional credit pool holds inventory per channel, and the distributor
	// drops the grant when the pinned channel has none. Never move such a key.
	if common.GetContextKeyInt(c, constant.ContextKeyCreditPoolId) != 0 {
		return
	}
	meta, _ := getChannelAffinityMeta(c)
	tried := channelAffinityTriedChannels(c, selectedChannelID)
	backup := pickFailoverChannel(group, meta.ModelName, meta.RequestPath, tried)
	if backup == nil {
		// Nowhere else to go. Hold the count one short of the threshold so
		// the next failure looks again; a channel may have been enabled since.
		if err := failures.SetWithTTL(failuresKey, threshold-1, ttl); err != nil {
			common.SysError(fmt.Sprintf("channel affinity failure count write failed: key=%s, err=%v", cacheKey, err))
		}
		logger.LogWarn(c, fmt.Sprintf("channel affinity failover: rule=%q key=%s failed %d times in a row on channel(s) %v and group %q has no other channel for %s",
			meta.RuleName, meta.KeyHint, count, tried, group, meta.ModelName))
		return
	}
	if err := getChannelAffinityCache().SetWithTTL(cacheKey, backup.Id, ttl); err != nil {
		common.SysError(fmt.Sprintf("channel affinity failover write failed: key=%s, err=%v", cacheKey, err))
		return
	}
	if _, err := failures.DeleteMany([]string{failuresKey}); err != nil {
		common.SysError(fmt.Sprintf("channel affinity failure count reset failed: key=%s, err=%v", cacheKey, err))
	}
	logger.LogWarn(c, fmt.Sprintf("channel affinity failover: rule=%q key=%s moved from channel(s) %v to #%d after %d consecutive failures",
		meta.RuleName, meta.KeyHint, tried, backup.Id, count))
}

// channelAffinityTriedChannels lists every channel this request was sent to.
// With retries that is more than the one the distributor selected, and all of
// them just failed.
func channelAffinityTriedChannels(c *gin.Context, selectedChannelID int) []int {
	tried := []int{selectedChannelID}
	add := func(id int) {
		if id <= 0 {
			return
		}
		for _, seen := range tried {
			if seen == id {
				return
			}
		}
		tried = append(tried, id)
	}
	add(c.GetInt("channel_id"))
	for _, raw := range c.GetStringSlice("use_channel") {
		if id, err := strconv.Atoi(raw); err == nil {
			add(id)
		}
	}
	return tried
}
