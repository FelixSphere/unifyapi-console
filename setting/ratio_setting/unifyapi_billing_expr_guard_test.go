/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package ratio_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The five hand-written expressions production served on 2026-10-30, copied
// verbatim from /api/pricing. gpt-5.6-sol's is gpt-5.5's price.
var productionHandWrittenExprs = map[string]string{
	"gpt-5.4":       `len <= 272000 ? tier("0_272k", p * 2.5 + c * 15 + cr * 0.25) : tier("272k_plus", p * 5 + c * 22.5 + cr * 0.5)`,
	"gpt-5.5":       `len <= 272000 ? tier("0_272k", p * 5 + c * 30 + cr * 0.5) : tier("272k_plus", p * 10 + c * 45 + cr * 1)`,
	"gpt-5.6-luna":  `len <= 272000 ? tier("0_272k", p * 0.2 + c * 1.2 + cr * 0.02 + cc * 0.25) : tier("272k_plus", p * 0.4 + c * 1.8 + cr * 0.04 + cc * 0.5)`,
	"gpt-5.6-sol":   `len <= 272000 ? tier("0_272k", p * 5 + c * 30 + cr * 0.5 + cc * 6.25) : tier("272k_plus", p * 10 + c * 45 + cr * 1 + cc * 12.5)`,
	"gpt-5.6-terra": `len <= 272000 ? tier("0_272k", p * 2 + c * 12 + cr * 0.2 + cc * 2.5) : tier("272k_plus", p * 4 + c * 18 + cr * 0.4 + cc * 5)`,
}

// OpenAI's Standard-tier prices (developers.openai.com/api/docs/pricing, read
// 2026-10-30), typed by hand: 1M input + 1M output, at or below 272K input
// tokens and above it.
var openAIOneMillionEach = map[string]struct{ short, long float64 }{
	"gpt-5.4":       {2.5 + 15, 5 + 22.5},
	"gpt-5.5":       {5 + 30, 10 + 45},
	"gpt-5.6-luna":  {0.2 + 1.2, 0.4 + 1.8},
	"gpt-5.6-sol":   {4 + 20, 8 + 30},
	"gpt-5.6-terra": {2 + 12, 4 + 18},
}

func TestNoProductionHandWrittenExpressionSurvivesARebuild(t *testing.T) {
	InitRatioSettings()
	for model, expr := range productionHandWrittenExprs {
		billing_setting.SetBillingExprForTest(model, expr)
	}

	InitRatioSettings()

	for model, want := range openAIOneMillionEach {
		t.Run(model, func(t *testing.T) {
			installed, ok := billing_setting.GetBillingExpr(model)
			require.True(t, ok)
			assert.Equal(t, BillingExprs()[model], installed, "the catalog's expression must replace the hand-written one")

			params := billingexpr.TokenParams{P: 1_000_000, C: 1_000_000, Len: 100_000}
			assert.InDelta(t, want.short, exprDollars(t, installed, params), 1e-9, "up to 272K input tokens")
			params.Len = 300_000
			assert.InDelta(t, want.long, exprDollars(t, installed, params), 1e-9, "above 272K input tokens")
		})
	}
}

// A model the catalog prices flat has no expression at all, so a hand-written
// one is removed rather than replaced, and the model bills its catalog ratio.
func TestAHandWrittenExpressionOnAFlatPricedModelIsRemoved(t *testing.T) {
	InitRatioSettings()
	require.NotContains(t, BillingExprs(), "gpt-4o")
	billing_setting.SetBillingExprForTest("gpt-4o", `tier("base", p * 9 + c * 9)`)

	dropped := EnforceCatalogBillingExprs()

	assert.Equal(t, []string{"gpt-4o"}, dropped)
	_, ok := billing_setting.GetBillingExpr("gpt-4o")
	assert.False(t, ok)
	assert.Equal(t, billing_setting.BillingModeRatio, billing_setting.GetBillingMode("gpt-4o"))
}

func TestSavingAHandWrittenExpressionForACataloguedModelIsRefused(t *testing.T) {
	InitRatioSettings()

	err := ValidateBillingSettingOption("billing_setting.billing_expr",
		`{"gpt-5.6-sol":`+quote(productionHandWrittenExprs["gpt-5.6-sol"])+`}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gpt-5.6-sol")

	err = ValidateBillingSettingOption("billing_setting.billing_expr", `{"gpt-4o":"tier(\"base\", p * 2.5 + c * 10)"}`)
	require.Error(t, err, "a flat-priced model cannot take an expression, even one matching its price")
	assert.Contains(t, err.Error(), "gpt-4o")
}

func TestSavingTheCatalogsOwnExpressionIsAllowed(t *testing.T) {
	InitRatioSettings()
	catalog := BillingExprs()["gpt-5.6-sol"]
	require.NotEmpty(t, catalog)

	reflowed := "  " + catalog + "\n"
	assert.NoError(t, ValidateBillingSettingOption("billing_setting.billing_expr", `{"gpt-5.6-sol":`+quote(reflowed)+`}`))
	assert.NoError(t, ValidateBillingSettingOption("billing_setting.billing_expr",
		`{"not-in-the-catalog":"tier(\"base\", p * 9)"}`), "a model the catalog does not price is not the catalog's to police")
}

func TestABillingModeThatDisagreesWithTheCatalogIsRefused(t *testing.T) {
	InitRatioSettings()

	assert.Error(t, ValidateBillingSettingOption("billing_setting.billing_mode", `{"gpt-4o":"tiered_expr"}`))
	assert.Error(t, ValidateBillingSettingOption("billing_setting.billing_mode", `{"gpt-5.6-sol":"ratio"}`))
	assert.NoError(t, ValidateBillingSettingOption("billing_setting.billing_mode", `{"gpt-5.6-sol":"tiered_expr","gpt-4o":"ratio"}`))
	assert.NoError(t, ValidateBillingSettingOption("billing_setting.billing_mode", `{"not-in-the-catalog":"tiered_expr"}`))
}

func quote(s string) string {
	b, err := common.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}
