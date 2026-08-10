package setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withStripeCurrencyConfig(t *testing.T, productId string, currenciesJSON string) {
	t.Helper()
	originalProduct, originalCurrencies := StripeProductId, StripeCurrencies
	t.Cleanup(func() {
		StripeProductId, StripeCurrencies = originalProduct, originalCurrencies
	})
	StripeProductId, StripeCurrencies = productId, currenciesJSON
}

func TestGetStripeCurrencies(t *testing.T) {
	cases := []struct {
		name      string
		productId string
		json      string
		expected  []StripeCurrency
	}{
		{
			name:      "base currency is always present and first",
			productId: "prod_x",
			json:      `[{"code":"MYR","rate":4.3}]`,
			expected:  []StripeCurrency{{Code: "USD", Rate: 1}, {Code: "MYR", Rate: 4.3}},
		},
		{
			name:      "no product id means only the base currency is offered",
			productId: "",
			json:      `[{"code":"MYR","rate":4.3},{"code":"THB","rate":36}]`,
			expected:  []StripeCurrency{{Code: "USD", Rate: 1}},
		},
		{
			name:      "codes are upper-cased and de-duplicated against the base",
			productId: "prod_x",
			json:      `[{"code":"myr","rate":4.3},{"code":"MYR","rate":9},{"code":"usd","rate":99}]`,
			expected:  []StripeCurrency{{Code: "USD", Rate: 1}, {Code: "MYR", Rate: 4.3}},
		},
		{
			name:      "non-positive rates and malformed codes are dropped, not fatal",
			productId: "prod_x",
			json:      `[{"code":"MYR","rate":0},{"code":"THBB","rate":36},{"code":"","rate":1},{"code":"SGD","rate":1.35}]`,
			expected:  []StripeCurrency{{Code: "USD", Rate: 1}, {Code: "SGD", Rate: 1.35}},
		},
		{
			name:      "unparseable json falls back to the base currency",
			productId: "prod_x",
			json:      `not json`,
			expected:  []StripeCurrency{{Code: "USD", Rate: 1}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withStripeCurrencyConfig(t, tc.productId, tc.json)
			assert.Equal(t, tc.expected, GetStripeCurrencies())
		})
	}
}

func TestFindStripeCurrency(t *testing.T) {
	withStripeCurrencyConfig(t, "prod_x", `[{"code":"MYR","rate":4.3}]`)

	t.Run("empty code resolves to the base currency", func(t *testing.T) {
		currency, ok := FindStripeCurrency("")
		require.True(t, ok)
		assert.Equal(t, StripeCurrency{Code: "USD", Rate: 1}, currency)
	})

	t.Run("lower-case configured code resolves", func(t *testing.T) {
		currency, ok := FindStripeCurrency("myr")
		require.True(t, ok)
		assert.Equal(t, StripeCurrency{Code: "MYR", Rate: 4.3}, currency)
	})

	t.Run("currency that is not configured is rejected", func(t *testing.T) {
		_, ok := FindStripeCurrency("THB")
		assert.False(t, ok)
	})
}

func TestStripeAmountToMinorUnits(t *testing.T) {
	cases := []struct {
		name     string
		amount   float64
		currency string
		expected int64
	}{
		// 10 credits at a 4.09 rate. The float path yields 4089.9999999999995,
		// so a bare int64 cast would charge a cent less than the displayed 40.90.
		{name: "float representation does not lose a minor unit", amount: 40.9, currency: "MYR", expected: 4090},
		{name: "two-decimal currency", amount: 13.5, currency: "SGD", expected: 1350},
		{name: "sub-unit amount rounds to nearest", amount: 0.005, currency: "USD", expected: 1},
		{name: "zero-decimal currency is not scaled", amount: 1500, currency: "JPY", expected: 1500},
		{name: "unknown currency defaults to two decimals", amount: 12.34, currency: "THB", expected: 1234},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			minorUnits, err := StripeAmountToMinorUnits(tc.amount, tc.currency)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, minorUnits)
		})
	}

	t.Run("amount rounding to zero is rejected", func(t *testing.T) {
		_, err := StripeAmountToMinorUnits(0.0001, "USD")
		require.Error(t, err)
	})

	t.Run("amount past the gateway ceiling is rejected", func(t *testing.T) {
		_, err := StripeAmountToMinorUnits(1_000_000_000, "USD")
		require.Error(t, err)
	})
}
