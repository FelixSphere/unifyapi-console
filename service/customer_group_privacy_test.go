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

// The registry is a database read, and a database read can fail while the
// table is still there -- a lock timeout, a permission change, a migration in
// flight. When that happens the only safe answer is to show nothing: the first
// call of a fresh process serves the first /api/pricing after a release, on a
// route that needs no authentication. Before this, an unreadable registry read
// as "no customers exist" and published every one of them.
//
// Simulated by renaming the column out from under the query, so HasTable still
// succeeds and the read fails -- which is the shape of the real failure. A
// missing TABLE is deliberately NOT this case: that genuinely means nobody has
// been provisioned, and treating it as unknown hides every ordinary tier.
func TestAnUnreadableCustomerRegistryHidesGroupsRatherThanPublishingThem(t *testing.T) {
	setupCustomerGroupPrivacyTest(t)

	require.NoError(t, model.DB.Exec(`ALTER TABLE partnership_customers RENAME COLUMN "group" TO group_moved`).Error)
	model.InvalidateCustomerOwnedGroupsCache()

	set, known := model.CustomerOwnedGroups()
	require.False(t, known, "the registry must report that it could not be read")
	require.Nil(t, set)

	usable := GetUserUsableGroups("default")
	assert.NotContains(t, usable, customerGroup,
		"an unreadable registry must not publish a customer's name")
	assert.Contains(t, usable, "default",
		"the caller's own group is restored by the fallback and must survive")
	assert.False(t, IsUserSelectableGroup("default", customerGroup),
		"nor may it be billed under")
}

// The counterpart: no registry table at all is a KNOWN answer -- nobody has
// been provisioned -- and must not hide ordinary tiers.
func TestAMissingRegistryTableIsNotTreatedAsAFailure(t *testing.T) {
	setupCustomerGroupPrivacyTest(t)

	require.NoError(t, model.DB.Migrator().DropTable(&model.PartnershipCustomer{}))
	model.InvalidateCustomerOwnedGroupsCache()

	set, known := model.CustomerOwnedGroups()
	assert.True(t, known, "an install with no customers knows it has none")
	assert.Empty(t, set)
	assert.Contains(t, GetUserUsableGroups("default"), tierGroup,
		"an ordinary tier must stay visible when no customer registry exists")
}

// Selection is gated on visibility, and that is the property the whole fix
// rests on: if a group can be billed under without being listed, every filter
// above it is decoration. Pinned against the real state -- customer group
// seeded into UserUsableGroups, which is where production has it.
func TestNothingIsSelectableThatIsNotAlsoVisible(t *testing.T) {
	setupCustomerGroupPrivacyTest(t)

	for _, viewer := range []string{"", "default", tierGroup, customerGroup} {
		visible := GetUserUsableGroups(viewer)
		for _, candidate := range []string{"default", tierGroup, customerGroup, "Chinhin"} {
			if !IsUserSelectableGroup(viewer, candidate) {
				continue
			}
			_, listed := visible[candidate]
			assert.True(t, listed,
				"viewer %q may select %q without being shown it", viewer, candidate)
		}
	}
}

// A group nobody has registered as a customer is NOT protected by the registry
// -- it is protected only by what the operator left in UserUsableGroups. This
// test states that boundary rather than implying the fix covers more than it
// does: Chinhin, GenAI and UnifyAI are customer names that reached the option
// by hand, so they carry no registry row and the data has to be cleaned.
func TestAnUnregisteredGroupIsGovernedOnlyByTheOptionItIsIn(t *testing.T) {
	setupCustomerGroupPrivacyTest(t)

	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(
		`{"default":"default","HandAddedCustomer":""}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(
		`{"default":0.9,"HandAddedCustomer":0.5}`))

	assert.Contains(t, GetUserUsableGroups("default"), "HandAddedCustomer",
		"no registry row means the code cannot know this is a customer -- "+
			"removing it from UserUsableGroups is the only thing that hides it")
}
