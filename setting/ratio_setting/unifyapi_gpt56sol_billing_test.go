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

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The expression production served for gpt-5.6-sol until 2026-10-23, copied
// verbatim from /api/pricing. It is gpt-5.5's price ($5/$30) on gpt-5.6-sol.
const handWrittenGpt56SolExpr = `len <= 272000 ? tier("0_272k", p * 5 + c * 30 + cr * 0.5 + cc * 6.25) : tier("272k_plus", p * 10 + c * 45 + cr * 1 + cc * 12.5)`

// OpenAI's published gpt-5.6-sol prices (developers.openai.com/api/docs/pricing,
// read 2026-10-17), typed by hand rather than read from the catalog under test:
//
//	<= 272K input: $4 in / $20 out / $0.40 cached / $5 cache write
//	 > 272K input: $8 in / $30 out / $0.80 cached / $10 cache write
func TestGpt56SolBillsOpenAIsPublishedPrice(t *testing.T) {
	entry, ok := CatalogEntryFor("gpt-5.6-sol")
	require.True(t, ok)
	expr := entry.BillingExpr(1)
	require.NotEmpty(t, expr, "gpt-5.6-sol must be on a catalog expression, or a hand-written one wins")

	million := billingexpr.TokenParams{P: 1_000_000, C: 1_000_000, CR: 1_000_000, CC: 1_000_000, Len: 100_000}
	assert.InDelta(t, 4+20+0.4+5, exprDollars(t, expr, million), 1e-9, "short-context prices")

	million.Len = 300_000
	assert.InDelta(t, 8+30+0.8+10, exprDollars(t, expr, million), 1e-9, "prices above 272K input tokens")
}

func TestARebuildReplacesTheHandWrittenGpt56SolExpression(t *testing.T) {
	InitRatioSettings()
	billing_setting.SetBillingExprForTest("gpt-5.6-sol", handWrittenGpt56SolExpr)

	InitRatioSettings()

	installed, ok := billing_setting.GetBillingExpr("gpt-5.6-sol")
	require.True(t, ok)
	assert.Equal(t, billing_setting.BillingModeTieredExpr, billing_setting.GetBillingMode("gpt-5.6-sol"))
	assert.Equal(t, BillingExprs()["gpt-5.6-sol"], installed)

	oneMillionEach := billingexpr.TokenParams{P: 1_000_000, C: 1_000_000, Len: 100_000}
	assert.InDelta(t, 24, exprDollars(t, installed, oneMillionEach), 1e-9,
		"1M in + 1M out is $4 + $20; the hand-written expression billed $35")
}
