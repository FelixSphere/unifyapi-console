package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func failoverChannel(id int, priority int64, weight uint) *Channel {
	return &Channel{Id: id, Status: common.ChannelStatusEnabled, Priority: &priority, Weight: &weight}
}

// withFailoverChannels installs channels as the enabled channels of group
// "default" for model "gpt-6-astra".
func withFailoverChannels(t *testing.T, channels ...*Channel) {
	t.Helper()
	channelSyncLock.Lock()
	prevIDM, prevGroups, prevCache := channelsIDM, group2model2channels, common.MemoryCacheEnabled
	channelsIDM = map[int]*Channel{}
	ids := make([]int, 0, len(channels))
	for _, ch := range channels {
		channelsIDM[ch.Id] = ch
		ids = append(ids, ch.Id)
	}
	group2model2channels = map[string]map[string][]int{"default": {"gpt-6-astra": ids}}
	common.MemoryCacheEnabled = true
	channelSyncLock.Unlock()
	t.Cleanup(func() {
		channelSyncLock.Lock()
		channelsIDM, group2model2channels, common.MemoryCacheEnabled = prevIDM, prevGroups, prevCache
		channelSyncLock.Unlock()
		channelSaturated.Store(map[int]struct{}{})
	})
}

func pickIDs(t *testing.T, tried []int, runs int) map[int]int {
	t.Helper()
	seen := map[int]int{}
	for i := 0; i < runs; i++ {
		ch := PickFailoverChannel("default", "gpt-6-astra", "/v1/responses", tried)
		if ch == nil {
			seen[0]++
			continue
		}
		seen[ch.Id]++
	}
	return seen
}

// The shape of 2026-11-13: the failing channel alone in the top tier, the only
// other channel one tier down. Selection by tier hands back 189 every time.
func TestFailoverLeavesTheTopTierWhenItsOnlyChannelFailed(t *testing.T) {
	withFailoverChannels(t, failoverChannel(189, 7, 0), failoverChannel(221, 6, 1))

	selected, err := GetRandomSatisfiedChannel("default", "gpt-6-astra", 0, "/v1/responses")
	require.NoError(t, err)
	require.Equal(t, 189, selected.Id, "ordinary selection keeps returning the failing channel")

	require.Equal(t, map[int]int{221: 50}, pickIDs(t, []int{189}, 50))
}

func TestFailoverPrefersASiblingInTheSameTier(t *testing.T) {
	withFailoverChannels(t, failoverChannel(189, 7, 0), failoverChannel(187, 7, 0), failoverChannel(221, 6, 1))
	require.Equal(t, map[int]int{187: 50}, pickIDs(t, []int{189}, 50))
}

func TestFailoverExcludesEveryTriedChannel(t *testing.T) {
	withFailoverChannels(t, failoverChannel(189, 7, 0), failoverChannel(187, 7, 0), failoverChannel(221, 6, 1))
	require.Equal(t, map[int]int{221: 50}, pickIDs(t, []int{189, 187}, 50))
}

func TestFailoverReturnsNilWhenNothingElseServesTheModel(t *testing.T) {
	withFailoverChannels(t, failoverChannel(189, 7, 0))
	require.Nil(t, PickFailoverChannel("default", "gpt-6-astra", "/v1/responses", []int{189}))
	require.Nil(t, PickFailoverChannel("default", "another-model", "/v1/responses", []int{189}))
	require.Nil(t, PickFailoverChannel("vip", "gpt-6-astra", "/v1/responses", []int{189}))
}

func TestFailoverNeverPicksADisabledChannel(t *testing.T) {
	disabled := failoverChannel(187, 7, 0)
	disabled.Status = common.ChannelStatusAutoDisabled
	withFailoverChannels(t, failoverChannel(189, 7, 0), disabled, failoverChannel(221, 6, 1))
	require.Equal(t, map[int]int{221: 50}, pickIDs(t, []int{189}, 50))
}

func TestFailoverSkipsARateLimitedChannelWhenAnotherIsFree(t *testing.T) {
	withFailoverChannels(t, failoverChannel(189, 7, 0), failoverChannel(187, 7, 0), failoverChannel(190, 7, 0))
	channelSaturated.Store(map[int]struct{}{187: {}})
	require.Equal(t, map[int]int{190: 50}, pickIDs(t, []int{189}, 50))

	// Every remaining candidate saturated: still better than the channel that
	// is failing, exactly as selection falls through.
	channelSaturated.Store(map[int]struct{}{187: {}, 190: {}})
	seen := pickIDs(t, []int{189}, 200)
	require.Zero(t, seen[0])
	require.Zero(t, seen[189])
	require.Equal(t, 200, seen[187]+seen[190])
}

func TestFailoverFollowsWeightsWithinTheTier(t *testing.T) {
	withFailoverChannels(t, failoverChannel(189, 7, 0), failoverChannel(187, 7, 0), failoverChannel(190, 7, 5))
	require.Equal(t, map[int]int{190: 100}, pickIDs(t, []int{189}, 100), "a weight-0 channel is not chosen next to a weighted one")

	withFailoverChannels(t, failoverChannel(189, 7, 0), failoverChannel(187, 7, 0), failoverChannel(190, 7, 0))
	seen := pickIDs(t, []int{189}, 400)
	require.Greater(t, seen[187], 100)
	require.Greater(t, seen[190], 100)
}

func TestFailoverNeedsTheChannelCache(t *testing.T) {
	withFailoverChannels(t, failoverChannel(189, 7, 0), failoverChannel(221, 6, 1))
	common.MemoryCacheEnabled = false
	require.Nil(t, PickFailoverChannel("default", "gpt-6-astra", "/v1/responses", []int{189}))
}
