package model

// UNIFYAPI-FORK: the shared FelixSphere Stripe account delivers other
// products' checkout events to the console webhook; see
// controller/stripe_product_scope.go.

// StripeOrderExists reports whether tradeNo is a console top-up or
// subscription order, whatever its status or gateway. Unlike
// GetTopUpByTradeNo it returns lookup failures as errors, so a database
// outage is never mistaken for "not one of ours".
func StripeOrderExists(tradeNo string) (bool, error) {
	var count int64
	if err := DB.Model(&TopUp{}).Where("trade_no = ?", tradeNo).Count(&count).Error; err != nil {
		return false, err
	}
	if count > 0 {
		return true, nil
	}
	if err := DB.Model(&SubscriptionOrder{}).Where("trade_no = ?", tradeNo).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}
