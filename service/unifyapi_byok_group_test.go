/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package service

import (
	"sort"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Production shape: the Auto list is made of groups that carry our channels,
// and every login may walk it. A BYOK login brought its own provider key on
// the understanding that its requests go to that key, so neither the walk nor
// a token pointed at one of our groups may take it onto our suppliers.
func configureByokGroupTest(t *testing.T) {
	t.Helper()
	originalAuto := setting.AutoGroups2JsonString()
	originalUsable := setting.UserUsableGroups2JSONString()
	originalRatios := ratio_setting.GroupRatio2JSONString()
	originalSpecial := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.ReadAll()
	ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.Clear()
	t.Cleanup(func() {
		special := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup
		special.Clear()
		special.AddAll(originalSpecial)
	})
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["GenAI","default"]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"GenAI":"","default":""}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"GenAI":1,"default":0.85,"byok-acme":0.05,"byok-other":0.05}`))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(originalAuto))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsable))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalRatios))
	})
}

func TestAByokLoginsAutoWalkNeverLeavesItsOwnGroup(t *testing.T) {
	configureByokGroupTest(t)

	require.Equal(t, []string{"GenAI", "default"}, GetUserAutoGroup("default"),
		"control: an ordinary login walks the whole Auto list")
	assert.Empty(t, GetUserAutoGroup("byok-acme"),
		"a BYOK login inheriting the global Auto list is given none of it")

	// A token that carries its own Auto list is filtered the same way.
	ctx := newRequestAutoGroupsContext()
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"GenAI", "byok-acme", "default", "byok-other"})
	assert.Equal(t, []string{"byok-acme"}, GetRequestAutoGroups(ctx, "byok-acme"),
		"only the login's own group survives, never our groups and never another BYOK customer's")

	assert.False(t, IsUserSelectableGroup("byok-acme", "default"))
	assert.False(t, IsUserSelectableGroup("byok-acme", "byok-other"))
	assert.True(t, IsUserSelectableGroup("byok-acme", "byok-acme"))
}

// Token auth admits a token's group when it is in the login's usable groups.
// For a BYOK login that list is its own group, so a token pointed at one of
// our groups is refused there instead of being served from our suppliers.
func TestAByokLoginsUsableGroupsAreItsOwn(t *testing.T) {
	configureByokGroupTest(t)

	require.Contains(t, GetUserUsableGroups("Kingdee"), "GenAI",
		"control: an ordinary login is offered the general list")
	assert.Equal(t, []string{"byok-acme"}, keysOf(GetUserUsableGroups("byok-acme")))
	assert.Equal(t, []string{"BYOK-Acme"}, keysOf(GetUserUsableGroups("BYOK-Acme")),
		"typed in capitals, it is the same rule")
}

// The operator's escape hatch is unchanged: an explicit "+:" grant is how a
// BYOK customer is also sold our supply, at that group's own price.
func TestAnOperatorCanGrantAByokLoginAnotherGroupExplicitly(t *testing.T) {
	configureByokGroupTest(t)
	ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.Set(
		"byok-acme", map[string]string{"+:GenAI": ""})

	assert.Equal(t, []string{"GenAI", "byok-acme"}, keysOf(GetUserUsableGroups("byok-acme")))
	assert.Equal(t, []string{"GenAI"}, GetUserAutoGroup("byok-acme"),
		"and the Auto walk follows the grant, no further")
}

func keysOf(groups map[string]string) []string {
	keys := make([]string, 0, len(groups))
	for group := range groups {
		keys = append(keys, group)
	}
	sort.Strings(keys)
	return keys
}

// An operator can put any name into UserUsableGroups by hand. A BYOK group
// that got there must still not be offered to, or walkable by, anybody else.
func TestAByokGroupIsNeverOfferedToAnotherLogin(t *testing.T) {
	configureByokGroupTest(t)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"GenAI":"","default":"","byok-acme":"Acme"}`))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["byok-acme","GenAI","default"]`))

	assert.NotContains(t, GetUserUsableGroups("default"), "byok-acme")
	assert.NotContains(t, GetUserUsableGroups("byok-other"), "byok-acme")
	assert.Contains(t, GetUserUsableGroups("byok-acme"), "byok-acme", "its own login keeps it")

	assert.Equal(t, []string{"GenAI", "default"}, GetUserAutoGroup("default"),
		"an ordinary login's Auto walk skips the BYOK group even when the Auto list names it")
	assert.Equal(t, []string{"byok-acme"}, GetUserAutoGroup("byok-acme"))
	assert.False(t, IsUserBillableGroup("default", "byok-acme"))
}
