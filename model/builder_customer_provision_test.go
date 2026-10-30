/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupGroupRatioProvisionTest(t *testing.T) {
	t.Helper()
	setupPartnershipTestDB(t)
	// An in-memory SQLite database is per-connection, and the pricing snapshot
	// is written on the global handle while a transaction holds another. Pin
	// the pool to one connection so both see the same schema.
	sqlDB, err := DB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, DB.AutoMigrate(&PricingConfigHistory{}))
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
		t.Cleanup(func() { common.OptionMap = nil })
	}
	// The snapshot reads the previous value from the option cache, which a
	// running server keeps populated. Seed it so the test exercises an
	// overwrite rather than a first write.
	var option Option
	require.NoError(t, DB.Where("key = ?", "GroupRatio").First(&option).Error)
	common.OptionMapRWMutex.Lock()
	common.OptionMap["GroupRatio"] = option.Value
	common.OptionMapRWMutex.Unlock()
}

func currentGroupRatios(t *testing.T) map[string]float64 {
	t.Helper()
	var option Option
	require.NoError(t, DB.Where("key = ?", "GroupRatio").First(&option).Error)
	groups := map[string]float64{}
	require.NoError(t, common.Unmarshal([]byte(option.Value), &groups))
	return groups
}

// The property that matters most in this file. Group Pricing replaces rather
// than merges on save, and a save through a careless path once left production
// holding thousands of keys. Adding one team must leave every other entry
// exactly as it was.
func TestProvisioningATeamPreservesEveryExistingPrice(t *testing.T) {
	setupGroupRatioProvisionTest(t)
	before := currentGroupRatios(t)
	require.NotEmpty(t, before, "the fixture must start with prices to preserve")

	require.NoError(t, EnsurePartnershipGroupRatio("Nusa Labs"))

	after := currentGroupRatios(t)
	for group, ratio := range before {
		assert.InDelta(t, ratio, after[group], 1e-9, "existing group %q was altered or dropped", group)
	}
	assert.Len(t, after, len(before)+1, "exactly one group added")
	assert.InDelta(t, 0.9, after["Nusa Labs"], 1e-9, "a new team starts at 90% of the published price -- every new customer does")
}

// Reconnecting must never reprice a team. A member joining a discounted team
// would otherwise quietly restore it to list price.
func TestReconnectingNeverRepricesAnExistingTeam(t *testing.T) {
	setupGroupRatioProvisionTest(t)
	require.NoError(t, EnsurePartnershipGroupRatio("Nusa Labs"))

	// Somebody negotiates a discount.
	groups := currentGroupRatios(t)
	groups["Nusa Labs"] = 0.7
	encoded, err := common.Marshal(groups)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", "GroupRatio").
		Update("value", string(encoded)).Error)

	// A second member of the same team connects.
	require.NoError(t, EnsurePartnershipGroupRatio("Nusa Labs"))

	assert.InDelta(t, 0.7, currentGroupRatios(t)["Nusa Labs"], 1e-9,
		"a negotiated discount must survive another member connecting")
}

// Several teams provisioned in turn all survive; each write merges into the
// last rather than starting from the fixture.
func TestProvisioningSeveralTeamsKeepsThemAll(t *testing.T) {
	setupGroupRatioProvisionTest(t)
	before := len(currentGroupRatios(t))
	for _, team := range []string{"Nusa Labs", "Acme Robotics", "Vidora"} {
		require.NoError(t, EnsurePartnershipGroupRatio(team))
	}
	after := currentGroupRatios(t)
	assert.Len(t, after, before+3)
	for _, team := range []string{"Nusa Labs", "Acme Robotics", "Vidora"} {
		assert.Contains(t, after, team)
	}
}

// Overwriting Group Pricing keeps the same audit trail the admin path keeps,
// so an automatic write is as recoverable as a manual one.
func TestProvisioningRecordsTheOverwrittenPricingForAudit(t *testing.T) {
	setupGroupRatioProvisionTest(t)
	require.NoError(t, EnsurePartnershipGroupRatio("Nusa Labs"))

	var history []PricingConfigHistory
	require.NoError(t, DB.Where("key = ?", "GroupRatio").Find(&history).Error)
	require.NotEmpty(t, history, "the previous pricing must be snapshotted before it is replaced")
	assert.Equal(t, "builder-bridge", history[len(history)-1].ChangedBy)
}

// Codes are pattern-constrained while team names are free text, so the
// projection has to produce something valid from anything a product is called.
func TestARegistrationCodeIsDerivedFromAnyTeamName(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"Acme Robotics", "acme-robotics"},
		{"  NusaPay  ", "nusapay"},
		{"Orii/Kroolo", "oriikroolo"},
		{"A", "team-a"},
		{"投资", "team"},
	} {
		code := partnershipCodeFromName(tc.name)
		assert.Equal(t, tc.want, code, "name %q", tc.name)
		assert.True(t, partnershipCodePattern.MatchString(code),
			"derived code %q from %q must satisfy the registration pattern", code, tc.name)
	}
}

// A pricing group is defined by three settings. Writing only the billing ratio
// leaves a half-registered group: it bills, but it has no top-up ratio, is not
// user-selectable, and never appears in the Customer model prices editor -- so
// nobody can ever price a model for that team. That is what shipped, and this
// is what it should have been.
func TestAProvisionedGroupIsRegisteredEverywhereAGroupHasToBe(t *testing.T) {
	setupGroupRatioProvisionTest(t)
	require.NoError(t, EnsurePartnershipGroupRatio("Nusa Labs"))

	read := func(key string) map[string]any {
		var option Option
		require.NoError(t, DB.Where("key = ?", key).First(&option).Error)
		out := map[string]any{}
		require.NoError(t, common.Unmarshal([]byte(option.Value), &out))
		return out
	}

	assert.Contains(t, read("GroupRatio"), "Nusa Labs", "billing ratio")
	assert.InDelta(t, 0.9, read("GroupRatio")["Nusa Labs"], 1e-9, "billed at 90% of the published price")
	assert.Contains(t, read("TopupGroupRatio"), "Nusa Labs", "top-up ratio -- shows as 'Not set' without this")
	assert.InDelta(t, 1, read("TopupGroupRatio")["Nusa Labs"], 1e-9, "the discount is on the bill, never on what a payment buys")

	// NOT in UserUsableGroups, which is the list of groups ANY user may pick.
	// It used to be added here, "so the operator can see it in the pricing
	// editor" -- but that editor lists GroupRatio union UserUsableGroups union
	// TopupGroupRatio, so the two assertions above already put it on screen,
	// while the entry published the customer's name and terms to every other
	// user and let them bill under it.
	var usable Option
	if err := DB.Where("key = ?", "UserUsableGroups").First(&usable).Error; err == nil {
		out := map[string]any{}
		require.NoError(t, common.Unmarshal([]byte(usable.Value), &out))
		assert.NotContains(t, out, "Nusa Labs",
			"a customer group must never be user-selectable")
	}
}

// The same merge hazard applies to all three maps: each replaces rather than
// merges on save, so provisioning a second team must not drop the first.
func TestProvisioningASecondTeamKeepsTheFirstInEverySetting(t *testing.T) {
	setupGroupRatioProvisionTest(t)
	require.NoError(t, EnsurePartnershipGroupRatio("Nusa Labs"))
	require.NoError(t, EnsurePartnershipGroupRatio("Acme Robotics"))

	for _, key := range []string{"GroupRatio", "TopupGroupRatio"} {
		var option Option
		require.NoError(t, DB.Where("key = ?", key).First(&option).Error)
		out := map[string]any{}
		require.NoError(t, common.Unmarshal([]byte(option.Value), &out))
		assert.Contains(t, out, "Nusa Labs", "%s lost the first team", key)
		assert.Contains(t, out, "Acme Robotics", "%s missing the second team", key)
	}
}

// An identity bound to a program that no longer exists is dangling, not a
// conflict. Refusing it locked the person out permanently: they cannot reach
// the deleted program, and the address they would reconnect with is already
// held by the very account that binding belongs to. Only an administrator
// could rescue them -- which is how this reached production.
//
// A live different program is still refused, because moving somebody between
// programs moves their usage onto another invoice.
func TestABindingToADeletedProgramHealsItself(t *testing.T) {
	setupGroupRatioProvisionTest(t)
	require.NoError(t, DB.AutoMigrate(&User{}, &Tenant{}, &BuilderIdentity{}, &Token{}))

	program := PartnershipProgram{Name: "Current Program", Code: "current", Group: "partner", Enabled: true}
	require.NoError(t, CreatePartnershipProgram(&program))
	var customer PartnershipCustomer
	require.NoError(t, DB.Where("program_id = ? AND is_default = ?", program.Id, true).First(&customer).Error)

	user := User{Username: "builder_stale", Email: "stale@example.invalid", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "orphaned"}
	require.NoError(t, DB.Create(&user).Error)
	// Bound to a program id that is not in the table at all.
	const deletedProgramId = 987654
	require.NoError(t, DB.Create(&BuilderIdentity{
		Subject: "stale-subject", UserId: user.Id,
		ProgramId: deletedProgramId, CustomerId: 999, TokenId: 1,
	}).Error)

	link, err := ConnectBuilderIdentityWithProgram("stale-subject", "stale@example.invalid",
		BuilderProgramSelector{ProgramName: program.Name}, "")
	require.NoError(t, err, "a dangling binding must not lock the account out")
	require.NotNil(t, link)

	var healed BuilderIdentity
	require.NoError(t, DB.Where("subject = ?", "stale-subject").First(&healed).Error)
	assert.Equal(t, program.Id, healed.ProgramId, "rebound to the program that resolves now")
	assert.Equal(t, customer.Id, healed.CustomerId)

	var moved User
	require.NoError(t, DB.First(&moved, user.Id).Error)
	assert.Equal(t, customer.Group, moved.Group, "the member's pricing group follows the rebinding")

	var enrollment PartnershipEnrollment
	require.NoError(t, DB.Where("program_id = ? AND user_id = ?", program.Id, user.Id).First(&enrollment).Error)
	assert.Equal(t, customer.Id, enrollment.CustomerId, "and the enrollment is repaired too")
}

// The guard that matters is kept: a binding to a program that still exists is
// a real conflict and must not be silently repointed.
func TestABindingToALiveOtherProgramIsStillRefused(t *testing.T) {
	setupGroupRatioProvisionTest(t)
	require.NoError(t, DB.AutoMigrate(&User{}, &Tenant{}, &BuilderIdentity{}, &Token{}))

	other := PartnershipProgram{Name: "Other Program", Code: "other", Group: "partner", Enabled: true}
	require.NoError(t, CreatePartnershipProgram(&other))
	require.NoError(t, EnsurePartnershipGroupRatio("second"))
	current := PartnershipProgram{Name: "Current Program", Code: "current", Group: "second", Enabled: true}
	require.NoError(t, CreatePartnershipProgram(&current))

	user := User{Username: "builder_live", Email: "live@example.invalid", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, DB.Create(&BuilderIdentity{
		Subject: "live-subject", UserId: user.Id,
		ProgramId: other.Id, CustomerId: 1, TokenId: 1,
	}).Error)

	_, err := ConnectBuilderIdentityWithProgram("live-subject", "live@example.invalid",
		BuilderProgramSelector{ProgramName: current.Name}, "")
	assert.ErrorIs(t, err, ErrPartnershipProgramUnavailable,
		"moving somebody between live programs moves their usage to another invoice")
}

// Removing a program is a soft delete: the row stays, so past invoices remain
// attributable. That makes it a different shape from the hard-deleted case
// above, and the account left behind must still heal rather than be stranded
// pointing at a program the operator can no longer see.
func TestABindingToARemovedProgramHealsItself(t *testing.T) {
	setupGroupRatioProvisionTest(t)
	require.NoError(t, DB.AutoMigrate(&User{}, &Tenant{}, &BuilderIdentity{}, &Token{}))

	retired := PartnershipProgram{Name: "Retired Program", Code: "retired", Group: "partner", Enabled: true}
	require.NoError(t, CreatePartnershipProgram(&retired))
	replacement := PartnershipProgram{Name: "Current Program", Code: "current", Group: "vip", Enabled: true}
	require.NoError(t, CreatePartnershipProgram(&replacement))
	var customer PartnershipCustomer
	require.NoError(t, DB.Where("program_id = ? AND is_default = ?", replacement.Id, true).First(&customer).Error)

	user := User{Username: "builder_retired", Email: "retired@example.invalid",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "partner"}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, DB.Create(&BuilderIdentity{
		Subject: "retired-subject", UserId: user.Id,
		ProgramId: retired.Id, CustomerId: 999, TokenId: 1,
	}).Error)

	_, err := DeletePartnershipProgram(retired.Id)
	require.NoError(t, err)

	var stillThere PartnershipProgram
	require.NoError(t, DB.First(&stillThere, retired.Id).Error,
		"the removed program's row must still exist, or this is not testing a soft delete")

	link, err := ConnectBuilderIdentityWithProgram("retired-subject", "retired@example.invalid",
		BuilderProgramSelector{ProgramName: replacement.Name}, "")
	require.NoError(t, err, "a binding to a removed program must not lock the account out")
	require.NotNil(t, link)

	var healed BuilderIdentity
	require.NoError(t, DB.Where("subject = ?", "retired-subject").First(&healed).Error)
	assert.Equal(t, replacement.Id, healed.ProgramId, "rebound to the program that resolves now")
	assert.Equal(t, customer.Id, healed.CustomerId)

	var moved User
	require.NoError(t, DB.First(&moved, user.Id).Error)
	assert.Equal(t, customer.Group, moved.Group, "the member's pricing group follows the rebinding")
}
