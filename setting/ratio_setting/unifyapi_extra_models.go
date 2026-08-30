package ratio_setting

// UNIFYAPI-FORK: admin-added model prices, layered ON TOP of the compiled
// catalog instead of replacing it.
//
// The catalog lives in code because a price is a commercial commitment and
// deserves review, provenance and an automated drift check. But requiring a
// deploy to sell a model a vendor launched this morning is a real cost, and the
// old escape hatch was worse than the problem: editing the raw ModelRatio map
// wrote an options row, and types.LoadFromJsonString is replace-not-merge, so a
// single save discarded the entire code baseline. Production once held such a
// row with 2,877 keys.
//
// So this table MERGES. It is a separate option key holding prices for models
// the catalog does not carry; Catalog() returns compiled entries plus these, and
// everything downstream -- billing, the pricing page, reconciliation, settlement
// -- sees one uniform list. Removing an entry restores the previous state
// exactly, because nothing was overwritten to begin with.
//
// Two rules keep it from becoming the thing it replaced:
//
//	1. AN EXTRA MAY ONLY ADD. Naming a model the catalog already prices is
//	   rejected, with a pointer to ModelDiscount -- which can already express any
//	   price as a ratio of the official one, without a deploy. So there is
//	   exactly one source of truth per model, always.
//	2. EXTRAS ARE MARKED. AdminAdded flows through to the pricing page and the
//	   drift checker, because nobody is watching these for vendor price changes.
//	   A price with no provenance should never look like one that has it.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
)

// maxExtraPriceUSD is a sanity ceiling per 1M tokens. The most expensive
// catalogued model is $10/1M, so anything past this is a typo -- a misplaced
// decimal or a per-1K price pasted into a per-1M field -- not a real quote.
const maxExtraPriceUSD = 1000.0

// ExtraModel is one admin-added price, in USD per 1M tokens. Same units as a
// catalog row, deliberately: a ratio is not a price, and the whole reason the
// baseline is expressed in dollars is that "2.5" is unreadable without knowing
// the quota convention, which is how the 11.8x underprice got typed in.
type ExtraModel struct {
	InputUSD      float64 `json:"input_usd"`
	OutputUSD     float64 `json:"output_usd"`
	CacheReadUSD  float64 `json:"cache_read_usd,omitempty"`
	CacheWriteUSD float64 `json:"cache_write_usd,omitempty"`

	// Vendor groups this model in the reconciliation report. Free text, since
	// an uncatalogued model is by definition one models.dev does not list.
	Vendor string `json:"vendor,omitempty"`

	// Note is why this exists and where the price came from. Not enforced, but
	// the pricing page shows it, and "where did this number come from" is the
	// question every unverified price eventually has to answer.
	Note string `json:"note,omitempty"`
}

// extraModelMap holds model -> admin-supplied price.
var extraModelMap = types.NewRWMap[string, ExtraModel]()

// toCatalogEntry renders an extra as a catalog row. Unverified and AdminAdded
// are both set: no models.dev listing exists to check it against, and the
// pricing page has to be able to say so.
func (e ExtraModel) toCatalogEntry(model string) CatalogEntry {
	return CatalogEntry{
		Model:         model,
		Vendor:        e.Vendor,
		InputUSD:      e.InputUSD,
		OutputUSD:     e.OutputUSD,
		CacheReadUSD:  e.CacheReadUSD,
		CacheWriteUSD: e.CacheWriteUSD,
		Unverified:    true,
		AdminAdded:    true,
		QuoteSource:   e.Note,
	}
}

// ExtraModels returns the configured extras.
func ExtraModels() map[string]ExtraModel { return extraModelMap.ReadAll() }

// ExtraModelEntries returns the extras as catalog rows, sorted by model name so
// Catalog() has a stable order.
func ExtraModelEntries() []CatalogEntry {
	raw := extraModelMap.ReadAll()
	names := make([]string, 0, len(raw))
	for name := range raw {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]CatalogEntry, 0, len(names))
	for _, name := range names {
		out = append(out, raw[name].toCatalogEntry(name))
	}
	return out
}

func ExtraModels2JSONString() string { return extraModelMap.MarshalJSONString() }

// UpdateExtraModelsByJSONString replaces the extras table and rebuilds every
// derived ratio map from it.
//
// Rebuilding here rather than lazily at lookup time is what keeps GET
// /api/pricing honest: the number a customer is quoted comes out of the same map
// the relay bills from.
func UpdateExtraModelsByJSONString(jsonStr string) error {
	// Validate against the incoming payload before committing anything. An
	// extra with a zero price or a name the catalog already owns would
	// otherwise start mispricing the moment it was saved, with the option row
	// already written by the time anyone noticed.
	var incoming map[string]ExtraModel
	if err := common.Unmarshal([]byte(jsonStr), &incoming); err != nil {
		return err
	}
	if problems := ValidateExtraModels(incoming); len(problems) > 0 {
		messages := make([]string, 0, len(problems))
		for _, problem := range problems {
			messages = append(messages, problem.Error())
		}
		return fmt.Errorf("拒绝保存补充定价：%s", strings.Join(messages, "; "))
	}

	if err := types.LoadFromJsonString(extraModelMap, jsonStr); err != nil {
		return err
	}
	RebuildRatioMapsFromCatalog()
	InvalidateExposedDataCache()
	return nil
}

// ValidateExtraModels reports every extra that cannot be applied.
//
// All of them are reported, not just the first: an admin pasting a table wants
// to fix every row in one pass rather than discovering them one save at a time.
func ValidateExtraModels(extras map[string]ExtraModel) []error {
	names := make([]string, 0, len(extras))
	for name := range extras {
		names = append(names, name)
	}
	sort.Strings(names)

	var problems []error
	for _, name := range names {
		extra := extras[name]

		if strings.TrimSpace(name) == "" {
			problems = append(problems, fmt.Errorf("模型名不能为空"))
			continue
		}
		if name != strings.TrimSpace(name) {
			problems = append(problems, fmt.Errorf(
				"%q: 模型名首尾有空格，API 调用时不会匹配上", name))
			continue
		}
		// The compiled catalog wins, always. Allowing an override here would
		// recreate the shadowing this table exists to avoid -- and there is
		// already a supported way to change a catalogued price without a
		// deploy.
		if _, ok := catalogIndex[name]; ok {
			problems = append(problems, fmt.Errorf(
				"%s: 目录里已经有它的官方报价，补充定价不能覆盖。要改它的售价请用模型折扣（ModelDiscount）", name))
			continue
		}

		switch {
		case extra.InputUSD <= 0:
			problems = append(problems, fmt.Errorf(
				"%s: 输入价必须大于 0，收到 %g（单位是美元/百万 token）", name, extra.InputUSD))
		case extra.OutputUSD <= 0:
			problems = append(problems, fmt.Errorf(
				"%s: 输出价必须大于 0，收到 %g（单位是美元/百万 token）", name, extra.OutputUSD))
		case extra.InputUSD > maxExtraPriceUSD || extra.OutputUSD > maxExtraPriceUSD:
			problems = append(problems, fmt.Errorf(
				"%s: 价格超过 $%g/百万 token 的合理上限，通常是小数点错位或把每千 token 的价填进来了",
				name, maxExtraPriceUSD))
		}

		if extra.CacheReadUSD < 0 || extra.CacheWriteUSD < 0 {
			problems = append(problems, fmt.Errorf("%s: 缓存价不能为负", name))
			continue
		}
		// A cached read costing more than fresh input is backwards everywhere
		// it is published, and silently overstates cost in reconciliation.
		if extra.CacheReadUSD > extra.InputUSD {
			problems = append(problems, fmt.Errorf(
				"%s: 缓存读价 $%g 高于输入价 $%g，方向反了", name, extra.CacheReadUSD, extra.InputUSD))
		}
	}
	return problems
}
