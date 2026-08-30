package ratio_setting

// UNIFYAPI-FORK: derives every billing ratio from the official list prices in
// unifyapi_catalog.go. See that file for why the baseline is prices and not
// ratios.
//
// The conversions, all relative to the input price so a ratio stays a pure
// multiplier on it:
//
//	model_ratio        = input_usd_per_1M / 2      (because ratio 1 == $0.002/1K == $2/1M)
//	completion_ratio   = output_usd     / input_usd
//	cache_ratio        = cache_read_usd / input_usd
//	create_cache_ratio = cache_write_usd / input_usd
//
// A vendor that publishes no cache price gets no entry, rather than a zero
// entry -- zero is a real ratio meaning "cached reads are free", which is not
// the same claim.

import (
	"fmt"
	"sort"
)

// usdPerMillionPerRatioUnit converts a USD-per-1M-tokens price into a billing
// ratio. Upstream's convention is ratio 1 == $0.002 per 1K tokens, i.e. $2 per
// 1M tokens, expressed as the USD constant (= 500 quota per dollar).
const usdPerMillionPerRatioUnit = 2.0

// catalogIndex is the catalog keyed by model name, built once at init.
var catalogIndex = func() map[string]CatalogEntry {
	index := make(map[string]CatalogEntry, len(unifyapiCatalog))
	for _, entry := range Catalog() {
		index[entry.Model] = entry
	}
	return index
}()

// ModelRatio is the input-token billing ratio for this entry.
func (e CatalogEntry) ModelRatio() float64 {
	return e.InputUSD / usdPerMillionPerRatioUnit
}

// CompletionRatio is the output-token multiplier over the input ratio. Falls
// back to 1 for a model with no input price, which would otherwise divide by
// zero; such an entry cannot be billed per token anyway.
func (e CatalogEntry) CompletionRatio() float64 {
	if e.InputUSD == 0 {
		return 1
	}
	return e.OutputUSD / e.InputUSD
}

// CacheReadRatio reports the cached-read multiplier, and whether the vendor
// publishes one at all.
func (e CatalogEntry) CacheReadRatio() (float64, bool) {
	if e.InputUSD == 0 || e.CacheReadUSD == 0 {
		return 0, false
	}
	return e.CacheReadUSD / e.InputUSD, true
}

// CacheWriteRatio reports the cache-write multiplier, and whether the vendor
// publishes one at all.
func (e CatalogEntry) CacheWriteRatio() (float64, bool) {
	if e.InputUSD == 0 || e.CacheWriteUSD == 0 {
		return 0, false
	}
	return e.CacheWriteUSD / e.InputUSD, true
}

// Catalog returns the pricing baseline -- the compiled entries first, in catalog
// order, then any admin-added extras.
//
// Compiled first is not cosmetic: it is the merge order. An extra can only add a
// model the catalog does not carry (ValidateExtraModels enforces it), so the two
// sets never overlap and nothing compiled can be shadowed.
func Catalog() []CatalogEntry {
	extras := ExtraModelEntries()
	out := make([]CatalogEntry, 0, len(unifyapiCatalog)+len(extras))
	out = append(out, unifyapiCatalog...)
	out = append(out, extras...)
	return out
}

// CompiledCatalog returns only the entries compiled into the binary, excluding
// admin-added extras. The drift checker uses it: models.dev has nothing to say
// about a price somebody typed into the console.
func CompiledCatalog() []CatalogEntry {
	out := make([]CatalogEntry, len(unifyapiCatalog))
	copy(out, unifyapiCatalog)
	return out
}

// CatalogEntryFor looks a model up in the baseline, compiled entries first.
func CatalogEntryFor(model string) (CatalogEntry, bool) {
	if entry, ok := catalogIndex[model]; ok {
		return entry, true
	}
	if extra, ok := extraModelMap.Get(model); ok {
		return extra.toCatalogEntry(model), true
	}
	return CatalogEntry{}, false
}

// CatalogModels lists every priced model, sorted, for seeds and reports.
func CatalogModels() []string {
	catalog := Catalog()
	names := make([]string, 0, len(catalog))
	for _, entry := range catalog {
		names = append(names, entry.Model)
	}
	sort.Strings(names)
	return names
}

// officialModelRatio is the model-ratio baseline at the vendors' list prices,
// with no customer discount applied. This is the number the drift check
// compares against models.dev, and the cost basis reconciliation starts from.
func officialModelRatio() map[string]float64 {
	out := make(map[string]float64, len(unifyapiCatalog))
	for _, entry := range Catalog() {
		out[entry.Model] = entry.ModelRatio()
	}
	return out
}

// baselineModelRatio is what should actually be billing: the official price
// with the per-model customer discount applied. Group ratio is NOT folded in --
// it is applied per request, because it depends on which group the caller is in.
//
// Everything that restores or verifies the live ratio map goes through this, so
// a discount cannot be lost by a reset and cannot be mistaken for drift.
func baselineModelRatio() map[string]float64 {
	out := make(map[string]float64, len(unifyapiCatalog))
	for _, entry := range Catalog() {
		out[entry.Model] = entry.ModelRatio() * GetModelDiscount(entry.Model)
	}
	return out
}

// baselineCompletionRatio is the derived completion-ratio baseline.
func baselineCompletionRatio() map[string]float64 {
	out := make(map[string]float64, len(unifyapiCatalog))
	for _, entry := range Catalog() {
		out[entry.Model] = entry.CompletionRatio()
	}
	return out
}

// baselineCacheRatio is the derived cached-read baseline, holding only models
// whose vendor publishes a cached-read price.
func baselineCacheRatio() map[string]float64 {
	out := make(map[string]float64)
	for _, entry := range Catalog() {
		if ratio, ok := entry.CacheReadRatio(); ok {
			out[entry.Model] = ratio
		}
	}
	return out
}

// baselineCreateCacheRatio is the derived cache-write baseline, holding only
// models whose vendor publishes a cache-write price.
func baselineCreateCacheRatio() map[string]float64 {
	out := make(map[string]float64)
	for _, entry := range Catalog() {
		if ratio, ok := entry.CacheWriteRatio(); ok {
			out[entry.Model] = ratio
		}
	}
	return out
}

// OfficialRatios is the baseline at the vendors' list prices, ignoring customer
// discounts. Used by the drift check and by reconciliation's cost side.
func OfficialRatios() map[string]map[string]float64 {
	return map[string]map[string]float64{
		"ModelRatio":       officialModelRatio(),
		"CompletionRatio":  baselineCompletionRatio(),
		"CacheRatio":       baselineCacheRatio(),
		"CreateCacheRatio": baselineCreateCacheRatio(),
	}
}

// baselineModelPrice is the per-call price baseline: models billed per request
// rather than per token. Empty today -- no catalogued model is per-call -- and
// deliberately derived rather than hardcoded, so adding one is a catalog edit
// like every other price.
func baselineModelPrice() map[string]float64 {
	out := make(map[string]float64)
	for _, entry := range Catalog() {
		if entry.PerCallUSD > 0 {
			out[entry.Model] = entry.PerCallUSD
		}
	}
	return out
}

// baselineImageRatio / baselineAudioRatio / baselineAudioCompletionRatio are the
// image- and audio-token modifiers. All empty today: none of the 54 catalogued
// models bills image or audio tokens at a rate different from text.
//
// They exist as functions rather than as an omission so that "every pricing map
// comes from the catalog" is literally true, and so the admin pricing page shows
// nothing UnifyAPI does not sell.
func baselineImageRatio() map[string]float64 {
	out := make(map[string]float64)
	for _, entry := range Catalog() {
		if entry.ImageRatio > 0 {
			out[entry.Model] = entry.ImageRatio
		}
	}
	return out
}

func baselineAudioRatio() map[string]float64 {
	out := make(map[string]float64)
	for _, entry := range Catalog() {
		if entry.AudioRatio > 0 {
			out[entry.Model] = entry.AudioRatio
		}
	}
	return out
}

func baselineAudioCompletionRatio() map[string]float64 {
	out := make(map[string]float64)
	for _, entry := range Catalog() {
		if entry.AudioCompletionRatio > 0 {
			out[entry.Model] = entry.AudioCompletionRatio
		}
	}
	return out
}

// BaselineRatios is every pricing map the catalog is authoritative for, keyed by
// the option name it is stored under, with per-model customer discounts applied.
// The reset endpoint, the seed generator and the shadow check all go through
// this, so a model can never end up in one map and miss another -- and no map
// can quietly keep a set of models the catalog does not list.
func BaselineRatios() map[string]map[string]float64 {
	return map[string]map[string]float64{
		"ModelRatio":           baselineModelRatio(),
		"CompletionRatio":      baselineCompletionRatio(),
		"CacheRatio":           baselineCacheRatio(),
		"CreateCacheRatio":     baselineCreateCacheRatio(),
		"ModelPrice":           baselineModelPrice(),
		"ImageRatio":           baselineImageRatio(),
		"AudioRatio":           baselineAudioRatio(),
		"AudioCompletionRatio": baselineAudioCompletionRatio(),
	}
}

// ValidateCatalog reports every structural problem in the baseline. It runs in
// tests and in gen-pricing-seed, so a malformed row cannot reach a seed file.
func ValidateCatalog() []error {
	var problems []error
	seen := make(map[string]bool, len(unifyapiCatalog))

	for _, entry := range Catalog() {
		switch {
		case entry.Model == "":
			problems = append(problems, fmt.Errorf("catalog has an entry with an empty model name"))
			continue
		case seen[entry.Model]:
			problems = append(problems, fmt.Errorf("%s: duplicate catalog entry", entry.Model))
			continue
		}
		seen[entry.Model] = true

		if entry.InputUSD <= 0 {
			problems = append(problems, fmt.Errorf("%s: input price must be positive, got %g", entry.Model, entry.InputUSD))
		}
		if entry.OutputUSD <= 0 {
			problems = append(problems, fmt.Errorf("%s: output price must be positive, got %g", entry.Model, entry.OutputUSD))
		}
		if entry.OutputUSD < entry.InputUSD {
			problems = append(problems, fmt.Errorf("%s: output price %g is below input price %g, which no vendor charges -- likely transposed",
				entry.Model, entry.OutputUSD, entry.InputUSD))
		}
		if entry.CacheReadUSD > entry.InputUSD {
			problems = append(problems, fmt.Errorf("%s: cached-read price %g exceeds the input price %g",
				entry.Model, entry.CacheReadUSD, entry.InputUSD))
		}
		if entry.CacheReadUSD < 0 || entry.CacheWriteUSD < 0 {
			problems = append(problems, fmt.Errorf("%s: cache prices must not be negative", entry.Model))
		}
		if entry.Unverified && entry.Vendor != "" {
			problems = append(problems, fmt.Errorf("%s: marked unverified but names vendor %q -- if it is listed, drop the flag", entry.Model, entry.Vendor))
		}
		if !entry.Unverified && entry.Vendor == "" {
			problems = append(problems, fmt.Errorf("%s: has no vendor and is not marked unverified, so nothing can check its price", entry.Model))
		}
		if entry.UpstreamModel != "" && entry.Vendor == "" {
			problems = append(problems, fmt.Errorf("%s: names upstream model %q without a vendor", entry.Model, entry.UpstreamModel))
		}
	}
	return problems
}

// BaselineShadow is one model whose live billing ratio disagrees with the
// catalog, i.e. a database options row is shadowing the code baseline.
type BaselineShadow struct {
	Option   string  `json:"option"`
	Model    string  `json:"model"`
	Baseline float64 `json:"baseline"`
	Live     float64 `json:"live"`
	Reason   string  `json:"reason"`
}

// liveRatioMaps pairs each catalog-owned option with the map actually billing.
func liveRatioMaps() map[string]map[string]float64 {
	return map[string]map[string]float64{
		"ModelRatio":       modelRatioMap.ReadAll(),
		"CompletionRatio":  completionRatioMap.ReadAll(),
		"CacheRatio":       cacheRatioMap.ReadAll(),
		"CreateCacheRatio": createCacheRatioMap.ReadAll(),
	}
}

// DetectBaselineShadow compares what is billing right now against the catalog.
//
// A non-empty result means an options row in the database has replaced the code
// baseline -- which the loader does wholesale, so this is the only way to notice
// from inside the process. Someone pressing save on the admin pricing page is
// enough to cause it. Logged at boot and served by GET /api/pricing/baseline so
// it cannot sit unnoticed the way the last drift did.
func DetectBaselineShadow() []BaselineShadow {
	var shadows []BaselineShadow

	for option, baseline := range BaselineRatios() {
		live := liveRatioMaps()[option]

		models := make([]string, 0, len(baseline))
		for name := range baseline {
			models = append(models, name)
		}
		sort.Strings(models)

		for _, name := range models {
			want := baseline[name]
			got, present := live[name]
			switch {
			case !present:
				shadows = append(shadows, BaselineShadow{
					Option: option, Model: name, Baseline: want,
					Reason: "catalogued model is missing from the live map; it will be refused at relay time",
				})
			case !nearlyEqualRatio(got, want):
				shadows = append(shadows, BaselineShadow{
					Option: option, Model: name, Baseline: want, Live: got,
					Reason: "live ratio differs from the official-price baseline",
				})
			}
		}

		extras := make([]string, 0)
		for name := range live {
			if _, ok := baseline[name]; !ok {
				extras = append(extras, name)
			}
		}
		sort.Strings(extras)
		for _, name := range extras {
			shadows = append(shadows, BaselineShadow{
				Option: option, Model: name, Live: live[name],
				Reason: "model is billing but is not in the catalog",
			})
		}
	}
	return shadows
}

// nearlyEqualRatio compares ratios with a tolerance, because a ratio derived
// from a price (25/5) and one that round-tripped through JSON are not
// bit-identical.
func nearlyEqualRatio(a, b float64) bool {
	const epsilon = 1e-9
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	if diff <= epsilon {
		return true
	}
	scale := a
	if scale < 0 {
		scale = -scale
	}
	if b > scale {
		scale = b
	} else if -b > scale {
		scale = -b
	}
	return diff <= epsilon*scale
}
