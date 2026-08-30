package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/assert"
)

func withCachedRates(t *testing.T, rates map[string]float64, fetchedAt time.Time) {
	t.Helper()
	original := midMarketRates
	t.Cleanup(func() { midMarketRates = original })
	midMarketRates = &fxRates{rates: rates, fetchedAt: fetchedAt}
}

func withMargin(t *testing.T, margin float64) {
	t.Helper()
	original := setting.StripeEstimateMargin
	t.Cleanup(func() { setting.StripeEstimateMargin = original })
	setting.StripeEstimateMargin = margin
}

func TestStripeEstimateRate(t *testing.T) {
	t.Run("base currency is never converted", func(t *testing.T) {
		withCachedRates(t, map[string]float64{"USD": 99}, time.Now())
		assert.Equal(t, 1.0, StripeEstimateRate(setting.StripeBaseCurrency, 7))
	})

	t.Run("a live rate is marked up so the estimate is not below Stripe's", func(t *testing.T) {
		// Stripe's markup is charged to the buyer, so quoting bare mid-market
		// would read cheaper than the checkout page.
		withCachedRates(t, map[string]float64{"MYR": 4.0}, time.Now())
		withMargin(t, 0.04)
		assert.InDelta(t, 4.16, StripeEstimateRate("MYR", 9.99), 0.000001)
	})

	t.Run("an unknown currency falls back to the configured rate", func(t *testing.T) {
		withCachedRates(t, map[string]float64{"MYR": 4.0}, time.Now())
		assert.Equal(t, 36.5, StripeEstimateRate("THB", 36.5))
	})

	t.Run("an empty cache falls back rather than quoting zero", func(t *testing.T) {
		// A zero rate would render the topup page as free.
		withCachedRates(t, map[string]float64{}, time.Time{})
		assert.Equal(t, 4.19, StripeEstimateRate("MYR", 4.19))
	})

	t.Run("a rate stale beyond the limit falls back", func(t *testing.T) {
		// A feed failing quietly for days must not keep quoting last week.
		withCachedRates(t, map[string]float64{"MYR": 4.0}, time.Now().Add(-fxMaxStaleness-time.Hour))
		assert.Equal(t, 4.19, StripeEstimateRate("MYR", 4.19))
	})

	t.Run("an absurd configured margin is ignored", func(t *testing.T) {
		withCachedRates(t, map[string]float64{"MYR": 4.0}, time.Now())
		withMargin(t, 5)
		assert.InDelta(t, 4.16, StripeEstimateRate("MYR", 9.99), 0.000001)
	})
}

func TestEffectiveStripeCurrenciesAppliesLiveRates(t *testing.T) {
	originalCurrencies := setting.StripeCurrencies
	t.Cleanup(func() { setting.StripeCurrencies = originalCurrencies })
	setting.StripeCurrencies = `[{"code":"MYR","rate":9.99}]`

	withCachedRates(t, map[string]float64{"MYR": 4.0}, time.Now())
	withMargin(t, 0.04)

	effective := EffectiveStripeCurrencies()
	assert.Equal(t, []setting.StripeCurrency{
		{Code: "USD", Rate: 1},
		{Code: "MYR", Rate: 4.16},
	}, effective)
}
