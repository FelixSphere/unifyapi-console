package helper

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The pre-deduction must cover what the request can cost, and the expensive
// half of a request is its output. claude-opus-5 lists $5 in / $25 out per 1M
// (typed by hand, not read from the catalogue under test):
//
//	1,000 prompt    x $5/1M  = $0.005
//	32,000 max out  x $25/1M = $0.800
//	hold                       $0.805 = 402,500 quota
//
// Holding max_tokens at the input price instead reserved $0.165, a fifth of
// the worst case, so a $0.50 wallet was admitted and settled into debt.
func TestMaxTokensAreReservedAtTheOutputPrice(t *testing.T) {
	resetPricingState(t)

	ctx, info := billingContextFor(t, "claude-opus-5", "default")
	priceData, err := ModelPriceHelper(ctx, info, 1_000, &types.TokenCountMeta{MaxTokens: 32_000})
	require.NoError(t, err)

	assert.Equal(t, 402_500, priceData.QuotaToPreConsume)
}
