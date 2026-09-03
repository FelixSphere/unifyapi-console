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
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
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
		&User{}, &Tenant{}, &Log{}, &Option{}, &PartnershipProgram{}, &PartnershipCustomer{}, &PartnershipEnrollment{},
	))
	previous := DB
	previousType := common.MainDatabaseType()
	DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	require.NoError(t, DB.Create(&Option{
		Key: "GroupRatio", Value: `{"default":1,"partner":0.9,"vip":0.8}`,
	}).Error)
	t.Cleanup(func() {
		DB = previous
		common.SetMainDatabaseType(previousType)
	})
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
		grant, err := user.InsertForPartnership("DEV-EVENT")
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

func TestConcurrentPartnershipRegistrationsClaimLastGrantOnce(t *testing.T) {
	setupPartnershipTestDB(t)
	sqlDB, err := DB.DB()
	require.NoError(t, err)
	// A single shared SQLite connection serializes the two transactions while
	// still exercising the conditional claimed_count update from two callers.
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, CreatePartnershipProgram(&PartnershipProgram{
		Name: "Last grant", Code: "last-grant", Group: "partner",
		GrantQuota: 5000000, GrantLimit: 1, Enabled: true,
	}))

	start := make(chan struct{})
	grants := make(chan int, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			user := &User{
				Username: fmt.Sprintf("lastgrant%d", index), Password: "password123",
				DisplayName: fmt.Sprintf("Last Grant %d", index), Role: 1, Status: 1,
			}
			grant, insertErr := user.InsertForPartnership("last-grant")
			grants <- grant
			errs <- insertErr
		}(i)
	}
	close(start)
	wg.Wait()
	close(grants)
	close(errs)
	for insertErr := range errs {
		require.NoError(t, insertErr)
	}
	grantedCount := 0
	for grant := range grants {
		if grant > 0 {
			grantedCount++
		}
	}
	assert.Equal(t, 1, grantedCount)
	program, err := GetPartnershipProgramByCode("last-grant")
	require.NoError(t, err)
	assert.Equal(t, 1, program.ClaimedCount)
}

func TestPartnershipRegistrationDoesNotStackAffiliateRewards(t *testing.T) {
	setupPartnershipTestDB(t)
	previousInviteeQuota := common.QuotaForInvitee
	previousInviterQuota := common.QuotaForInviter
	payment := operation_setting.GetPaymentSetting()
	previousPayment := *payment
	t.Cleanup(func() {
		common.QuotaForInvitee = previousInviteeQuota
		common.QuotaForInviter = previousInviterQuota
		*payment = previousPayment
	})
	common.QuotaForInvitee = 1234
	common.QuotaForInviter = 5678
	payment.ComplianceConfirmed = true
	payment.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion

	require.NoError(t, CreatePartnershipProgram(&PartnershipProgram{
		Name: "Exhausted program", Code: "exhausted-program", Group: "partner",
		GrantQuota: 5000000, GrantLimit: 0, Enabled: true,
	}))
	inviter := &User{Username: "inviter", DisplayName: "Inviter", AffCode: "invite", Quota: 99}
	require.NoError(t, DB.Create(inviter).Error)
	user := &User{
		Username: "partnerinvitee", Password: "password123", DisplayName: "Partner Invitee",
		Role: 1, Status: 1, InviterId: inviter.Id,
	}
	grant, err := user.InsertForPartnership("exhausted-program")
	require.NoError(t, err)
	assert.Zero(t, grant)

	var storedUser User
	require.NoError(t, DB.First(&storedUser, user.Id).Error)
	var tenant Tenant
	require.NoError(t, DB.First(&tenant, storedUser.TenantId).Error)
	assert.Zero(t, tenant.Quota, "an exhausted Program must not gain invitee credit")
	var storedInviter User
	require.NoError(t, DB.First(&storedInviter, inviter.Id).Error)
	assert.Equal(t, 99, storedInviter.Quota)
	assert.Equal(t, inviter.Id, storedUser.InviterId, "the relationship remains available for attribution")
}

func TestUnavailablePartnershipDoesNotCreateUser(t *testing.T) {
	setupPartnershipTestDB(t)
	require.NoError(t, CreatePartnershipProgram(&PartnershipProgram{
		Name: "Paused event", Code: "paused-event", Group: "partner",
		GrantQuota: 100, GrantLimit: 1, Enabled: false,
	}))

	user := &User{Username: "blocked", Password: "password123", Role: 1, Status: 1}
	_, err := user.InsertForPartnership("paused-event")
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

func TestConcurrentProgramUpdatePreservesClaimLimitInvariant(t *testing.T) {
	setupPartnershipTestDB(t)
	sqlDB, err := DB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	program := &PartnershipProgram{
		Name: "Concurrent program", Code: "concurrent-program", Group: "partner",
		GrantQuota: 100, GrantLimit: 1, Enabled: true,
	}
	require.NoError(t, CreatePartnershipProgram(program))

	start := make(chan struct{})
	registrationErr := make(chan error, 1)
	updateErr := make(chan error, 1)
	go func() {
		<-start
		user := &User{Username: "concurrentuser", Password: "password123", Role: 1, Status: 1}
		_, insertErr := user.InsertForPartnership(program.Code)
		registrationErr <- insertErr
	}()
	go func() {
		<-start
		updateErr <- UpdatePartnershipProgram(program.Id, &PartnershipProgram{
			Name: program.Name, Code: program.Code, Group: program.Group,
			GrantQuota: program.GrantQuota, GrantLimit: 0, Enabled: true,
		})
	}()
	close(start)
	require.NoError(t, <-registrationErr)
	possibleUpdateErr := <-updateErr

	var stored PartnershipProgram
	require.NoError(t, DB.First(&stored, program.Id).Error)
	assert.GreaterOrEqual(t, stored.GrantLimit, stored.ClaimedCount)
	if stored.ClaimedCount == 1 {
		require.Error(t, possibleUpdateErr)
		assert.Contains(t, possibleUpdateErr.Error(), "grant limit cannot be lower")
	} else {
		require.NoError(t, possibleUpdateErr)
		assert.Zero(t, stored.GrantLimit)
	}
}

func TestConnectingExistingUserDoesNotChangeGroupOrGrant(t *testing.T) {
	setupPartnershipTestDB(t)
	program := &PartnershipProgram{
		Name: "Existing account event", Code: "existing-event", Group: "partner",
		GrantQuota: 5000000, GrantLimit: 2, Enabled: true,
	}
	require.NoError(t, CreatePartnershipProgram(program))
	user := &User{Username: "existing", DisplayName: "Existing", Group: "partner", Quota: 700, AffCode: "existing-aff"}
	require.NoError(t, DB.Create(user).Error)

	connection, err := ConnectExistingUserToPartnership(user.Id, program.Code)
	require.NoError(t, err)
	assert.Equal(t, PartnershipStatusConnectedExisting, connection.Status)
	assert.Equal(t, "partner", connection.UserGroup)
	assert.Equal(t, "partner", connection.ProgramGroup)

	// Reconnecting is idempotent and never consumes a registration grant.
	_, err = ConnectExistingUserToPartnership(user.Id, program.Code)
	require.NoError(t, err)
	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	assert.Equal(t, "partner", stored.Group)
	assert.Equal(t, 700, stored.Quota)
	var enrollments int64
	require.NoError(t, DB.Model(&PartnershipEnrollment{}).Count(&enrollments).Error)
	assert.Equal(t, int64(1), enrollments)
	var reloadedProgram PartnershipProgram
	require.NoError(t, DB.First(&reloadedProgram, program.Id).Error)
	assert.Zero(t, reloadedProgram.ClaimedCount)
}

func TestExistingUserCannotConnectThroughAnotherCustomerLink(t *testing.T) {
	setupPartnershipTestDB(t)
	program := &PartnershipProgram{
		Name: "Customer-safe event", Code: "customer-safe", Group: "partner",
		GrantLimit: 1, Enabled: true,
	}
	require.NoError(t, CreatePartnershipProgram(program))
	user := &User{Username: "wrongcustomer", DisplayName: "Wrong Customer", Group: "vip"}
	require.NoError(t, DB.Create(user).Error)

	_, err := ConnectExistingUserToPartnership(user.Id, program.Code)
	require.ErrorIs(t, err, ErrPartnershipCustomerMismatch)
	var enrollments int64
	require.NoError(t, DB.Model(&PartnershipEnrollment{}).Count(&enrollments).Error)
	assert.Zero(t, enrollments)
}

func TestPartnershipCustomerLinkAssignsItsOwnBillableGroup(t *testing.T) {
	setupPartnershipTestDB(t)
	program := &PartnershipProgram{
		Name: "Regional event", Code: "regional-event", Group: "partner",
		GrantQuota: 5000000, GrantLimit: 2, Enabled: true,
	}
	require.NoError(t, CreatePartnershipProgram(program))
	customer := &PartnershipCustomer{
		Name: "Acme Vietnam", Code: "acme-vietnam", Group: "vip", Enabled: true,
	}
	require.NoError(t, CreatePartnershipCustomer(program.Id, customer))

	user := &User{Username: "acmedev", Password: "password123", Role: 1, Status: 1}
	grant, err := user.InsertForPartnership("acme-vietnam")
	require.NoError(t, err)
	assert.Equal(t, 5000000, grant)
	assert.Equal(t, "vip", user.Group)

	var enrollment PartnershipEnrollment
	require.NoError(t, DB.Where("user_id = ?", user.Id).First(&enrollment).Error)
	assert.Equal(t, customer.Id, enrollment.CustomerId)
	assert.Equal(t, "vip", enrollment.CustomerGroup)
}

func TestPartnershipCodesShareOnePublicNamespace(t *testing.T) {
	setupPartnershipTestDB(t)
	program := &PartnershipProgram{
		Name: "Regional event", Code: "regional-code", Group: "partner",
		GrantLimit: 1, Enabled: true,
	}
	require.NoError(t, CreatePartnershipProgram(program))

	err := CreatePartnershipCustomer(program.Id, &PartnershipCustomer{
		Name: "Program collision", Code: program.Code, Group: "vip", Enabled: true,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already in use")

	require.NoError(t, CreatePartnershipCustomer(program.Id, &PartnershipCustomer{
		Name: "Acme Vietnam", Code: "acme-code", Group: "vip", Enabled: true,
	}))
	err = CreatePartnershipProgram(&PartnershipProgram{
		Name: "Customer collision", Code: "acme-code", Group: "default",
		GrantLimit: 1, Enabled: true,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already in use")
}

func TestInitializePartnershipCustomersBackfillsLegacyAccountingOwner(t *testing.T) {
	setupPartnershipTestDB(t)
	program := &PartnershipProgram{
		Name: "Legacy event", Code: "legacy-event", Group: "partner",
		GrantLimit: 1, Enabled: true,
	}
	require.NoError(t, DB.Create(program).Error)
	enrollment := &PartnershipEnrollment{ProgramId: program.Id, UserId: 42}
	require.NoError(t, DB.Create(enrollment).Error)

	require.NoError(t, initializePartnershipCustomers())
	require.NoError(t, initializePartnershipCustomers(), "startup backfill must be idempotent")

	var customer PartnershipCustomer
	require.NoError(t, DB.Where("program_id = ? AND is_default = ?", program.Id, true).First(&customer).Error)
	assert.Equal(t, program.Code, customer.Code)
	assert.Equal(t, program.Group, customer.Group)
	require.NoError(t, DB.First(&enrollment, enrollment.Id).Error)
	assert.Equal(t, customer.Id, enrollment.CustomerId)
	assert.Equal(t, program.Group, enrollment.CustomerGroup)
	var customerCount int64
	require.NoError(t, DB.Model(&PartnershipCustomer{}).Where("program_id = ?", program.Id).Count(&customerCount).Error)
	assert.Equal(t, int64(1), customerCount)
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
	assert.NoError(t, validateOptionValue("UserUsableGroups", `{}`))
	assert.NoError(t, validateOptionValue("GroupRatio", `{"default":1,"partner":0.9}`))

	require.NoError(t, DB.Model(program).Update("enabled", false).Error)
	assert.NoError(t, ValidateActivePartnershipGroups(map[string]struct{}{"default": {}}))
}

func TestGroupPricingUpdateAndProgramWriteNeverLeaveDanglingReference(t *testing.T) {
	setupPartnershipTestDB(t)
	previousRatios := ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousRatios))
	})
	sqlDB, err := DB.DB()
	require.NoError(t, err)
	// One shared connection gives SQLite the same transaction ordering that the
	// production PostgreSQL advisory lock enforces between the two root writes.
	sqlDB.SetMaxOpenConns(1)

	start := make(chan struct{})
	programErr := make(chan error, 1)
	pricingErr := make(chan error, 1)
	go func() {
		<-start
		programErr <- CreatePartnershipProgram(&PartnershipProgram{
			Name: "Concurrent group program", Code: "concurrent-group", Group: "partner",
			GrantLimit: 1, Enabled: true,
		})
	}()
	go func() {
		<-start
		pricingErr <- DB.Transaction(func(tx *gorm.DB) error {
			return withPartnershipGroupIntegrityLock(tx, func(tx *gorm.DB) error {
				if err := validateOptionValueWithDB(tx, "GroupRatio", `{"default":1}`); err != nil {
					return err
				}
				return saveOptionValue(tx, "GroupRatio", `{"default":1}`)
			})
		})
	}()
	close(start)
	createErr := <-programErr
	updateErr := <-pricingErr

	var option Option
	require.NoError(t, DB.Where("key = ?", "GroupRatio").First(&option).Error)
	var groups map[string]float64
	require.NoError(t, common.Unmarshal([]byte(option.Value), &groups))
	var activeCount int64
	require.NoError(t, DB.Model(&PartnershipProgram{}).
		Where("enabled = ? AND `group` = ?", true, "partner").Count(&activeCount).Error)
	_, groupExists := groups["partner"]
	assert.False(t, activeCount > 0 && !groupExists,
		"committed state must not contain an enabled program whose group was removed")

	if createErr == nil {
		require.Error(t, updateErr)
		assert.Contains(t, updateErr.Error(), "used by an enabled partnership")
		assert.Equal(t, 0.9, groups["partner"])
	} else {
		require.NoError(t, updateErr)
		assert.Contains(t, createErr.Error(), "does not exist in Group Pricing")
		_, exists := groups["partner"]
		assert.False(t, exists)
	}
}
