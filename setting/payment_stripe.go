package setting

var StripeApiSecret = ""
var StripeWebhookSecret = ""
var StripePriceId = ""

// StripeProductId is the Stripe product that non-USD checkouts are billed
// against. Only USD topups can use StripePriceId, because a Stripe Price
// carries a fixed currency; every other currency is priced inline against this
// product. Empty means non-USD currencies are unavailable.
var StripeProductId = ""
var StripeUnitPrice = 8.0
var StripeMinTopUp = 1
var StripePromotionCodesEnabled = false

// StripeCurrencies is the admin-configured checkout currency list, as JSON.
// See stripe_currency.go for the shape and the parsing rules.
var StripeCurrencies = defaultStripeCurrenciesJSON
