package service

import relaycommon "github.com/QuantumNous/new-api/relay/common"

// CustomerGroupForLog keeps the company identity separate from the group used
// to route a request. Reconciliation and customer statements read logs.group,
// so that column must always be the immutable request-time User Group. The
// selected route remains available in Other for operational diagnosis.
func CustomerGroupForLog(info *relaycommon.RelayInfo, other map[string]interface{}) string {
	if info == nil {
		return ""
	}
	customerGroup := info.UserGroup
	if customerGroup == "" {
		customerGroup = info.UsingGroup
	}
	if other != nil && info.UsingGroup != "" && info.UsingGroup != customerGroup {
		other["routing_group"] = info.UsingGroup
	}
	return customerGroup
}

// CustomerChargeQuota returns the cash-billable amount. Promotional requests
// retain their gross contract-price usage in metadata while logs.quota stays
// zero, so customer invoices cannot accidentally charge free credits.
func CustomerChargeQuota(info *relaycommon.RelayInfo, grossQuota int, other map[string]interface{}) int {
	if info == nil || info.BillingSource != BillingSourcePromotional {
		return grossQuota
	}
	if other != nil {
		other["promotional"] = true
		other["promotional_quota"] = grossQuota
		other["credit_pool_id"] = info.CreditPoolId
		other["credit_grant_id"] = info.CreditGrantId
		other["credit_reservation_id"] = info.CreditReservationId
	}
	return 0
}
