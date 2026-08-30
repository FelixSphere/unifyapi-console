package ratio_setting

// UNIFYAPI-FORK: tests for admin-added model prices.
//
// This table exists so that adding a model does not need a deploy. It replaces
// an escape hatch that destroyed the whole baseline on save, so the property
// that matters most is not "an extra prices a model" -- it is that an extra
// CANNOT reach the compiled catalog, in either direction: it may not overwrite
// a compiled price, and removing it may not leave anything behind.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// withExtras installs an extras table for one test and restores the previous
// one afterwards, rebuilding the ratio maps both times so a test cannot leak
// pricing into the next.
func withExtras(t *testing.T, jsonStr string) {
	t.Helper()
	previous := ExtraModels2JSONString()
	require.NoError(t, UpdateExtraModelsByJSONString(jsonStr))
	t.Cleanup(func() {
		require.NoError(t, UpdateExtraModelsByJSONString(previous))
	})
}

// TestExtraModelBecomesSellable is the point of the whole table: a model the
// binary has never heard of gets a price, and every downstream map picks it up
// without a deploy.
func TestExtraModelBecomesSellable(t *testing.T) {
	_, ok, _ := GetModelRatio("brand-new-model")
	require.False(t, ok, "precondition: the model must be unknown before the test")

	withExtras(t, `{"brand-new-model":{"input_usd":2,"output_usd":8,"cache_read_usd":0.2}}`)

	ratio, ok, _ := GetModelRatio("brand-new-model")
	require.True(t, ok, "an extra model must be sellable, not refused")
	require.InDelta(t, 1, ratio, 1e-9, "$2/1M is ratio 1 under the 2 USD-per-unit convention")

	require.InDelta(t, 4, GetCompletionRatio("brand-new-model"), 1e-9, "8/2")
	cacheRatio, hasCache := GetCacheRatio("brand-new-model")
	require.True(t, hasCache)
	require.InDelta(t, 0.1, cacheRatio, 1e-9, "0.2/2")

	entry, ok := CatalogEntryFor("brand-new-model")
	require.True(t, ok)
	require.True(t, entry.AdminAdded, "the pricing page has to be able to say nobody vetted this")
	require.True(t, entry.Unverified, "no models.dev listing exists to check it against")
}

// TestExtraModelCannotOverwriteACompiledPrice. This is the rule that stops the
// new table from becoming the old one. There is exactly one source of truth per
// model, and for a catalogued model that source is the code.
func TestExtraModelCannotOverwriteACompiledPrice(t *testing.T) {
	err := UpdateExtraModelsByJSONString(`{"claude-opus-4-8":{"input_usd":0.01,"output_usd":0.01}}`)
	require.Error(t, err, "an extra naming a catalogued model must be refused")
	require.Contains(t, err.Error(), "ModelDiscount",
		"the refusal has to say where to go instead, or it just reads as a wall")

	// And the refusal is total: nothing was written on the way to failing.
	entry, ok := CatalogEntryFor("claude-opus-4-8")
	require.True(t, ok)
	require.InDelta(t, 5, entry.InputUSD, 1e-9, "the official price must be untouched")
	require.False(t, entry.AdminAdded)
}

// TestRemovingAnExtraLeavesNothingBehind is the failure the rebuild exists to
// prevent. modelRatioMap is rebuilt from scratch, but if the derived maps were
// only ever added to, a deleted model's completion and cache multipliers would
// linger and later attach themselves to whatever model took that name.
func TestRemovingAnExtraLeavesNothingBehind(t *testing.T) {
	withExtras(t, `{"temp-model":{"input_usd":3,"output_usd":30,"cache_read_usd":0.3}}`)
	require.InDelta(t, 10, GetCompletionRatio("temp-model"), 1e-9)
	cacheRatio, hasCache := GetCacheRatio("temp-model")
	require.True(t, hasCache)
	require.InDelta(t, 0.1, cacheRatio, 1e-9)

	require.NoError(t, UpdateExtraModelsByJSONString(`{}`))

	_, ok, _ := GetModelRatio("temp-model")
	require.False(t, ok, "the model must stop being sellable")
	require.InDelta(t, 1, GetCompletionRatio("temp-model"), 1e-9,
		"a stale completion multiplier would silently reprice the next model to take this name")
	_, staleCache := GetCacheRatio("temp-model")
	require.False(t, staleCache, "a stale cache multiplier would outlive the price it belonged to")
	_, hasEntry := CatalogEntryFor("temp-model")
	require.False(t, hasEntry)
}

// TestExtrasDoNotDisturbTheCompiledCatalog -- merge, not replace. This is the
// exact regression that motivated the table: one save used to discard 54 models.
func TestExtrasDoNotDisturbTheCompiledCatalog(t *testing.T) {
	before := CatalogModels()
	require.NotEmpty(t, before)

	withExtras(t, `{"extra-a":{"input_usd":1,"output_usd":2},"extra-b":{"input_usd":1,"output_usd":2}}`)

	after := CatalogModels()
	require.Len(t, after, len(before)+2, "extras add, they never replace")
	for _, model := range before {
		require.Contains(t, after, model, "%s disappeared when an extra was added", model)
	}

	// Spot-check that a compiled price is still the compiled price.
	ratio, ok, _ := GetModelRatio("claude-opus-4-8")
	require.True(t, ok)
	require.InDelta(t, 2.5, ratio, 1e-9)
}

// TestDiscountsApplyToExtras. An extra that ignored the discount table would be
// sold at list while the console showed a discount, which is the kind of
// disagreement a customer finds first.
func TestDiscountsApplyToExtras(t *testing.T) {
	withExtras(t, `{"discountable":{"input_usd":4,"output_usd":8}}`)

	previous := ModelDiscount2JSONString()
	require.NoError(t, UpdateModelDiscountByJSONString(`{"discountable":0.5}`))
	t.Cleanup(func() { require.NoError(t, UpdateModelDiscountByJSONString(previous)) })

	ratio, ok, _ := GetModelRatio("discountable")
	require.True(t, ok)
	require.InDelta(t, 1, ratio, 1e-9, "$4/1M is ratio 2; a 0.5 discount halves it")
}

func TestValidateExtraModelsRejectsBadInput(t *testing.T) {
	cases := map[string]struct {
		extras map[string]ExtraModel
		expect string
	}{
		"zero input":   {map[string]ExtraModel{"m": {InputUSD: 0, OutputUSD: 1}}, "输入价"},
		"zero output":  {map[string]ExtraModel{"m": {InputUSD: 1, OutputUSD: 0}}, "输出价"},
		"negative":     {map[string]ExtraModel{"m": {InputUSD: -1, OutputUSD: 1}}, "输入价"},
		"empty name":   {map[string]ExtraModel{" ": {InputUSD: 1, OutputUSD: 1}}, "不能为空"},
		"untrimmed":    {map[string]ExtraModel{"m ": {InputUSD: 1, OutputUSD: 1}}, "空格"},
		"absurd price": {map[string]ExtraModel{"m": {InputUSD: 5000, OutputUSD: 1}}, "小数点"},
		"cache inverted": {map[string]ExtraModel{
			"m": {InputUSD: 1, OutputUSD: 2, CacheReadUSD: 5}}, "方向反了"},
		"negative cache": {map[string]ExtraModel{
			"m": {InputUSD: 1, OutputUSD: 2, CacheReadUSD: -1}}, "不能为负"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			problems := ValidateExtraModels(tc.extras)
			require.NotEmpty(t, problems, "must be rejected")
			require.Contains(t, problems[0].Error(), tc.expect)
		})
	}
}

// TestValidationReportsEveryBadRow -- an admin pasting a table wants to fix all
// of it in one pass, not discover the rows one save at a time.
func TestValidationReportsEveryBadRow(t *testing.T) {
	problems := ValidateExtraModels(map[string]ExtraModel{
		"a": {InputUSD: 0, OutputUSD: 1},
		"b": {InputUSD: 1, OutputUSD: 0},
		"c": {InputUSD: 1, OutputUSD: 2},
	})
	require.Len(t, problems, 2)
}

// TestARejectedSaveChangesNothing. Validation runs against the incoming payload
// before it is loaded, so a partly-valid table cannot half-apply.
func TestARejectedSaveChangesNothing(t *testing.T) {
	withExtras(t, `{"keeper":{"input_usd":1,"output_usd":2}}`)

	err := UpdateExtraModelsByJSONString(
		`{"keeper":{"input_usd":9,"output_usd":9},"broken":{"input_usd":0,"output_usd":1}}`)
	require.Error(t, err)

	entry, ok := CatalogEntryFor("keeper")
	require.True(t, ok, "the existing extra must survive a rejected save")
	require.InDelta(t, 1, entry.InputUSD, 1e-9, "and keep its old price, not the rejected one")
	_, added := CatalogEntryFor("broken")
	require.False(t, added)
}
