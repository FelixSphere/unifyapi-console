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
