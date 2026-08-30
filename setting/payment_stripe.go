package setting

var StripeApiSecret = ""
var StripeWebhookSecret = ""
var StripePriceId = ""

var StripeUnitPrice = 8.0
var StripeMinTopUp = 1
var StripePromotionCodesEnabled = false

// StripeCurrencies is the admin-configured list of currencies the wallet page
// can show an estimate in. It never prices a charge — see stripe_currency.go.
// Each rate is the fallback used when no live reference rate is available.
var StripeCurrencies = defaultStripeCurrenciesJSON

// StripeEstimateMargin marks the wallet page's estimate up from the mid-market
// reference rate. Stripe's conversion markup is paid by the buyer (documented
// 2-4%), so quoting bare mid-market reads cheaper than the checkout page will.
// Defaults to the top of that band so the amount at Stripe is the same or lower
// than quoted, never higher.
var StripeEstimateMargin = 0.04
