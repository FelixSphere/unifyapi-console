package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withStripePricingConfig(t *testing.T, priceId string, productId string, unitPrice float64) {
	t.Helper()
	originalPrice, originalProduct, originalUnitPrice := setting.StripePriceId, setting.StripeProductId, setting.StripeUnitPrice
	t.Cleanup(func() {
		setting.StripePriceId = originalPrice
		setting.StripeProductId = originalProduct
		setting.StripeUnitPrice = originalUnitPrice
	})
	setting.StripePriceId = priceId
	setting.StripeProductId = productId
	setting.StripeUnitPrice = unitPrice
}

// The base-currency checkout must keep billing against the configured Price
// with the credit count as the quantity. That is the shape production runs, so
// any drift here silently changes what every existing USD topup charges.
func TestStripeTopUpLineItemBaseCurrencyBillsThePriceByQuantity(t *testing.T) {
	withStripePricingConfig(t, "price_live_1", "prod_live_1", 1)

	lineItem, err := stripeTopUpLineItem(10, setting.StripeBaseCurrency, 10)
	require.NoError(t, err)

	require.NotNil(t, lineItem.Price)
	assert.Equal(t, "price_live_1", *lineItem.Price)
	require.NotNil(t, lineItem.Quantity)
	assert.Equal(t, int64(10), *lineItem.Quantity)
	assert.Nil(t, lineItem.PriceData, "base currency must not switch to inline pricing")
}

// A non-base currency is priced inline for the exact total already shown to the
// user: one unit at quantity 1, so no per-credit rounding can drift away from
// the displayed figure.
func TestStripeTopUpLineItemForeignCurrencyChargesTheDisplayedTotal(t *testing.T) {
	withStripePricingConfig(t, "price_live_1", "prod_live_1", 1)

	lineItem, err := stripeTopUpLineItem(10, "MYR", 40.9)
	require.NoError(t, err)

	assert.Nil(t, lineItem.Price)
	require.NotNil(t, lineItem.PriceData)
	require.NotNil(t, lineItem.PriceData.Currency)
	assert.Equal(t, "myr", *lineItem.PriceData.Currency, "Stripe expects a lower-case code")
	require.NotNil(t, lineItem.PriceData.Product)
	assert.Equal(t, "prod_live_1", *lineItem.PriceData.Product)
	require.NotNil(t, lineItem.PriceData.UnitAmount)
	assert.Equal(t, int64(4090), *lineItem.PriceData.UnitAmount)
	require.NotNil(t, lineItem.Quantity)
	assert.Equal(t, int64(1), *lineItem.Quantity)
}

func TestStripeTopUpLineItemRejectsMisconfiguredIds(t *testing.T) {
	t.Run("base currency needs a price id", func(t *testing.T) {
		withStripePricingConfig(t, "prod_wrong_field", "prod_live_1", 1)
		_, err := stripeTopUpLineItem(10, setting.StripeBaseCurrency, 10)
		require.Error(t, err)
	})

	t.Run("foreign currency needs a product id", func(t *testing.T) {
		withStripePricingConfig(t, "price_live_1", "", 1)
		_, err := stripeTopUpLineItem(10, "MYR", 40.9)
		require.Error(t, err)
	})

	t.Run("foreign currency rejects a price id in the product field", func(t *testing.T) {
		withStripePricingConfig(t, "price_live_1", "price_wrong_field", 1)
		_, err := stripeTopUpLineItem(10, "MYR", 40.9)
		require.Error(t, err)
	})
}

// The currency rate is the only new factor in the pricing formula, so the base
// currency must come out exactly where it did before multi-currency existed.
func TestGetStripePayMoneyAppliesTheCurrencyRateLast(t *testing.T) {
	withStripePricingConfig(t, "price_live_1", "prod_live_1", 8)

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
