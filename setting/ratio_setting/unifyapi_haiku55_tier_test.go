package ratio_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Anthropic's Claude Haiku 5.5 prices, typed by hand from
// https://platform.claude.com/docs/en/about-claude/pricing (read 2026-11-08),
// USD per 1M tokens:
//
//	                    input  5m write  1h write  cache hit  output
//	up to 100K tokens   0.10   0.125     0.20      0.01       0.50
//	over 100K tokens    0.50   0.625     1.00      0.05       2.50
func TestHaiku55BillsLongPromptsAtAnthropicsOver100KRates(t *testing.T) {
	InitRatioSettings()

	expr, ok := billing_setting.GetBillingExpr("claude-haiku-5-5")
	require.True(t, ok, "a context tier must put the model on an expression")
	assert.Equal(t, billing_setting.BillingModeTieredExpr, billing_setting.GetBillingMode("claude-haiku-5-5"))

	cases := []struct {
		name   string
		params billingexpr.TokenParams
		want   float64
	}{
		{"50K prompt", billingexpr.TokenParams{P: 1_000_000, C: 1_000_000, CR: 1_000_000, CC: 1_000_000, CC1h: 1_000_000, Len: 50_000},
			0.10 + 0.50 + 0.01 + 0.125 + 0.20},
		{"exactly 100K", billingexpr.TokenParams{P: 1_000_000, Len: 100_000}, 0.10},
		{"150K prompt", billingexpr.TokenParams{P: 1_000_000, C: 1_000_000, CR: 1_000_000, CC: 1_000_000, CC1h: 1_000_000, Len: 150_000},
			0.50 + 2.50 + 0.05 + 0.625 + 1.00},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.InDelta(t, tc.want, exprDollars(t, expr, tc.params), 1e-9)
		})
	}
}
