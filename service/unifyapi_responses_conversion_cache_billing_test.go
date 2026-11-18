package service

import (
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"

	"github.com/stretchr/testify/require"
)

// A chat-completions call that the relay serves over the Responses API
// (global.chat_completions_to_responses_policy) settles from the usage the
// conversion hands back. That usage carries the upstream's cache counts under
// input_tokens_details, the Responses spelling, inside its billing snapshot.
// Settlement read only prompt_tokens_details, so every cached and cache-write
// token was billed again at the full input price: production billed 117
// converted gpt-6-astra calls with 0 cached tokens against 88% cache reads on
// the same channel before the policy was switched on.
//
// The expression is the one production carries for gpt-6.2-sol. The expected
// quotas are typed from OpenAI's list prices, not derived from the expression.
func TestConvertedResponsesUsageBillsCacheAtCachePrices(t *testing.T) {
	const gpt62SolExpr = `len <= 272000 ? tier("standard", p * 2 + c * 10 + cr * 0.1 + cc * 2.5) : tier("long_context", p * 4 + c * 15 + cr * 0.2 + cc * 5)`

	cases := []struct {
		name       string
		upstream   dto.Usage
		wantTier   string
		wantQuota  int
		wantCached int
		wantWrite  int
	}{
		{
			// 1,000 fresh x $2 + 500 out x $10 + 6,000 read x $0.10 +
			// 3,000 written x $2.50 = $0.0151 = 7,550 quota.
			// Billing all 10,000 as fresh input would be 12,500.
			name: "standard tier",
			upstream: dto.Usage{
				InputTokens: 10000, OutputTokens: 500, TotalTokens: 10500,
				InputTokensDetails: &dto.InputTokenDetails{CachedTokens: 6000, CacheWriteTokens: 3000},
			},
			wantTier: "standard", wantQuota: 7550, wantCached: 6000, wantWrite: 3000,
		},
		{
			// Over 272K the tier is chosen on the whole prompt, cache included:
			// 50,000 fresh x $4 + 500 out x $15 + 200,000 read x $0.20 +
			// 50,000 written x $5 = $0.4975 = 248,750 quota.
			name: "long context tier",
			upstream: dto.Usage{
				InputTokens: 300000, OutputTokens: 500, TotalTokens: 300500,
				InputTokensDetails: &dto.InputTokenDetails{CachedTokens: 200000, CacheWriteTokens: 50000},
			},
			wantTier: "long_context", wantQuota: 248750, wantCached: 200000, wantWrite: 50000,
		},
		{
			// No cache at all must be unchanged: 10,000 x $2 + 500 x $10 = 12,500.
			name: "no cache",
			upstream: dto.Usage{
				InputTokens: 10000, OutputTokens: 500, TotalTokens: 10500,
				InputTokensDetails: &dto.InputTokenDetails{},
			},
			wantTier: "standard", wantQuota: 12500,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := tc.upstream
			converted := relayconvert.UsageFromResponsesUsage(&upstream)
			require.NotNil(t, converted.BillingUsage, "the conversion must attach the billing snapshot this test is about")

			billingUsage := effectiveBillingUsage(converted)
			require.Equal(t, tc.wantCached, billingUsage.PromptTokensDetails.CachedTokens)
			require.Equal(t, tc.wantWrite, billingUsage.PromptTokensDetails.CacheCreationTokensTotal())

			relayInfo := makeRelayInfo(gpt62SolExpr, 1, tc.upstream.InputTokens, tc.upstream.OutputTokens)
			params := BuildTieredTokenParams(billingUsage, false, billingexpr.UsedVars(gpt62SolExpr))
			ok, quota, result := TryTieredSettle(relayInfo, params)
			require.True(t, ok)
			require.NotNil(t, result)
			require.Equal(t, tc.wantTier, result.MatchedTier)
			require.Equal(t, tc.wantQuota, quota)
		})
	}
}
