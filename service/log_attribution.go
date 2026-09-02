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
