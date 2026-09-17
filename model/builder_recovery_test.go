/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"errors"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestBuilderRemovedCustomerGetsDistinctGroupWithoutMovingHistory(t *testing.T) {
	setupGroupRatioProvisionTest(t)
	program := PartnershipProgram{Name: "Builders", Code: "builders", Group: "partner", Enabled: true}
	require.NoError(t, CreatePartnershipProgram(&program))
	require.NoError(t, ProvisionBuilderCustomer(program.Name, "UnifyAPI"))
	old, err := ResolveBuilderProgram(DB, BuilderProgramSelector{ProgramName: program.Name, CustomerName: "UnifyAPI"}, false)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&PartnershipCustomer{}).Where("id = ?", old.CustomerId).Updates(map[string]any{"removed_at": 123, "enabled": false, "granted_quota": 5000000, "grant_claimed_at": 100}).Error)
	enrollment := PartnershipEnrollment{ProgramId: program.Id, CustomerId: old.CustomerId, CustomerGroup: old.CustomerGroup, UserId: 42, GrantedQuota: 5000000}
	require.NoError(t, DB.Create(&enrollment).Error)
	var archived PartnershipCustomer
	require.NoError(t, DB.First(&archived, old.CustomerId).Error)
	before := currentGroupRatios(t)
	// A suffix can also already belong to a separately configured price group.
	require.NoError(t, EnsurePartnershipGroupRatio("unifyapi-2"))
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- ProvisionBuilderCustomer(program.Name, "UnifyAPI") }()
	}
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	offer, err := ResolveBuilderProgram(DB, BuilderProgramSelector{ProgramName: program.Name, CustomerName: "UnifyAPI"}, false)
	require.NoError(t, err)
	assert.NotEqual(t, old.CustomerId, offer.CustomerId)
	assert.NotEqual(t, old.CustomerGroup, offer.CustomerGroup)
	assert.NotEqual(t, "unifyapi-2", offer.CustomerGroup)
	var unchanged PartnershipCustomer
	require.NoError(t, DB.First(&unchanged, old.CustomerId).Error)
	assert.Equal(t, archived, unchanged)
	var stored PartnershipEnrollment
	require.NoError(t, DB.First(&stored, enrollment.Id).Error)
	assert.Equal(t, enrollment, stored)
	var active int64
	require.NoError(t, DB.Model(&PartnershipCustomer{}).Where("program_id = ? AND name = ? AND removed_at = 0", program.Id, "UnifyAPI").Count(&active).Error)
	assert.EqualValues(t, 1, active)
	var fresh PartnershipCustomer
	require.NoError(t, DB.First(&fresh, offer.CustomerId).Error)
	assert.Zero(t, fresh.GrantedQuota)
	assert.Zero(t, fresh.GrantClaimedAt)
	assert.Zero(t, fresh.TenantId)
	for _, key := range []string{"GroupRatio", "TopupGroupRatio", "UserUsableGroups"} {
		var option Option
		require.NoError(t, DB.Where("key = ?", key).First(&option).Error)
		var entries map[string]any
		require.NoError(t, common.Unmarshal([]byte(option.Value), &entries))
		assert.Contains(t, entries, offer.CustomerGroup)
		assert.Contains(t, entries, old.CustomerGroup)
	}
	after := currentGroupRatios(t)
	for key, value := range before {
		assert.Equal(t, value, after[key])
	}
}

func TestBuilderDisabledCustomerAndFailedProvisionLeaveSettingsUntouched(t *testing.T) {
	for _, failure := range []string{"disabled", "ambiguous", "insert", "bad-setting", "wrong-setting-type"} {
		t.Run(failure, func(t *testing.T) {
			setupGroupRatioProvisionTest(t)
			program := PartnershipProgram{Name: "Builders", Code: "builders", Group: "partner", Enabled: true}
			require.NoError(t, CreatePartnershipProgram(&program))
			if failure == "disabled" || failure == "ambiguous" {
				require.NoError(t, ProvisionBuilderCustomer(program.Name, "Team"))
				if failure == "disabled" {
					require.NoError(t, DB.Model(&PartnershipCustomer{}).Where("name = ?", "Team").Update("enabled", false).Error)
				} else {
					require.NoError(t, DB.Create(&PartnershipCustomer{ProgramId: program.Id, Name: "Team", Code: "duplicate", Group: "vip", Enabled: true}).Error)
				}
			}
			if failure == "insert" {
				require.NoError(t, DB.Exec("CREATE TRIGGER reject_builder_customer BEFORE INSERT ON partnership_customers WHEN NEW.name = 'Team' BEGIN SELECT RAISE(ABORT, 'injected insert failure'); END").Error)
			}
			if failure == "bad-setting" {
				require.NoError(t, DB.Create(&Option{Key: "TopupGroupRatio", Value: "not-json"}).Error)
			}
			if failure == "wrong-setting-type" {
				require.NoError(t, DB.Create(&Option{Key: "TopupGroupRatio", Value: `{"existing":"not-a-number"}`}).Error)
			}
			var before, after []Option
			require.NoError(t, DB.Order("key").Find(&before).Error)
			var beforeHistory, afterHistory int64
			require.NoError(t, DB.Model(&PricingConfigHistory{}).Count(&beforeHistory).Error)
			err := ProvisionBuilderCustomer(program.Name, "Team")
			require.Error(t, err)
			if failure == "disabled" || failure == "ambiguous" {
				assert.ErrorIs(t, err, ErrPartnershipCustomerUnavailable)
			}
			require.NoError(t, DB.Order("key").Find(&after).Error)
			assert.Equal(t, before, after)
			require.NoError(t, DB.Model(&PricingConfigHistory{}).Count(&afterHistory).Error)
			assert.Equal(t, beforeHistory, afterHistory)
		})
	}
}

func TestBuilderMissingAccountRequiresRepairWithoutRecreatingOrGranting(t *testing.T) {
	for _, soft := range []bool{false, true} {
		t.Run(map[bool]string{false: "hard deleted", true: "soft deleted"}[soft], func(t *testing.T) {
			setupGroupRatioProvisionTest(t)
			require.NoError(t, DB.AutoMigrate(&BuilderIdentity{}, &Token{}))
			program := PartnershipProgram{Name: "Builders", Code: "builders", Group: "partner", Enabled: true, GrantQuota: 5000000, GrantLimit: 10}
			require.NoError(t, CreatePartnershipProgram(&program))
			link, err := ConnectBuilderIdentity("subject", "builder@example.invalid", program.Code, "")
			require.NoError(t, err)
			_, user, err := GetBuilderIdentity(link.Subject)
			require.NoError(t, err)
			require.NoError(t, DB.Model(&Tenant{}).Where("id = ?", user.TenantId).Update("quota", 5000000).Error)
			if soft {
				require.NoError(t, DB.Delete(&User{}, user.Id).Error)
			} else {
				require.NoError(t, DB.Unscoped().Delete(&User{}, user.Id).Error)
			}
			var tenantBefore Tenant
			require.NoError(t, DB.First(&tenantBefore, user.TenantId).Error)
			var enrollmentBefore PartnershipEnrollment
			require.NoError(t, DB.Where("user_id = ?", user.Id).First(&enrollmentBefore).Error)
			_, _, err = GetBuilderIdentity(link.Subject)
			assert.ErrorIs(t, err, ErrBuilderAccountRepairRequired)
			assert.False(t, errors.Is(err, gorm.ErrRecordNotFound))
			_, err = ConnectBuilderIdentity(link.Subject, "builder@example.invalid", program.Code, "")
			assert.ErrorIs(t, err, ErrBuilderAccountRepairRequired)
			err = ClaimBuilderTeamGrant(link.Subject, program.Code)
			assert.ErrorIs(t, err, ErrBuilderAccountRepairRequired)
			var unchanged BuilderIdentity
			require.NoError(t, DB.First(&unchanged, link.Id).Error)
			assert.Equal(t, *link, unchanged)
			var tenantAfter Tenant
			require.NoError(t, DB.First(&tenantAfter, user.TenantId).Error)
			assert.Equal(t, tenantBefore, tenantAfter)
			var enrollmentAfter PartnershipEnrollment
			require.NoError(t, DB.First(&enrollmentAfter, enrollmentBefore.Id).Error)
			assert.Equal(t, enrollmentBefore, enrollmentAfter)
			var users int64
			require.NoError(t, DB.Model(&User{}).Count(&users).Error)
			assert.Zero(t, users)
		})
	}
}

func TestBuilderArchiveIsNotAUsableSubject(t *testing.T) {
	subject := BuilderArchivedSubjectPrefix + "builder:2:20260917"
	_, _, err := GetBuilderIdentity(subject)
	assert.ErrorIs(t, err, ErrBuilderUnavailable)
	_, err = ConnectBuilderIdentityWithProgram(subject, "verified@example.invalid", BuilderProgramSelector{ProgramName: "Builders"}, "")
	assert.ErrorIs(t, err, ErrBuilderUnavailable)
	assert.ErrorIs(t, ClaimBuilderTeamGrantWithProgram(subject, BuilderProgramSelector{ProgramName: "Builders"}), ErrBuilderUnavailable)
}
