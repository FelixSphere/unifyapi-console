package model

import (
	"math/rand"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// PickFailoverChannel returns the channel an affinity key should move to after
// the channels in tried kept failing it, or nil when the group has no other
// enabled channel for the model.
//
// UNIFYAPI-FORK: GetRandomSatisfiedChannel cannot answer this. It picks by
// priority tier, so asking it again returns the channel that just failed
// whenever that channel is alone in the top tier, and asking it for "the next
// tier" skips a healthy sibling in the same one. This excludes by channel
// instead, then chooses as selection does: highest remaining priority, channels
// over their own rate limit dropped if that leaves any, weighted within the tier.
//
// It reads the in-memory channel cache only. Without that cache (no Redis and
// MEMORY_CACHE_ENABLED unset) it returns nil and the key stays where it is.
func PickFailoverChannel(group string, modelName string, requestPath string, tried []int) *Channel {
	if !common.MemoryCacheEnabled || group == "" || modelName == "" {
		return nil
	}
	excluded := make(map[int]struct{}, len(tried))
	for _, id := range tried {
		excluded[id] = struct{}{}
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	ids := filterChannelsByRequestPathAndModel(group2model2channels[group][modelName], requestPath, modelName)
	if len(ids) == 0 {
		normalized := ratio_setting.FormatMatchingModelName(modelName)
		ids = filterChannelsByRequestPathAndModel(group2model2channels[group][normalized], requestPath, modelName)
	}

	var tier []*Channel
	for _, id := range ids {
		if _, skip := excluded[id]; skip {
			continue
		}
		channel, ok := channelsIDM[id]
		if !ok || channel.Status != common.ChannelStatusEnabled {
			continue
		}
		switch {
		case len(tier) == 0 || channel.GetPriority() > tier[0].GetPriority():
			tier = []*Channel{channel}
		case channel.GetPriority() == tier[0].GetPriority():
			tier = append(tier, channel)
		}
	}
	if len(tier) == 0 {
		return nil
	}
	if available := dropRateLimitedChannels(tier); len(available) > 0 {
		tier = available
	}

	// Same weighting as GetRandomSatisfiedChannel: a tier whose weights are
	// all 0 is shared evenly.
	total := 0
	for _, channel := range tier {
		total += channel.GetWeight()
	}
	if total == 0 {
		return tier[rand.Intn(len(tier))]
	}
	roll := rand.Intn(total)
	for _, channel := range tier {
		roll -= channel.GetWeight()
		if roll < 0 {
			return channel
		}
	}
	return tier[len(tier)-1]
}
