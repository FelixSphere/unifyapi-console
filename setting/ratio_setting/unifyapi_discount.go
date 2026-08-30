package ratio_setting

// UNIFYAPI-FORK: the per-model customer discount.
//
// UnifyAPI deals with three different prices for the same model, and conflating
// any two of them is how the pricing table rotted the first time:
//
//	1. the vendor's official list price   -- unifyapi_catalog.go, code, one per model
//	2. what our upstream charges us       -- ChannelCostRatio, per channel, cost only
//	3. what we charge the customer        -- this file x group ratio
//
// Before this, (3) was implemented by editing (1): someone typed a discounted
// ratio straight into the ModelRatio map. That destroyed the only thing that
// made the baseline checkable -- once claude-opus-4-8 read 0.2125 there was no
// way to tell a deliberate 91.5% discount from a slipped decimal point, and it
// turned out to be the latter.
//
// So the discount is its own table. The catalog keeps the official price, this
// map keeps the multiplier, and the two are combined into the ratio that bills.
//
// Combination happens in one place: applyModelDiscounts rebuilds modelRatioMap
// as official x discount. Nothing downstream is discount-aware -- the relay, the
// quota maths, the pricing page and every billing path (text, audio, image,
// task, tiered) read modelRatioMap the way they always did, so there is no path
// that can forget to apply the discount. Completion, cache-read and cache-write
// ratios are multipliers ON the model ratio, so they scale with it for free: a
// 20% discount takes 20% off input, output and cache alike, which is what a
// per-model discount should mean.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
)

// maxModelDiscount bounds the multiplier. Values above 1 are a markup over the
// vendor's list price rather than a discount -- legitimate for a model we resell
// at a premium, but rare enough that a fat-fingered 20 should not be accepted
// silently. ValidateModelDiscounts reports every value above 1 so a markup is
// always a visible decision.
const maxModelDiscount = 10.0

// modelDiscountMap holds model -> customer discount multiplier. A model absent
// from the map is sold at the official list price (multiplier 1).
var modelDiscountMap = types.NewRWMap[string, float64]()

// GetModelDiscount returns the customer discount multiplier for a model,
// defaulting to 1 (sell at the vendor's official price).
func GetModelDiscount(model string) float64 {
	model = FormatMatchingModelName(model)
	if discount, ok := modelDiscountMap.Get(model); ok && discount > 0 {
		return discount
	}
	return 1
}

// GetModelDiscountCopy returns the configured discounts. Absent models are sold
// at list and are deliberately not materialised here, so the map reads as "the
// deviations", which is what a reviewer wants to see.
func GetModelDiscountCopy() map[string]float64 {
	return modelDiscountMap.ReadAll()
}

func ModelDiscount2JSONString() string {
	return modelDiscountMap.MarshalJSONString()
}

// UpdateModelDiscountByJSONString replaces the discount table and immediately
// rebuilds the billing ratios from it. Rebuilding here rather than lazily at
// lookup time is what keeps GET /api/pricing honest: the number a customer is
// quoted is read out of the same map the relay bills from.
func UpdateModelDiscountByJSONString(jsonStr string) error {
	// Validate against a scratch copy first. A discount that names an
	// uncatalogued model or reads 0 would otherwise be accepted and start
	// mispricing immediately, and the option row would already be committed by
	// the time anyone noticed.
	var incoming map[string]float64
	if err := common.Unmarshal([]byte(jsonStr), &incoming); err != nil {
		return err
	}
	problems, markups := ValidateModelDiscounts(incoming)
	if len(problems) > 0 {
		messages := make([]string, 0, len(problems))
		for _, problem := range problems {
			messages = append(messages, problem.Error())
		}
		return fmt.Errorf("拒绝保存模型折扣：%s", strings.Join(messages, "; "))
	}
	for _, markup := range markups {
		common.SysLog("PRICING: " + markup)
	}

	if err := types.LoadFromJsonString(modelDiscountMap, jsonStr); err != nil {
		return err
	}
	RebuildRatioMapsFromCatalog()
	InvalidateExposedDataCache()
	return nil
}

// applyModelDiscounts rebuilds modelRatioMap as official price x discount.
//
// It rebuilds the whole map rather than mutating entries, so removing a discount
// restores the official price instead of leaving the last discounted value
// behind -- the failure mode that makes a discount table impossible to reason
// about after a few edits.
func applyModelDiscounts() {
	catalog := Catalog()
	effective := make(map[string]float64, len(catalog))
	for _, entry := range catalog {
		effective[entry.Model] = entry.ModelRatio() * GetModelDiscount(entry.Model)
	}
	modelRatioMap.Clear()
	modelRatioMap.AddAll(effective)
}

// ValidateModelDiscounts reports discounts that cannot be applied, and flags
// markups so they are never invisible. Returned errors are shown to the admin
// on save; markups come back as notices, not errors.
func ValidateModelDiscounts(discounts map[string]float64) (problems []error, markups []string) {
	models := make([]string, 0, len(discounts))
	for model := range discounts {
		models = append(models, model)
	}
	sort.Strings(models)

	for _, model := range models {
		discount := discounts[model]
		if _, ok := CatalogEntryFor(model); !ok {
			problems = append(problems, fmt.Errorf(
				"%s: not in the pricing catalog, so it has no official price to discount from", model))
			continue
		}
		switch {
		case discount <= 0:
			problems = append(problems, fmt.Errorf(
				"%s: discount must be greater than 0, got %g -- a free model is configured by discounting to a "+
					"very small number or by removing the model, not by zeroing its price", model, discount))
		case discount > maxModelDiscount:
			problems = append(problems, fmt.Errorf(
				"%s: discount %g exceeds the sanity bound of %g", model, discount, maxModelDiscount))
		case discount > 1:
			markups = append(markups, fmt.Sprintf(
				"%s is sold at %.4gx the vendor's list price, i.e. a markup, not a discount", model, discount))
		}
	}
	return problems, markups
}

// CustomerPrice is what a customer in a given group actually pays for a model,
// alongside the pieces the number is built from. It exists so the pricing page,
// an invoice and the reconciliation report can all quote the same decomposition
// instead of each re-deriving it.
type CustomerPrice struct {
	Model            string  `json:"model"`
	OfficialInputUSD float64 `json:"official_input_usd"`
	OfficialOutputUS float64 `json:"official_output_usd"`
	ModelDiscount    float64 `json:"model_discount"`
	GroupRatio       float64 `json:"group_ratio"`
	InputUSD         float64 `json:"input_usd"`
	OutputUSD        float64 `json:"output_usd"`
	CacheReadUSD     float64 `json:"cache_read_usd,omitempty"`
	CacheWriteUSD    float64 `json:"cache_write_usd,omitempty"`
}

// CustomerPriceFor decomposes what a group pays for a model, in USD per 1M
// tokens. Returns false for an uncatalogued model, which is also unsellable.
func CustomerPriceFor(model, group string) (CustomerPrice, bool) {
	entry, ok := CatalogEntryFor(model)
	if !ok {
		return CustomerPrice{}, false
	}

	discount := GetModelDiscount(model)
	groupRatio := GetGroupRatio(group)
	factor := discount * groupRatio

	price := CustomerPrice{
		Model:            entry.Model,
		OfficialInputUSD: entry.InputUSD,
		OfficialOutputUS: entry.OutputUSD,
		ModelDiscount:    discount,
		GroupRatio:       groupRatio,
		InputUSD:         entry.InputUSD * factor,
		OutputUSD:        entry.OutputUSD * factor,
	}
	if entry.CacheReadUSD != 0 {
		price.CacheReadUSD = entry.CacheReadUSD * factor
	}
	if entry.CacheWriteUSD != 0 {
		price.CacheWriteUSD = entry.CacheWriteUSD * factor
	}
	return price, true
}
