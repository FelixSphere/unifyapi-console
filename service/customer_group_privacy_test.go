/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
 * A customer's pricing group is that customer's identity.
 *
 * Found on production 2026-09-22: the playground's Model Group picker listed
 * every customer by name to every user. Provisioning added each customer group
 * to UserUsableGroups -- upstream's list of groups ANY user may select -- so
 * GetPricing published the whole list, including each group's ratio, on a route
 * that needs no authentication at all. It was not cosmetic: an ordinary user in
 * `default` created an API token bound to another customer's group and the
 * server accepted it, which is the group the relay prices the request with.
 *
 * These tests seed the group INTO UserUsableGroups first, because that is the
 * state production is in and because an operator can still put it back by hand.
 * The guard has to hold against the data, not just against the writer.
 */
package service

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const (
	customerGroup = "Builder_hub_2026_Sep_Batch_UnifyAPI-2"
	tierGroup     = "Vip User"
)

func setupCustomerGroupPrivacyTest(t *testing.T) {
	t.Helper()

	originalDB := model.DB
	originalUsable := setting.UserUsableGroups2JSONString()
	originalRatios := ratio_setting.GroupRatio2JSONString()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.PartnershipProgram{}, &model.PartnershipCustomer{}))
	model.DB = db

	// The customer exists in the registry -- the authoritative record of which
	// group belongs to whom.
	require.NoError(t, db.Create(&model.PartnershipCustomer{
		ProgramId: 1, Name: "UnifyAPI-2", Code: "bh26u2", Group: customerGroup,
	}).Error)
	model.InvalidateCustomerOwnedGroupsCache()

	// Production's state: the customer group sits in the user-selectable list.
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(
		`{"default":"default","`+tierGroup+`":"","`+customerGroup+`":"`+customerGroup+`"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(
		`{"default":0.9,"`+tierGroup+`":1,"`+customerGroup+`":0.5}`))

	t.Cleanup(func() {
		model.DB = originalDB
		model.InvalidateCustomerOwnedGroupsCache()
		_ = setting.UpdateUserUsableGroupsByJSONString(originalUsable)
		_ = ratio_setting.UpdateGroupRatioByJSONString(originalRatios)
	})
}

func TestAnotherCustomersGroupIsNeverOfferedToAUser(t *testing.T) {
	setupCustomerGroupPrivacyTest(t)

	usable := GetUserUsableGroups("default")
	assert.NotContains(t, usable, customerGroup,
		"a customer's name must not be listed to an unrelated user")
	assert.Contains(t, usable, "default", "the user's own group stays")
	assert.Contains(t, usable, tierGroup,
		"a tier that belongs to no customer is unaffected")
}

func TestAnotherCustomersGroupCannotBeSelected(t *testing.T) {
	setupCustomerGroupPrivacyTest(t)

	// This is the check token creation (controller/token.go) and the playground
	// (middleware/distributor.go) both go through. It returning true is what
	// let an ordinary user hold a key billed at another customer's rate.
	assert.False(t, IsUserSelectableGroup("default", customerGroup),
		"an unrelated user must not be able to bill under a customer's group")
	assert.True(t, IsUserSelectableGroup("default", tierGroup),
		"an ordinary tier must stay selectable")
}

func TestACustomersOwnMembersKeepTheirGroup(t *testing.T) {
	setupCustomerGroupPrivacyTest(t)

	// The filter must not lock a customer out of their own pricing.
	usable := GetUserUsableGroups(customerGroup)
	assert.Contains(t, usable, customerGroup,
		"the customer's own members must keep their group")
	assert.True(t, IsUserSelectableGroup(customerGroup, customerGroup))
}

func TestAnAnonymousCallerIsOfferedNoCustomerAtAll(t *testing.T) {
	setupCustomerGroupPrivacyTest(t)

	// GetPricing passes "" for a caller with no session, and publishes the
	// result on an unauthenticated route.
	usable := GetUserUsableGroups("")
	assert.NotContains(t, usable, customerGroup,
		"the public pricing route must not enumerate customers")
	assert.Contains(t, usable, "default")
}
