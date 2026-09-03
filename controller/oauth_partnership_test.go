/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/oauth"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPartnershipOAuthKeepsInviterAttributionWithoutRewards(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.User{}, &model.Tenant{}, &model.Log{},
		&model.Option{}, &model.PartnershipProgram{}, &model.PartnershipCustomer{}, &model.PartnershipEnrollment{},
	))
	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousType := common.MainDatabaseType()
	previousRedis := common.RedisEnabled
	previousRegister := common.RegisterEnabled
	previousInviteeQuota := common.QuotaForInvitee
	previousInviterQuota := common.QuotaForInviter
	payment := operation_setting.GetPaymentSetting()
	previousPayment := *payment
	model.DB = db
	model.LOG_DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.RegisterEnabled = true
	common.QuotaForInvitee = 1234
	common.QuotaForInviter = 5678
	require.NoError(t, model.DB.Create(&model.Option{
		Key: "GroupRatio", Value: `{"partner":0.9}`,
	}).Error)
	payment.ComplianceConfirmed = true
	payment.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.SetMainDatabaseType(previousType)
		common.RedisEnabled = previousRedis
		common.RegisterEnabled = previousRegister
		common.QuotaForInvitee = previousInviteeQuota
		common.QuotaForInviter = previousInviterQuota
		*payment = previousPayment
	})

	inviter := &model.User{Username: "oauth-inviter", DisplayName: "OAuth Inviter", AffCode: "oauth-aff", Quota: 99}
	require.NoError(t, model.DB.Create(inviter).Error)
	program := &model.PartnershipProgram{
		Name: "OAuth Program", Code: "oauth-program", Group: "partner",
		GrantQuota: 5000000, GrantLimit: 1, Enabled: true,
	}
	require.NoError(t, model.CreatePartnershipProgram(program))

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	user, err := findOrCreateOAuthUser(c, &authFlowTestOAuthProvider{}, &oauth.OAuthUser{
		ProviderUserID: "partner-external", Username: "oauth-partner", DisplayName: "OAuth Partner",
	}, inviter.AffCode, program.Code)
	require.NoError(t, err)

	var stored model.User
	require.NoError(t, model.DB.First(&stored, user.Id).Error)
	assert.Equal(t, inviter.Id, stored.InviterId)
	var enrollment model.PartnershipEnrollment
	require.NoError(t, model.DB.Where("user_id = ?", user.Id).First(&enrollment).Error)
	assert.Equal(t, 5000000, enrollment.GrantedQuota)
	var tenant model.Tenant
	require.NoError(t, model.DB.First(&tenant, stored.TenantId).Error)
	assert.Equal(t, 5000000, tenant.Quota, "invitee reward must not stack")
	var storedInviter model.User
	require.NoError(t, model.DB.First(&storedInviter, inviter.Id).Error)
	assert.Equal(t, 99, storedInviter.Quota, "inviter reward must not stack")
}
