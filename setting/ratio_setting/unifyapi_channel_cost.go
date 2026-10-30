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

// UsageSemanticAnthropic marks a row whose PromptTokens counts fresh input
// only, with cached reads and cache writes reported separately. Anything else
// (OpenAI and the formats modelled on it) reports a prompt total that already
// contains the cached reads.
const UsageSemanticAnthropic = "anthropic"

// TokenUsage is the token counts of one request, or of an aggregate of them,
// together with the one fact needed to price them correctly.
//
// A struct rather than five int64 parameters on purpose: the old positional
// form let a caller pass cache-write tokens where completion tokens go, and
// nothing would have caught it.
type TokenUsage struct {
	PromptTokens     int64
	CachedTokens     int64
	CacheWriteTokens int64
	CompletionTokens int64

	// Semantic is the row's logs.usage_semantic. Empty means unknown, which
	// happens on rows written before the column existed.
	Semantic string
}

// promptIsFreshOnly reports whether PromptTokens excludes the cache buckets.
//
// Unknown rows are treated as prompt-includes-cached, which is what the cost
// model did for every row before semantics were recorded. That keeps a
// historical report stable rather than silently restating it; run the backfill
// (scripts/backfill-usage-semantic) to classify those rows properly.
func (u TokenUsage) promptIsFreshOnly() bool {
	return u.Semantic == UsageSemanticAnthropic
}

// ListPriceUSD is what a request's tokens cost at the vendor's official list
// price, before any purchasing discount. It is the denomination a vendor's own
// prepaid credit balance decrements in, which is why the credit supply draws
// lots down by this figure rather than by UpstreamCostUSD (see
// model/credit_lot.go).
//
// Three prices, not two. Cached reads are an order of magnitude cheaper than
// fresh input wherever they are offered (Anthropic bills them at 0.1x) and
// cache WRITES are dearer than it (Anthropic 1.25x, and we bill the customer
// that premium), so a two-way split understates cost on cache-heavy traffic in
// the one direction that flatters a margin report.
//
// Whether the buckets are inside PromptTokens depends on the upstream, which is
// why TokenUsage carries the semantic. Subtracting cached reads from an
// Anthropic prompt count -- which never contained them -- is how a 12.8M-token
// cache read once priced as 838 tokens in production.
//
// Returns false for a model with no official price, since a cost we cannot
// compute must be reported as unknown rather than silently counted as zero.
func ListPriceUSD(model string, usage TokenUsage) (float64, bool) {
	entry, ok := CatalogEntryFor(model)
	if !ok {
		return 0, false
	}

	cached := usage.CachedTokens
	if cached < 0 {
		cached = 0
	}
	write := usage.CacheWriteTokens
	if write < 0 {
		write = 0
	}

	fresh := usage.PromptTokens
	if !usage.promptIsFreshOnly() {
		// The prompt count is a total: take the buckets back out of it so each
		// is charged at its own price exactly once.
		if cached > fresh {
			cached = fresh
		}
		fresh -= cached
		if write > fresh {
			write = fresh
		}
		fresh -= write
	}
	if fresh < 0 {
		fresh = 0
	}

	const perMillion = 1_000_000.0
	cost := float64(fresh)/perMillion*entry.InputUSD +
		float64(usage.CompletionTokens)/perMillion*entry.OutputUSD

	// A vendor with no published cached-read price charges full input price for
	// them, so they stay at InputUSD rather than becoming free. Same for
	// writes: no published premium means the vendor charges plain input.
	cachedPrice := entry.InputUSD
	if entry.CacheReadUSD != 0 {
		cachedPrice = entry.CacheReadUSD
	}
	writePrice := entry.InputUSD
	if entry.CacheWriteUSD != 0 {
		writePrice = entry.CacheWriteUSD
	}
	cost += float64(cached)/perMillion*cachedPrice + float64(write)/perMillion*writePrice
	return cost, true
}

// UpstreamCostUSD is what a channel charges us for a request's tokens, in USD:
// the list price scaled by that channel's purchasing ratio.
func UpstreamCostUSD(model string, channelID int, usage TokenUsage) (float64, bool) {
	cost, ok := ListPriceUSD(model, usage)
	if !ok {
		return 0, false
	}
	return cost * GetChannelCostRatio(channelID), true
}
