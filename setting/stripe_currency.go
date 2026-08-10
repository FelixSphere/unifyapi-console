package setting

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
)

// StripeCurrency is one selectable checkout currency and its rate against USD.
//
// Rate is "1 USD = Rate <Code>", the same direction as
// operation_setting.USDExchangeRate. USD itself is always present with rate 1
// and is not configurable, so a misconfigured list can never take the site's
// base currency offline.
type StripeCurrency struct {
	Code string  `json:"code"`
	Rate float64 `json:"rate"`
}

const (
	// StripeBaseCurrency is the currency StripeUnitPrice is denominated in.
	StripeBaseCurrency = "USD"

	// stripeMaxMinorUnits caps a single charge. Stripe itself rejects amounts
	// above 8 digits; bounding here turns a gateway error into a validated
	// rejection and keeps an absurd rate from producing an absurd charge.
	stripeMaxMinorUnits = 99999999

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

// GetStripeCurrencies returns the enabled checkout currencies, USD first.
//
// Invalid entries are dropped rather than failing the whole list: a bad row in
// an admin-edited JSON blob must not take topup offline. A currency other than
// USD is only usable when StripeProductId is set, because inline pricing is the
// only way to charge a currency the configured Price does not carry.
func GetStripeCurrencies() []StripeCurrency {
	currencies := []StripeCurrency{{Code: StripeBaseCurrency, Rate: 1}}
	if strings.TrimSpace(StripeProductId) == "" {
		return currencies
	}

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

// StripeAmountToMinorUnits converts a display amount into the integer minor
// units Stripe charges.
//
// The conversion goes through decimal because the float path truncates: at a
// rate of 4.09, float64(10*4.09)*100 is 4089.9999999999995, which an int cast
// turns into a charge one cent short of the amount the user was shown.
func StripeAmountToMinorUnits(amount float64, code string) (int64, error) {
	minor := decimal.NewFromFloat(amount).Shift(StripeCurrencyExponent(code)).Round(0)
	if !minor.IsPositive() {
		return 0, fmt.Errorf("支付金额过低: %s %.4f", code, amount)
	}
	if minor.GreaterThan(decimal.NewFromInt(stripeMaxMinorUnits)) {
		return 0, fmt.Errorf("支付金额超出上限: %s %.4f", code, amount)
	}
	return minor.IntPart(), nil
}
