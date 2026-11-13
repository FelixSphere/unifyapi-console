package service

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

// Production keeps affinity in Redis, so the pin and the failure count are
// shared by every instance and survive a restart. The other tests in this
// package run on the in-memory fallback; this one runs the same sequence
// against Redis and looks at what is actually stored there.
func TestAffinityFailoverOnRedis(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	prevRDB, prevEnabled := common.RDB, common.RedisEnabled
	common.RDB, common.RedisEnabled = client, true
	resetAffinityCaches := func() {
		channelAffinityCache, channelAffinityCacheOnce = nil, sync.Once{}
		channelAffinityFailuresCache, channelAffinityFailuresCacheOnce = nil, sync.Once{}
	}
	resetAffinityCaches()
	t.Cleanup(func() {
		_ = client.Close()
		common.RDB, common.RedisEnabled = prevRDB, prevEnabled
		resetAffinityCaches()
	})

	rig := newAffinityFailoverRig(t, 2)
	pinKey := rig.cacheKey
	countKey := channelAffinityFailuresNamespace + ":" + channelAffinityFailuresKey(rig.cacheKey)
	require.True(t, strings.HasPrefix(pinKey, channelAffinityCacheNamespace+":"))

	rig.request(served)
	require.Equal(t, 189, rig.pin())
	require.Equal(t, []string{pinKey}, server.Keys())
	require.Equal(t, 600*time.Second, server.TTL(pinKey))

	rig.request(failedInside)
	require.Equal(t, 189, rig.pin())
	require.Equal(t, 1, rig.failures())
	require.ElementsMatch(t, []string{pinKey, countKey}, server.Keys())
	require.Equal(t, 600*time.Second, server.TTL(countKey), "the count expires with the pin it belongs to")

	server.FastForward(5 * time.Minute)
	rig.request(failedWith500)
	require.Equal(t, 221, rig.pin())
	require.Equal(t, []string{pinKey}, server.Keys(), "the count is gone once the key has moved")
	require.Equal(t, 600*time.Second, server.TTL(pinKey), "the new pin gets a full lifetime")
	require.Equal(t, [][]int{{189}}, rig.picks)
}

// The operator set this to two on 2026-11-13. Changing it is their decision.
func TestAffinityFailoverDefaultThreshold(t *testing.T) {
	require.Equal(t, 2, operation_setting.GetChannelAffinitySetting().FailoverAfterFailures)
}
