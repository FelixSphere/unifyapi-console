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
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Group Pricing is one option row, and saving it without a group is how a
// group is deleted. These tests are about what that save may take away.
//
// The first one is the rename the form used to perform: the same map with one
// key replaced. That is a delete of the old name as far as everything else is
// concerned, and on production it left a customer's logins, keys, balance and
// channel exclusions on a name with no price.

// setupPricingGroupTest gives the test its own database and its own pricing
// settings, and puts both back afterwards. GroupRatio is the stored Group
// Pricing; the other options are stored and loaded the way a boot loads them.
func setupPricingGroupTest(t *testing.T, options map[string]string) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&Option{}, &User{}, &Token{}, &Tenant{}, &CustomerWallet{}, &Channel{}, &Ability{},
		&PartnershipProgram{}, &PartnershipCustomer{}, &PartnershipEnrollment{},
		&TopUp{}, &SubscriptionPlan{}, &UserSubscription{}, &CreditPool{}, &Task{},
		&PricingConfigHistory{},
	))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	previousDB, previousType := DB, common.MainDatabaseType()
	previousMaster, previousCache, previousRedis := common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled
	previousOptionMap := common.OptionMap
	specialRules, err := common.Marshal(ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.ReadAll())
	require.NoError(t, err)
	previousSettings := map[string]string{
		"GroupRatio":                 ratio_setting.GroupRatio2JSONString(),
		"GroupGroupRatio":            ratio_setting.GroupGroupRatio2JSONString(),
		"GroupModelDiscount":         ratio_setting.GroupModelDiscount2JSONString(),
		"TopupGroupRatio":            common.TopupGroupRatio2JSONString(),
		"UserUsableGroups":           setting.UserUsableGroups2JSONString(),
		"AutoGroups":                 setting.AutoGroups2JsonString(),
		"ModelRequestRateLimitGroup": setting.ModelRequestRateLimitGroup2JSONString(),
		"group_ratio_setting.group_special_usable_group": string(specialRules),
	}

	DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.IsMasterNode = true
	common.MemoryCacheEnabled = false
	common.RedisEnabled = false
	common.OptionMap = make(map[string]string)
	initCol()
	t.Cleanup(func() {
		for key, value := range previousSettings {
			require.NoError(t, updateOptionMapLocked(key, value), key)
		}
		DB = previousDB
		common.SetMainDatabaseType(previousType)
		common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled = previousMaster, previousCache, previousRedis
		common.OptionMap = previousOptionMap
		initCol()
		_ = sqlDB.Close()
	})

	// Stored in a fixed order, ExtraModelPricing first: rows are loaded in
	// storage order and the customer prices are checked against it.
	for _, key := range []string{
		"ExtraModelPricing", "GroupRatio", "TopupGroupRatio", "UserUsableGroups", "GroupModelDiscount",
		"GroupGroupRatio", "AutoGroups", "ModelRequestRateLimitGroup",
		"group_ratio_setting.group_special_usable_group",
	} {
		value, ok := options[key]
		if !ok {
			continue
		}
		require.NoError(t, DB.Create(&Option{Key: key, Value: value}).Error, key)
		require.NoError(t, updateOptionMapLocked(key, value), key)
	}
}

func createLogin(t *testing.T, username, group string, tenantId int) *User {
	t.Helper()
	user := &User{Username: username, Password: "not-a-real-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: group, TenantId: tenantId, AffCode: username}
	require.NoError(t, DB.Create(user).Error)
	return user
}

func createKey(t *testing.T, user *User, key, group string) *Token {
	t.Helper()
	token := &Token{UserId: user.Id, Key: key, Name: key, Status: common.TokenStatusEnabled,
		ExpiredTime: -1, UnlimitedQuota: true, Group: group}
	require.NoError(t, DB.Create(token).Error)
	return token
}

func createWallet(t *testing.T, group string, balance int) *Tenant {
	t.Helper()
	tenant := &Tenant{Name: group, Slug: slugFromName("customer-" + group), Status: TenantStatusEnabled, Quota: balance, Group: group}
	require.NoError(t, DB.Create(tenant).Error)
	require.NoError(t, DB.Create(&CustomerWallet{Group: group, TenantId: tenant.Id}).Error)
	return tenant
}

func TestGroupPricingRefusesToDropAGroupThatLoginsStillUse(t *testing.T) {
	setupPricingGroupTest(t, map[string]string{"GroupRatio": `{"default":1,"friend solo":1,"GenAI":1}`})
	createLogin(t, "gade", "friend solo", 0)

	// What the form sent for a rename: the same table with the key replaced.
	err := UpdateOption("GroupRatio", `{"default":1,"Gade Union":1,"GenAI":1}`)

	require.Error(t, err, "the login would be left on a group that has no price")
	assert.Contains(t, err.Error(), `"friend solo"`)
	assert.Contains(t, err.Error(), "1 login(s)")
	assert.True(t, ratio_setting.ContainsGroupRatio("friend solo"), "a refused save changes nothing in memory")
	var stored Option
	require.NoError(t, DB.Where(map[string]any{"key": "GroupRatio"}).First(&stored).Error)
	assert.JSONEq(t, `{"default":1,"friend solo":1,"GenAI":1}`, stored.Value, "or in the database")
}

func TestGroupPricingNamesEverythingThatStillUsesTheGroup(t *testing.T) {
	setupPricingGroupTest(t, map[string]string{"GroupRatio": `{"default":1,"friend solo":1}`})
	wallet := createWallet(t, "friend solo", 2500)
	login := createLogin(t, "gade", "friend solo", wallet.Id)
	createKey(t, login, "fixedgroupkey", "friend solo")
	disabled := createKey(t, login, "disabledkey", "friend solo")
	require.NoError(t, DB.Model(disabled).Update("status", common.TokenStatusDisabled).Error)
	blocked := createChannel(t, "mythos", "default", "gpt-6-astra")
	require.NoError(t, DB.Model(blocked).Update("settings", `{"excluded_groups":["friend solo"]}`).Error)

	err := UpdateOption("GroupRatio", `{"default":1}`)

	require.Error(t, err)
	for _, part := range []string{
		"1 login(s)",
		"1 enabled API key(s) fixed to it",
		"a wallet that still holds a balance",
		"the blocked customer groups of channel(s)",
		"Rename",
	} {
		assert.Contains(t, err.Error(), part)
	}
}

func TestGroupPricingStillDeletesAGroupNothingUses(t *testing.T) {
	setupPricingGroupTest(t, map[string]string{"GroupRatio": `{"default":1,"friend solo":1,"trial":1}`})
	// An emptied customer: the login moved away, the key is disabled and the
	// wallet row stays behind with nothing in it.
	wallet := createWallet(t, "trial", 0)
	login := createLogin(t, "gade", "friend solo", wallet.Id)
	spent := createKey(t, login, "spentkey", "trial")
	require.NoError(t, DB.Model(spent).Update("status", common.TokenStatusDisabled).Error)

	require.NoError(t, UpdateOption("GroupRatio", `{"default":1,"friend solo":1}`))

	assert.False(t, ratio_setting.ContainsGroupRatio("trial"))
}

func TestGroupPricingSavesThatRemoveNothingAreNotHeldUpByOldReferences(t *testing.T) {
	setupPricingGroupTest(t, map[string]string{"GroupRatio": `{"default":1,"GenAI":1}`})
	// A login on a group that was deleted before this guard existed.
	createLogin(t, "stranded", "long gone", 0)

	require.NoError(t, UpdateOption("GroupRatio", `{"default":1,"GenAI":0.9,"Kingdee":1}`),
		"repricing one group and adding another removes nothing")

	assert.Equal(t, 0.9, ratio_setting.GetGroupRatio("GenAI"))
}
