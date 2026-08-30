package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withStripePricingConfig(t *testing.T, priceId string, unitPrice float64) {
	t.Helper()
	originalPrice, originalUnitPrice := setting.StripePriceId, setting.StripeUnitPrice
	t.Cleanup(func() {
		setting.StripePriceId = originalPrice
		setting.StripeUnitPrice = originalUnitPrice
	})
	setting.StripePriceId = priceId
	setting.StripeUnitPrice = unitPrice
}

// Every checkout must bill the configured Price in the settlement currency,
// whatever currency the wallet page quoted. Presenting a non-settlement
// currency here switches off Stripe Adaptive Pricing, which is what localises
// the amount for foreign buyers at a rate Stripe guarantees and we pay nothing
// for. It would also leave us charging a rate this server stores, which is the
// only way this flow could take on FX risk.
func TestStripeTopUpLineItemAlwaysBillsThePriceInTheSettlementCurrency(t *testing.T) {
	withStripePricingConfig(t, "price_live_1", 1)

	lineItem, err := stripeTopUpLineItem(10)
	require.NoError(t, err)

	require.NotNil(t, lineItem.Price)
	assert.Equal(t, "price_live_1", *lineItem.Price)
	require.NotNil(t, lineItem.Quantity)
	assert.Equal(t, int64(10), *lineItem.Quantity)
	assert.Nil(t, lineItem.PriceData, "inline pricing would disable Adaptive Pricing")
}

func TestStripeTopUpLineItemRejectsAProductIdInThePriceField(t *testing.T) {
	withStripePricingConfig(t, "prod_wrong_field", 1)
	_, err := stripeTopUpLineItem(10)
	require.Error(t, err)
}

// getStripePayMoney now only produces the wallet page estimate, but it still
// must not distort the base currency: a USD quote has to equal the amount the
// Price will actually charge, or the page contradicts the checkout it opens.
func TestGetStripePayMoneyAppliesTheCurrencyRateLast(t *testing.T) {
	withStripePricingConfig(t, "price_live_1", 8)

	cases := []struct {
		name     string
		currency setting.StripeCurrency
		expected float64
	}{
		{
			name:     "base currency is unchanged by multi-currency support",
			currency: setting.StripeCurrency{Code: "USD", Rate: 1},
			expected: 80,
		},
		{
			name:     "foreign currency scales the base total",
			currency: setting.StripeCurrency{Code: "MYR", Rate: 4.09},
			expected: 327.2,
		},
		{
			name:     "a non-positive rate falls back to 1 rather than charging zero",
			currency: setting.StripeCurrency{Code: "MYR", Rate: 0},
			expected: 80,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payMoney := getStripePayMoney(10, "unconfigured-group", tc.currency)
			assert.InDelta(t, tc.expected, payMoney, 0.000001)
		})
	}
}
