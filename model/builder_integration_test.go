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

func TestBuilderRegistrationDefersGrantAndReusesIdentity(t *testing.T) {
	setupPartnershipTestDB(t)
	require.NoError(t, DB.AutoMigrate(&BuilderIdentity{}, &Token{}))
	require.NoError(t, CreatePartnershipProgram(&PartnershipProgram{Name: "Builders", Code: "builders", Group: "partner", GrantQuota: 5000000, GrantLimit: 2, Enabled: true}))
	first, err := ConnectBuilderIdentity("subject-one", "builder@example.invalid", "builders", "")
	require.NoError(t, err)
	second, err := ConnectBuilderIdentity("subject-one", "builder@example.invalid", "builders", "")
	require.NoError(t, err)
	assert.Equal(t, first.Id, second.Id)
	_, user, err := GetBuilderIdentity("subject-one")
	require.NoError(t, err)
	assert.Equal(t, "partner", user.Group)
	assert.NotZero(t, user.TenantId)
	quota, err := GetUserQuota(user.Id, true)
	require.NoError(t, err)
	assert.Zero(t, quota)
	var count int64
	require.NoError(t, DB.Model(&Token{}).Where("user_id = ?", user.Id).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	program, err := GetPartnershipProgramByCode("builders")
	require.NoError(t, err)
	assert.Zero(t, program.ClaimedCount)
}

func TestBuilderCannotLinkAnExistingEmailWithoutAccountProof(t *testing.T) {
	setupPartnershipTestDB(t)
	require.NoError(t, DB.AutoMigrate(&BuilderIdentity{}, &Token{}))
	require.NoError(t, CreatePartnershipProgram(&PartnershipProgram{Name: "Builders", Code: "builders", Group: "partner", Enabled: true}))
	existing := User{Username: "existing", Email: "existing@example.invalid", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, DB.Create(&existing).Error)
	_, err := ConnectBuilderIdentity("attacker-subject", existing.Email, "builders", "")
	require.ErrorIs(t, err, ErrBuilderLinkRequired)
	_, err = ConnectBuilderIdentity("attacker-subject", existing.Email, "builders", "invalid")
	require.ErrorIs(t, err, ErrBuilderUnavailable)
	var count int64
	require.NoError(t, DB.Model(&BuilderIdentity{}).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, DB.First(&existing, existing.Id).Error)
	assert.Equal(t, "default", existing.Group)
}

func TestBuilderExistingAccountKeepsGroupAndBalanceAndRequiresMatchingProof(t *testing.T) {
	setupPartnershipTestDB(t)
	require.NoError(t, DB.AutoMigrate(&BuilderIdentity{}, &Token{}))
	require.NoError(t, CreatePartnershipProgram(&PartnershipProgram{Name: "Builders", Code: "builders", Group: "partner", Enabled: true}))
	proof := "management-test-credential"
	existing := User{Username: "existing", Email: "existing@example.invalid", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", Quota: 12345, AccessToken: &proof}
	require.NoError(t, DB.Create(&existing).Error)
	_, err := ConnectBuilderIdentity("wrong-email", "someone@example.invalid", "builders", proof)
	require.ErrorIs(t, err, ErrBuilderUnavailable)
	link, err := ConnectBuilderIdentity("existing-subject", existing.Email, "builders", proof)
	require.NoError(t, err)
	assert.Equal(t, existing.Id, link.UserId)
	require.NoError(t, DB.First(&existing, existing.Id).Error)
	assert.Equal(t, "default", existing.Group)
	assert.Equal(t, 12345, existing.Quota)
	require.NoError(t, DB.Model(&existing).Update("status", common.UserStatusDisabled).Error)
	_, _, err = GetBuilderIdentity("existing-subject")
	require.ErrorIs(t, err, ErrBuilderUnavailable)
}

func TestBuilderWorkspaceProjectionDoesNotExposeCredentialsOrOtherUsers(t *testing.T) {
	setupPartnershipTestDB(t)
	require.NoError(t, DB.AutoMigrate(&BuilderIdentity{}, &Token{}, &TopUp{}, &Ability{}))
	oldLogDB := LOG_DB
	LOG_DB = DB
	t.Cleanup(func() { LOG_DB = oldLogDB })
	require.NoError(t, CreatePartnershipProgram(&PartnershipProgram{Name: "Builders", Code: "builders", Group: "partner", Enabled: true}))
	link, err := ConnectBuilderIdentity("reader", "reader@example.invalid", "builders", "")
	require.NoError(t, err)
	_, user, err := GetBuilderIdentity("reader")
	require.NoError(t, err)
	require.NoError(t, DB.Create(&Log{UserId: user.Id, Type: LogTypeConsume, CreatedAt: 2000000000, ModelName: "visible", Quota: 500, PromptTokens: 7, CompletionTokens: 2}).Error)
	require.NoError(t, DB.Create(&Log{UserId: user.Id + 1, Type: LogTypeConsume, CreatedAt: 2000000000, ModelName: "other-user-private", Content: "secret-provider-data"}).Error)
	data, err := ReadBuilderWorkspace(link, user, "30d")
	require.NoError(t, err)
	encoded, err := common.Marshal(data)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), "visible")
	assert.NotContains(t, string(encoded), "other-user-private")
	assert.NotContains(t, string(encoded), "secret-provider-data")
	token, err := GetTokenById(link.TokenId)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), token.Key)
	assert.NotContains(t, string(encoded), "access_token")
	assert.NotNil(t, data["models"])
}

func TestBuilderTeamGrantConcurrentClaimsCreditOwnerOnce(t *testing.T) {
	setupPartnershipTestDB(t)
	require.NoError(t, DB.AutoMigrate(&BuilderIdentity{}, &Token{}))
	sqlDB, err := DB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, CreatePartnershipProgram(&PartnershipProgram{Name: "Builders", Code: "builders", Group: "partner", GrantQuota: common.QuotaFromFloat(10 * common.QuotaPerUnit), GrantLimit: 1, Enabled: true}))
	link, err := ConnectBuilderIdentity("team-owner", "owner@example.invalid", "builders", "")
	require.NoError(t, err)
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() { results <- ClaimBuilderTeamGrant("team-owner", "builders") }()
	}
	for i := 0; i < 8; i++ {
		require.NoError(t, <-results)
	}
	quota, err := GetUserQuota(link.UserId, true)
	require.NoError(t, err)
	assert.Equal(t, common.QuotaFromFloat(10*common.QuotaPerUnit), quota)
	stored, _, err := GetBuilderIdentity("team-owner")
	require.NoError(t, err)
	assert.Positive(t, stored.GrantClaimedAt)
	claimed, err := BuilderGrantStatus(stored)
	require.NoError(t, err)
	assert.True(t, claimed)
	program, err := GetPartnershipProgramByCode("builders")
	require.NoError(t, err)
	assert.Equal(t, 1, program.ClaimedCount)
	second, err := ConnectBuilderIdentity("other-owner", "other@example.invalid", "builders", "")
	require.NoError(t, err)
	require.Error(t, ClaimBuilderTeamGrant(second.Subject, "builders"))
	quota, err = GetUserQuota(second.UserId, true)
	require.NoError(t, err)
	assert.Zero(t, quota)
}

func TestBuilderTeamGrantDoesNotStackExistingPartnershipGrant(t *testing.T) {
	setupPartnershipTestDB(t)
	require.NoError(t, DB.AutoMigrate(&BuilderIdentity{}, &Token{}))
	require.NoError(t, CreatePartnershipProgram(&PartnershipProgram{Name: "Builders", Code: "builders", Group: "partner", GrantQuota: common.QuotaFromFloat(10 * common.QuotaPerUnit), GrantLimit: 2, Enabled: true}))
	link, err := ConnectBuilderIdentity("owner", "owner@example.invalid", "builders", "")
	require.NoError(t, err)
	require.NoError(t, DB.Model(&PartnershipEnrollment{}).Where("user_id = ?", link.UserId).Update("granted_quota", 123).Error)
	require.NoError(t, ClaimBuilderTeamGrant(link.Subject, "builders"))
	quota, err := GetUserQuota(link.UserId, true)
	require.NoError(t, err)
	assert.Zero(t, quota)
	claimed, err := BuilderGrantStatus(link)
	require.NoError(t, err)
	assert.True(t, claimed)
	program, err := GetPartnershipProgramByCode("builders")
	require.NoError(t, err)
	assert.Zero(t, program.ClaimedCount)
}

func TestBuilderTeamGrantRollsBackWhenQuotaWriteFails(t *testing.T) {
	setupPartnershipTestDB(t)
	require.NoError(t, DB.AutoMigrate(&BuilderIdentity{}, &Token{}))
	require.NoError(t, CreatePartnershipProgram(&PartnershipProgram{Name: "Builders", Code: "builders", Group: "partner", GrantQuota: common.QuotaFromFloat(10 * common.QuotaPerUnit), GrantLimit: 1, Enabled: true}))
	link, err := ConnectBuilderIdentity("owner", "owner@example.invalid", "builders", "")
	require.NoError(t, err)
	require.NoError(t, DB.Exec("CREATE TRIGGER fail_grant BEFORE UPDATE OF quota ON tenants BEGIN SELECT RAISE(ABORT, 'injected failure'); END").Error)
	require.Error(t, ClaimBuilderTeamGrant(link.Subject, "builders"))
	program, err := GetPartnershipProgramByCode("builders")
	require.NoError(t, err)
	assert.Zero(t, program.ClaimedCount)
	stored, _, err := GetBuilderIdentity(link.Subject)
	require.NoError(t, err)
	assert.Zero(t, stored.GrantClaimedAt)
	claimed, err := BuilderGrantStatus(stored)
	require.NoError(t, err)
	assert.False(t, claimed)
	require.NoError(t, DB.Exec("DROP TRIGGER fail_grant").Error)
	require.NoError(t, ClaimBuilderTeamGrant(link.Subject, "builders"))
}

func TestBuilderTeamGrantRejectsChangedOfferOrDisabledAccount(t *testing.T) {
	for _, scenario := range []string{"wrong-amount", "wrong-customer", "disabled-account", "disabled-program"} {
		t.Run(scenario, func(t *testing.T) {
			setupPartnershipTestDB(t)
			require.NoError(t, DB.AutoMigrate(&BuilderIdentity{}, &Token{}))
			require.NoError(t, CreatePartnershipProgram(&PartnershipProgram{Name: "Builders", Code: "builders", Group: "partner", GrantQuota: common.QuotaFromFloat(10 * common.QuotaPerUnit), GrantLimit: 2, Enabled: true}))
			link, err := ConnectBuilderIdentity("owner", "owner@example.invalid", "builders", "")
			require.NoError(t, err)
			code := "builders"
			switch scenario {
			case "wrong-amount":
				require.NoError(t, DB.Model(&PartnershipProgram{}).Where("id = ?", link.ProgramId).Update("grant_quota", 1).Error)
			case "wrong-customer":
				require.NoError(t, CreatePartnershipProgram(&PartnershipProgram{Name: "Other", Code: "other", Group: "partner", GrantQuota: common.QuotaFromFloat(10 * common.QuotaPerUnit), GrantLimit: 2, Enabled: true}))
				code = "other"
			case "disabled-account":
				require.NoError(t, DB.Model(&User{}).Where("id = ?", link.UserId).Update("status", common.UserStatusDisabled).Error)
			case "disabled-program":
				require.NoError(t, DB.Model(&PartnershipProgram{}).Where("id = ?", link.ProgramId).Update("enabled", false).Error)
			}
			require.Error(t, ClaimBuilderTeamGrant(link.Subject, code))
			quota, err := GetUserQuota(link.UserId, true)
			require.NoError(t, err)
			assert.Zero(t, quota)
			program, err := GetPartnershipProgramByCode("builders")
			require.NoError(t, err)
			assert.Zero(t, program.ClaimedCount)
		})
	}
}

func TestBuilderProgramNameConnectAndGrant(t *testing.T) {
	setupPartnershipTestDB(t)
	require.NoError(t, DB.AutoMigrate(&BuilderIdentity{}, &Token{}))
	program := PartnershipProgram{Name: "Builder_hub_2026_Sep_Batch", Code: "legacy-builders", Group: "partner", GrantQuota: 5000000, GrantLimit: 1, Enabled: true}
	require.NoError(t, CreatePartnershipProgram(&program))
	require.NoError(t, DB.Create(&PartnershipCustomer{ProgramId: program.Id, Name: "Other customer", Code: "non-default", Group: "default", Enabled: true}).Error)
	selector := BuilderProgramSelector{ProgramName: program.Name}
	link, err := ConnectBuilderIdentityWithProgram("named-owner", "named@example.invalid", selector, "")
	require.NoError(t, err)
	assert.Equal(t, program.Id, link.ProgramId)
	assert.Positive(t, link.CustomerId)
	quota, err := GetUserQuota(link.UserId, true)
	require.NoError(t, err)
	assert.Zero(t, quota)
	reused, err := ConnectBuilderIdentity("named-owner", "named@example.invalid", program.Code, "")
	require.NoError(t, err)
	assert.Equal(t, link.Id, reused.Id)
	require.NoError(t, ClaimBuilderTeamGrantWithProgram(link.Subject, selector))
	require.NoError(t, ClaimBuilderTeamGrantWithProgram(link.Subject, selector))
	quota, err = GetUserQuota(link.UserId, true)
	require.NoError(t, err)
	assert.Equal(t, 5000000, quota)
	require.NoError(t, DB.First(&program, program.Id).Error)
	assert.Equal(t, 1, program.ClaimedCount)
	other := PartnershipProgram{Name: "Other", Code: "other", Group: "partner", GrantQuota: 5000000, GrantLimit: 1, Enabled: true}
	require.NoError(t, CreatePartnershipProgram(&other))
	_, err = ConnectBuilderIdentityWithProgram(link.Subject, "named@example.invalid", BuilderProgramSelector{ProgramName: other.Name}, "")
	require.Error(t, err)
	require.Error(t, ClaimBuilderTeamGrantWithProgram(link.Subject, BuilderProgramSelector{ProgramName: other.Name}))
}

func TestBuilderProgramNameFailsClosed(t *testing.T) {
	for _, scenario := range []string{"missing", "duplicate", "disabled", "future", "expired", "no-default", "disabled-default", "removed-default", "multiple-defaults", "case-mismatch", "code-as-name", "both-selectors"} {
		t.Run(scenario, func(t *testing.T) {
			setupPartnershipTestDB(t)
			require.NoError(t, DB.AutoMigrate(&BuilderIdentity{}, &Token{}))
			program := PartnershipProgram{Name: "Exact Name", Code: "legacy-code", Group: "partner", Enabled: true}
			require.NoError(t, CreatePartnershipProgram(&program))
			selector := BuilderProgramSelector{ProgramName: program.Name}
			switch scenario {
			case "missing":
				selector.ProgramName = "missing"
			case "duplicate":
				require.NoError(t, CreatePartnershipProgram(&PartnershipProgram{Name: program.Name, Code: "duplicate", Group: "partner", Enabled: true}))
			case "disabled":
				require.NoError(t, DB.Model(&program).Update("enabled", false).Error)
			case "future":
				require.NoError(t, DB.Model(&program).Update("starts_at", int64(4102444800)).Error)
			case "expired":
				require.NoError(t, DB.Model(&program).Update("ends_at", 1).Error)
			case "no-default":
				require.NoError(t, DB.Where("program_id = ?", program.Id).Delete(&PartnershipCustomer{}).Error)
			case "disabled-default":
				require.NoError(t, DB.Model(&PartnershipCustomer{}).Where("program_id = ?", program.Id).Update("enabled", false).Error)
			case "removed-default":
				require.NoError(t, DB.Model(&PartnershipCustomer{}).Where("program_id = ?", program.Id).Update("removed_at", 1).Error)
			case "multiple-defaults":
				require.NoError(t, DB.Create(&PartnershipCustomer{ProgramId: program.Id, Name: "Duplicate", Code: "dup-default", Group: "default", IsDefault: true, Enabled: true}).Error)
			case "case-mismatch":
				selector.ProgramName = "exact name"
			case "code-as-name":
				selector.ProgramName = program.Code
			case "both-selectors":
				selector.PartnershipCode = program.Code
			}
			_, err := ConnectBuilderIdentityWithProgram("owner", "owner@example.invalid", selector, "")
			require.ErrorIs(t, err, ErrPartnershipProgramUnavailable)
			var count int64
			require.NoError(t, DB.Model(&BuilderIdentity{}).Count(&count).Error)
			assert.Zero(t, count)
		})
	}
}

func TestBuilderProgramNameDoesNotResolveCustomerCodeCollision(t *testing.T) {
	setupPartnershipTestDB(t)
	first := PartnershipProgram{Name: "target-name", Code: "first-code", Group: "partner", Enabled: true}
	second := PartnershipProgram{Name: "Other", Code: "target-name", Group: "partner", Enabled: true}
	require.NoError(t, CreatePartnershipProgram(&first))
	require.NoError(t, CreatePartnershipProgram(&second))
	offer, err := ResolveBuilderProgram(DB, BuilderProgramSelector{ProgramName: first.Name}, false)
	require.NoError(t, err)
	assert.Equal(t, first.Id, offer.Program.Id)
	legacy, err := ResolveBuilderProgram(DB, BuilderProgramSelector{PartnershipCode: second.Code}, false)
	require.NoError(t, err)
	assert.Equal(t, second.Id, legacy.Program.Id)
}

func TestBuilderNamedProgramPreservesExistingFundsAndAtomicGrant(t *testing.T) {
	setupPartnershipTestDB(t)
	require.NoError(t, DB.AutoMigrate(&BuilderIdentity{}, &Token{}))
	program := PartnershipProgram{Name: "Named Builders", Code: "named-builders", Group: "partner", GrantQuota: 5000000, GrantLimit: 1, Enabled: true}
	require.NoError(t, CreatePartnershipProgram(&program))
	proof := "management-test-credential"
	user := User{Username: "existing", Email: "existing@example.invalid", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", Quota: 12345, AccessToken: &proof}
	require.NoError(t, DB.Create(&user).Error)
	selector := BuilderProgramSelector{ProgramName: program.Name}
	_, err := ConnectBuilderIdentityWithProgram("owner", user.Email, selector, "")
	require.ErrorIs(t, err, ErrBuilderLinkRequired)
	link, err := ConnectBuilderIdentityWithProgram("owner", user.Email, selector, proof)
	require.NoError(t, err)
	require.NoError(t, DB.First(&user, user.Id).Error)
	assert.Equal(t, "default", user.Group)
	assert.Equal(t, 12345, user.Quota)
	require.NoError(t, DB.Exec("CREATE TRIGGER fail_named_grant BEFORE UPDATE OF quota ON users BEGIN SELECT RAISE(ABORT, 'injected failure'); END").Error)
	require.Error(t, ClaimBuilderTeamGrantWithProgram(link.Subject, selector))
	require.NoError(t, DB.First(&program, program.Id).Error)
	assert.Zero(t, program.ClaimedCount)
	stored, _, err := GetBuilderIdentity(link.Subject)
	require.NoError(t, err)
	assert.Zero(t, stored.GrantClaimedAt)
	require.NoError(t, DB.Exec("DROP TRIGGER fail_named_grant").Error)
	require.NoError(t, ClaimBuilderTeamGrantWithProgram(link.Subject, selector))
	quota, err := GetUserQuota(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, 5012345, quota)
}
