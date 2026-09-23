/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
 * What a login may BE BILLED UNDER is narrower than what it may see or route
 * through, and conflating the three is what let a customer undercut their own
 * contract.
 *
 * `default` has to stay in UserUsableGroups -- an empty allowlist makes the
 * public catalogue return no models at all -- but it carries the new-customer
 * discount: 0.9 on production on 2026-09-22, while every customer group was
 * 1.0. So anything that treated "is in UserUsableGroups" as "may be billed
 * under" let any customer point a token at `default` and pay 10% less.
 *
 * Routing is deliberately NOT narrowed. An auto walk picks a CHANNEL; pricing
 * stays on the login's own group because auth pins ContextKeyUsingGroup -- see
 * middleware/unifyapi_default_user_auto_routing_test.go.
 */
package service

import (
	"testing"

	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupBillableGroupTest(t *testing.T) {
	t.Helper()
	originalUsable := setting.UserUsableGroups2JSONString()
	originalRatios := ratio_setting.GroupRatio2JSONString()
	originalSpecial := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.ReadAll()

	// Production's shape: `default` is the only entry left in the allowlist,
	// and it is the cheap one.
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":0.9,"Kingdee":1}`))
	ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.Clear()

	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsable))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalRatios))
		special := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup
		special.Clear()
		special.AddAll(originalSpecial)
	})
}

func TestACustomerCannotBeBilledUnderTheCheaperDefaultGroup(t *testing.T) {
	setupBillableGroupTest(t)

	assert.False(t, IsUserBillableGroup("Kingdee", "default"),
		"a customer must not be able to bill under the new-customer discount")
	assert.True(t, IsUserBillableGroup("Kingdee", "Kingdee"),
		"a login always bills under its own group")

	// The catalogue still shows `default`, which is why it stays in the
	// allowlist and why the two questions had to be separated.
	assert.Contains(t, GetUserUsableGroups("Kingdee"), "default",
		"the pricing page must keep rendering")
}

func TestAnOperatorCanStillGrantAnotherGroupExplicitly(t *testing.T) {
	setupBillableGroupTest(t)

	assert.False(t, IsUserBillableGroup("Kingdee", "default"))
	ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.Set(
		"Kingdee", map[string]string{"+:default": ""})
	assert.True(t, IsUserBillableGroup("Kingdee", "default"),
		`"+:" is how an operator grants an exception, and it must still work`)
}

func TestRoutingIsNotNarrowedByTheBillingRule(t *testing.T) {
	setupBillableGroupTest(t)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","Kingdee":""}`))

	// An auto walk chooses a channel, never a price, so it keeps the broader
	// set. Narrowing it would have cut a `default` user off from models their
	// own group has no channel for, with no pricing benefit at all.
	assert.True(t, IsUserSelectableGroup("default", "Kingdee"),
		"routing must not be narrowed by a billing rule")
	assert.False(t, IsUserBillableGroup("default", "Kingdee"),
		"...while billing still is")
}

func TestNoSessionCanBeBilledUnderAnything(t *testing.T) {
	setupBillableGroupTest(t)
	assert.Empty(t, GetUserBillableGroups(""))
	assert.False(t, IsUserBillableGroup("", "default"))
}
