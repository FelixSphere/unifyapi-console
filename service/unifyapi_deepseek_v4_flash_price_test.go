package service

// UNIFYAPI-FORK: deepseek-v4-flash is a legacy id that DeepSeek serves with
// V4.1 Flash and bills at the Flash price. Operator decision 2026-11-16: sell it
// at that price. Dollars typed from api-docs.deepseek.com/quick_start/pricing
// (peak tier), read 2026-11-16 -- not derived from the catalog under test.

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestDeepSeekV4FlashBillsAtTheFlashPrice(t *testing.T) {
	withDiscount(t, `{}`)

	require.InDelta(t, 0.3+1.2, chargeUSD(t, "deepseek-v4-flash", "default", oneMillionEach()), 1e-6)
	// Same id, same money as the name DeepSeek now documents.
	require.InDelta(t, chargeUSD(t, "deepseek-flash", "default", oneMillionEach()),
		chargeUSD(t, "deepseek-v4-flash", "default", oneMillionEach()), 1e-6)

	short := &dto.Usage{PromptTokens: 10_000, CompletionTokens: 10_000, TotalTokens: 20_000}
	require.InDelta(t, (0.3+1.2)/100, chargeUSD(t, "deepseek-v4-flash", "default", short), 1e-6)
}
