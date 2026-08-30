package setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withStripeCurrencyConfig(t *testing.T, currenciesJSON string) {
	t.Helper()
	original := StripeCurrencies
	t.Cleanup(func() { StripeCurrencies = original })
	StripeCurrencies = currenciesJSON
}

func TestGetStripeCurrencies(t *testing.T) {
	cases := []struct {
		name     string
		json     string
		expected []StripeCurrency
	}{
		{
			name:     "base currency is always present and first",
			json:     `[{"code":"MYR","rate":4.3}]`,
			expected: []StripeCurrency{{Code: "USD", Rate: 1}, {Code: "MYR", Rate: 4.3}},
		},
		{
			name:     "codes are upper-cased and de-duplicated against the base",
			json:     `[{"code":"myr","rate":4.3},{"code":"MYR","rate":9},{"code":"usd","rate":99}]`,
			expected: []StripeCurrency{{Code: "USD", Rate: 1}, {Code: "MYR", Rate: 4.3}},
		},
		{
			name:     "non-positive rates and malformed codes are dropped, not fatal",
			json:     `[{"code":"MYR","rate":0},{"code":"THBB","rate":36},{"code":"","rate":1},{"code":"SGD","rate":1.35}]`,
			expected: []StripeCurrency{{Code: "USD", Rate: 1}, {Code: "SGD", Rate: 1.35}},
		},
		{
			name:     "unparseable json falls back to the base currency",
			json:     `not json`,
			expected: []StripeCurrency{{Code: "USD", Rate: 1}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withStripeCurrencyConfig(t, tc.json)
			assert.Equal(t, tc.expected, GetStripeCurrencies())
		})
	}
}

func TestFindStripeCurrency(t *testing.T) {
	withStripeCurrencyConfig(t, `[{"code":"MYR","rate":4.3}]`)

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
