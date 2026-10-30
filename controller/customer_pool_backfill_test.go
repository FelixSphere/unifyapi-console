/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type backfillBody struct {
	Success bool `json:"success"`
	Data    struct {
		DryRun       bool `json:"dry_run"`
		MembersMoved int  `json:"members_moved"`
		QuotaCarried int  `json:"quota_carried"`
	} `json:"data"`
}

func callBackfill(t *testing.T, query string) backfillBody {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/partnership/customer-pools/backfill"+query, nil)
	BackfillCustomerPools(c)
	var body backfillBody
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &body))
	return body
}

// common.RedisEnabled defaults to true and only turns off when a real client
// is initialised, which no unit test does. Leaving it on means the cache
// invalidation that follows a successful move dereferences a nil client.
func withoutRedis(t *testing.T) {
	t.Helper()
	previous := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previous })
}

func seedPooledMember(t *testing.T) {
	t.Helper()
	require.NoError(t, model.DB.Model(&model.Option{}).Where("key = ?", "GroupRatio").
		Update("value", `{"partner":0.9,"nusa-labs":1}`).Error)
	program := &model.PartnershipProgram{
		Name: "Builder hub", Code: "builder-hub", Group: "partner",
		GrantQuota: 5000000, GrantLimit: 50, Enabled: true,
	}
	require.NoError(t, model.CreatePartnershipProgram(program))
	customer := &model.PartnershipCustomer{
		ProgramId: program.Id, Name: "Nusa Labs", Code: "nusa-labs",
		Group: "nusa-labs", Enabled: true,
	}
	require.NoError(t, model.DB.Create(customer).Error)
	user := &model.User{
		Username: "member", Password: "password123", DisplayName: "M",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Group: "nusa-labs", Quota: 5000000, AffCode: "mem1",
	}
	require.NoError(t, model.DB.Create(user).Error)
	require.NoError(t, model.DB.Create(&model.PartnershipEnrollment{
		ProgramId: program.Id, CustomerId: customer.Id,
		CustomerGroup: customer.Group, UserId: user.Id,
	}).Error)
}

// The endpoint must default to reporting, not moving. An operator who calls
// it to look must not discover afterwards that it moved the money.
func TestTheBackfillEndpointDefaultsToADryRun(t *testing.T) {
	setupPartnershipControllerTest(t)
	withoutRedis(t)
	require.NoError(t, model.DB.AutoMigrate(&model.User{}, &model.Tenant{}))
	seedPooledMember(t)

	body := callBackfill(t, "")
	require.True(t, body.Success)
	assert.True(t, body.Data.DryRun, "the default must be a dry run")
	assert.Equal(t, 1, body.Data.MembersMoved)
	assert.Equal(t, 5000000, body.Data.QuotaCarried)

	var user model.User
	require.NoError(t, model.DB.Where("username = ?", "member").First(&user).Error)
	assert.Equal(t, 5000000, user.Quota, "a dry run must not have moved the money")
	assert.Zero(t, user.TenantId)
}

// And applying it takes an explicit flag.
func TestTheBackfillEndpointAppliesOnlyWhenAsked(t *testing.T) {
	setupPartnershipControllerTest(t)
	withoutRedis(t)
	require.NoError(t, model.DB.AutoMigrate(&model.User{}, &model.Tenant{}))
	seedPooledMember(t)

	body := callBackfill(t, "?apply=true")
	require.True(t, body.Success)
	assert.False(t, body.Data.DryRun)
	assert.Equal(t, 1, body.Data.MembersMoved)

	var user model.User
	require.NoError(t, model.DB.Where("username = ?", "member").First(&user).Error)
	assert.NotZero(t, user.TenantId, "the member now draws on the customer's wallet")
	assert.Zero(t, user.Quota, "and their private column is emptied, not double counted")

	quota, err := model.GetUserQuota(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, 5000000, quota, "with every unit of their credit carried across")
}
