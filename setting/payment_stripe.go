package setting

var StripeApiSecret = ""
var StripeWebhookSecret = ""
var StripePriceId = ""

var StripeUnitPrice = 8.0
var StripeMinTopUp = 1
var StripePromotionCodesEnabled = false

// StripeCurrencies is the admin-configured list of currencies the wallet page
// can show an estimate in. It never prices a charge — see stripe_currency.go.
var StripeCurrencies = defaultStripeCurrenciesJSON
