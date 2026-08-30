package setting

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// StripeCurrency is one currency the wallet page can quote an estimate in, and
// its rate against USD.
//
// This never prices a charge. Every checkout is created in the account's
// settlement currency and Stripe Adaptive Pricing localises it at a rate Stripe
// guarantees through settlement; quoting the customer in a currency here only
// tells them roughly what to expect. Rate is "1 USD = Rate <Code>", the same
// direction as operation_setting.USDExchangeRate. USD itself is always present
// with rate 1 and is not configurable, so a misconfigured list can never take
// the site's base currency offline.
//
// Set rates slightly above mid-market. Stripe's conversion markup is borne by
// the buyer (documented 2-4%), so an estimate at bare mid-market reads as
// cheaper than the checkout page will, and the difference looks like a bait.
type StripeCurrency struct {
	Code string  `json:"code"`
	Rate float64 `json:"rate"`
}

const (
	// StripeBaseCurrency is the currency StripeUnitPrice is denominated in.
	StripeBaseCurrency = "USD"

	defaultStripeCurrenciesJSON = `[{"code":"USD","rate":1}]`
)

// stripeCurrencyExponents holds the minor-unit exponent for every currency
// whose exponent is not 2. Getting this wrong overcharges or undercharges by
// 100x, so new currencies must be checked against Stripe's zero-decimal list
// rather than assumed.
var stripeCurrencyExponents = map[string]int32{
	"BIF": 0, "CLP": 0, "DJF": 0, "GNF": 0, "JPY": 0, "KMF": 0, "KRW": 0,
	"MGA": 0, "PYG": 0, "RWF": 0, "UGX": 0, "VND": 0, "VUV": 0, "XAF": 0,
	"XOF": 0, "XPF": 0,
}

// GetStripeCurrencies returns the currencies the wallet page may quote, USD
// first.
//
// Invalid entries are dropped rather than failing the whole list: a bad row in
// an admin-edited JSON blob must not take topup offline.
func GetStripeCurrencies() []StripeCurrency {
	currencies := []StripeCurrency{{Code: StripeBaseCurrency, Rate: 1}}

	var parsed []StripeCurrency
	if err := common.UnmarshalJsonStr(StripeCurrencies, &parsed); err != nil {
		return currencies
	}

	seen := map[string]bool{StripeBaseCurrency: true}
	for _, currency := range parsed {
		code := strings.ToUpper(strings.TrimSpace(currency.Code))
		if len(code) != 3 || seen[code] {
			continue
		}
		if currency.Rate <= 0 {
			continue
		}
		seen[code] = true
		currencies = append(currencies, StripeCurrency{Code: code, Rate: currency.Rate})
	}
	return currencies
}

// FindStripeCurrency resolves a client-supplied currency code. An empty code
// means the base currency, so existing clients that do not send one keep
// working unchanged.
func FindStripeCurrency(code string) (StripeCurrency, bool) {
	normalized := strings.ToUpper(strings.TrimSpace(code))
	if normalized == "" {
		normalized = StripeBaseCurrency
	}
	for _, currency := range GetStripeCurrencies() {
		if currency.Code == normalized {
			return currency, true
		}
	}
	return StripeCurrency{}, false
}

// StripeCurrencyExponent returns the number of decimal places the currency is
// charged in.
func StripeCurrencyExponent(code string) int32 {
	if exponent, ok := stripeCurrencyExponents[strings.ToUpper(strings.TrimSpace(code))]; ok {
		return exponent
	}
	return 2
}

// GetStripeEstimateMargin returns the display markup, ignoring values that
// would make the estimate useless (negative, or more than a quarter over
// mid-market).
func GetStripeEstimateMargin() float64 {
	if StripeEstimateMargin < 0 || StripeEstimateMargin > 0.25 {
		return 0.04
	}
	return StripeEstimateMargin
}
