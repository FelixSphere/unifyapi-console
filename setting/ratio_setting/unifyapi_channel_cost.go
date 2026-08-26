package ratio_setting

// UNIFYAPI-FORK: what our upstream charges us, per channel.
//
// This is the second of the three prices (see unifyapi_discount.go). It exists
// only to make reconciliation possible and MUST NOT reach customer billing:
// routing is load balanced, so the same request can go to a different channel
// on any given day. If a channel's cost fed into what we charge, an identical
// request would cost the customer a different amount depending on routing, and
// no customer could reconcile their own invoice.
//
// Expressed as a multiplier on the vendor's official list price rather than as
// absolute prices per model. A reseller contract is almost always "list minus
// N%", so one number per channel captures it, and it stays correct when a
// vendor changes its list price -- which is the whole reason the catalog tracks
// official prices in the first place.
//
// A channel with no entry is assumed to cost list price (multiplier 1), which
// is the conservative assumption: it can only understate our margin.

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/QuantumNous/new-api/types"
)

// maxChannelCostRatio bounds the multiplier. Paying more than 5x a vendor's
// list price is not a contract, it is a typo.
const maxChannelCostRatio = 5.0

// channelCostRatioMap holds channel id (as a decimal string, because option
// values are JSON objects) -> cost multiplier on the official list price.
var channelCostRatioMap = types.NewRWMap[string, float64]()

// GetChannelCostRatio returns the cost multiplier for a channel, defaulting to
// 1 (we pay list price).
func GetChannelCostRatio(channelID int) float64 {
	if ratio, ok := channelCostRatioMap.Get(strconv.Itoa(channelID)); ok && ratio > 0 {
		return ratio
	}
	return 1
}

// GetChannelCostRatioCopy returns the configured cost multipliers.
func GetChannelCostRatioCopy() map[string]float64 {
	return channelCostRatioMap.ReadAll()
}

func ChannelCostRatio2JSONString() string {
	return channelCostRatioMap.MarshalJSONString()
}

// UpdateChannelCostRatioByJSONString replaces the per-channel cost table.
func UpdateChannelCostRatioByJSONString(jsonStr string) error {
	return types.LoadFromJsonString(channelCostRatioMap, jsonStr)
}

// ValidateChannelCostRatios reports unusable cost multipliers.
func ValidateChannelCostRatios(ratios map[string]float64) []error {
	keys := make([]string, 0, len(ratios))
	for key := range ratios {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var problems []error
	for _, key := range keys {
		if _, err := strconv.Atoi(key); err != nil {
			problems = append(problems, fmt.Errorf(
				"%q: channel cost is keyed by numeric channel id, not by name", key))
			continue
		}
		ratio := ratios[key]
		switch {
		case ratio <= 0:
			problems = append(problems, fmt.Errorf(
				"channel %s: cost multiplier must be greater than 0, got %g -- a free upstream would make every "+
					"margin infinite and hide real spend", key, ratio))
		case ratio > maxChannelCostRatio:
			problems = append(problems, fmt.Errorf(
				"channel %s: cost multiplier %g exceeds the sanity bound of %g", key, ratio, maxChannelCostRatio))
		}
	}
	return problems
}

// UpstreamCostUSD is what a channel charges us for a request's tokens, in USD.
//
// The split matters: cached reads are an order of magnitude cheaper than fresh
// input at every vendor that offers them (Anthropic bills them at 0.1x), so
// folding them into promptTokens would overstate cost badly on cache-heavy
// traffic. cachedTokens is the subset of promptTokens that was served from
// cache; it is subtracted, not added.
//
// Returns false for a model with no official price, since a cost we cannot
// compute must be reported as unknown rather than silently counted as zero.
func UpstreamCostUSD(model string, channelID int, promptTokens, cachedTokens, completionTokens int64) (float64, bool) {
	entry, ok := CatalogEntryFor(model)
	if !ok {
		return 0, false
	}

	if cachedTokens > promptTokens {
		cachedTokens = promptTokens
	}
	if cachedTokens < 0 {
		cachedTokens = 0
	}
	freshTokens := promptTokens - cachedTokens

	const perMillion = 1_000_000.0
	cost := float64(freshTokens)/perMillion*entry.InputUSD +
		float64(completionTokens)/perMillion*entry.OutputUSD

	// A vendor with no published cached-read price charges full input price for
	// them, so they stay at InputUSD rather than becoming free.
	cachedPrice := entry.InputUSD
	if entry.CacheReadUSD != 0 {
		cachedPrice = entry.CacheReadUSD
	}
	cost += float64(cachedTokens) / perMillion * cachedPrice

	return cost * GetChannelCostRatio(channelID), true
}
