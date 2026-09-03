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
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupPartnershipTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&User{}, &Tenant{}, &Log{}, &PartnershipProgram{}, &PartnershipEnrollment{},
	))
	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous })
}

func TestPartnershipGrantStopsAtConfiguredLimit(t *testing.T) {
	setupPartnershipTestDB(t)
	require.NoError(t, CreatePartnershipProgram(&PartnershipProgram{
		Name: "Online developer event", Code: "dev-event", Group: "partner",
		GrantQuota: 5000000, GrantLimit: 2, Enabled: true,
	}))

	for i, wantGrant := range []int{5000000, 5000000, 0} {
		user := &User{
			Username: fmt.Sprintf("partner%d", i), Password: "password123",
			DisplayName: fmt.Sprintf("Partner %d", i), Role: 1, Status: 1,
		}
		grant, err := user.InsertForPartnership("DEV-EVENT", 0)
		require.NoError(t, err)
		assert.Equal(t, wantGrant, grant)

		var stored User
		require.NoError(t, DB.First(&stored, user.Id).Error)
		assert.Equal(t, "partner", stored.Group)
		assert.Zero(t, stored.Quota, "starting balance must live on the tenant")

		var tenant Tenant
		require.NoError(t, DB.First(&tenant, stored.TenantId).Error)
		assert.Equal(t, wantGrant, tenant.Quota)
	}

	program, err := GetPartnershipProgramByCode("dev-event")
	require.NoError(t, err)
	assert.Equal(t, 2, program.ClaimedCount)
	var enrollments []PartnershipEnrollment
	require.NoError(t, DB.Order("user_id").Find(&enrollments).Error)
	require.Len(t, enrollments, 3)
	assert.Equal(t, []int{5000000, 5000000, 0}, []int{
		enrollments[0].GrantedQuota, enrollments[1].GrantedQuota, enrollments[2].GrantedQuota,
	})
}

func TestUnavailablePartnershipDoesNotCreateUser(t *testing.T) {
	setupPartnershipTestDB(t)
	require.NoError(t, CreatePartnershipProgram(&PartnershipProgram{
		Name: "Paused event", Code: "paused-event", Group: "partner",
		GrantQuota: 100, GrantLimit: 1, Enabled: false,
	}))

	user := &User{Username: "blocked", Password: "password123", Role: 1, Status: 1}
	_, err := user.InsertForPartnership("paused-event", 0)
	require.ErrorIs(t, err, ErrPartnershipProgramUnavailable)
	var count int64
	require.NoError(t, DB.Model(&User{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestProgramLimitCannotDropBelowClaims(t *testing.T) {
	setupPartnershipTestDB(t)
	program := &PartnershipProgram{
		Name: "Developer event", Code: "developer-event", Group: "partner",
		GrantQuota: 100, GrantLimit: 1, ClaimedCount: 1, Enabled: true,
	}
	require.NoError(t, DB.Create(program).Error)
	err := UpdatePartnershipProgram(program.Id, &PartnershipProgram{
		Name: program.Name, Code: program.Code, Group: program.Group,
		GrantQuota: 100, GrantLimit: 0, Enabled: true,
	})
	assert.Error(t, err)
	assert.False(t, errors.Is(err, gorm.ErrRecordNotFound))
}

func TestConnectingExistingUserDoesNotChangeGroupOrGrant(t *testing.T) {
	setupPartnershipTestDB(t)
	program := &PartnershipProgram{
		Name: "Existing account event", Code: "existing-event", Group: "partner",
		GrantQuota: 5000000, GrantLimit: 2, Enabled: true,
	}
	require.NoError(t, CreatePartnershipProgram(program))
	user := &User{Username: "existing", DisplayName: "Existing", Group: "vip", Quota: 700, AffCode: "existing-aff"}
	require.NoError(t, DB.Create(user).Error)

	connection, err := ConnectExistingUserToPartnership(user.Id, program.Code)
	require.NoError(t, err)
	assert.Equal(t, PartnershipStatusConnectedExisting, connection.Status)
	assert.Equal(t, "vip", connection.UserGroup)
	assert.Equal(t, "partner", connection.ProgramGroup)

	// Reconnecting is idempotent and never consumes a registration grant.
	_, err = ConnectExistingUserToPartnership(user.Id, program.Code)
	require.NoError(t, err)
	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	assert.Equal(t, "vip", stored.Group)
	assert.Equal(t, 700, stored.Quota)
	var enrollments int64
	require.NoError(t, DB.Model(&PartnershipEnrollment{}).Count(&enrollments).Error)
	assert.Equal(t, int64(1), enrollments)
	var reloadedProgram PartnershipProgram
	require.NoError(t, DB.First(&reloadedProgram, program.Id).Error)
	assert.Zero(t, reloadedProgram.ClaimedCount)
}

func TestEnabledProgramPreventsDanglingGroupReference(t *testing.T) {
	setupPartnershipTestDB(t)
	program := &PartnershipProgram{
		Name: "Protected group event", Code: "protected-group", Group: "partner",
		GrantLimit: 1, Enabled: true,
	}
	require.NoError(t, CreatePartnershipProgram(program))
	assert.Error(t, ValidateActivePartnershipGroups(map[string]struct{}{"default": {}}))
	assert.NoError(t, ValidateActivePartnershipGroups(map[string]struct{}{
		"default": {}, "partner": {},
	}))
	assert.Error(t, validateOptionValue("GroupRatio", `{"default":1}`))
	assert.Error(t, validateOptionValue("UserUsableGroups", `{"default":"Default"}`))
	assert.NoError(t, validateOptionValue("GroupRatio", `{"default":1,"partner":0.9}`))
	assert.NoError(t, validateOptionValue("UserUsableGroups", `{"default":"Default","partner":"Partner"}`))

	require.NoError(t, DB.Model(program).Update("enabled", false).Error)
	assert.NoError(t, ValidateActivePartnershipGroups(map[string]struct{}{"default": {}}))
}
