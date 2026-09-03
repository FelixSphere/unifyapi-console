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
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupPartnershipControllerTest(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.Option{}, &model.PartnershipProgram{}, &model.PartnershipCustomer{}, &model.PartnershipEnrollment{},
	))
	previousDB := model.DB
	previousType := common.MainDatabaseType()
	previousRatios := ratio_setting.GroupRatio2JSONString()
	previousUsable := setting.UserUsableGroups2JSONString()
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	require.NoError(t, model.DB.Create(&model.Option{
		Key: "GroupRatio", Value: `{"partner":0.9}`,
	}).Error)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"partner":0.9}`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{}`))
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousRatios))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousUsable))
	})
}

func TestPartnershipProgramsUseGroupPricingWhenUsableGroupsEmpty(t *testing.T) {
	setupPartnershipControllerTest(t)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/partnership/", strings.NewReader(`{
		"name":"Partner Program","code":"partner-program","group":"partner",
		"grant_quota":5000000,"grant_limit":50,"enabled":true
	}`))
	c.Request.Header.Set("Content-Type", "application/json")

	CreatePartnershipProgram(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var created struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &created))
	require.True(t, created.Success, recorder.Body.String())

	recorder = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/partnership/", nil)
	GetPartnershipPrograms(c)

	var listed struct {
		Success bool `json:"success"`
		Data    struct {
			Programs []model.PartnershipProgram `json:"programs"`
			Groups   map[string]float64         `json:"groups"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &listed))
	require.True(t, listed.Success, recorder.Body.String())
	require.Len(t, listed.Data.Programs, 1)
	assert.Equal(t, "partner", listed.Data.Programs[0].Group)
	assert.Equal(t, 0.9, listed.Data.Groups["partner"])
	assert.Equal(t, `{}`, setting.UserUsableGroups2JSONString())
}

func TestPartnershipCustomerGetsDedicatedPublicRegistrationOffer(t *testing.T) {
	setupPartnershipControllerTest(t)
	require.NoError(t, model.DB.Model(&model.Option{}).
		Where("key = ?", "GroupRatio").Update("value", `{"partner":0.9,"acme":0.9}`).Error)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"partner":0.9,"acme":0.9}`))
	program := &model.PartnershipProgram{
		Name: "Builder Hub", Code: "builder-hub", Group: "partner",
		GrantQuota: 5000000, GrantLimit: 50, Enabled: true,
	}
	require.NoError(t, model.CreatePartnershipProgram(program))

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/partnership/1/customers", strings.NewReader(`{
		"name":"Acme Vietnam","code":"acme-vietnam","group":"acme","enabled":true
	}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(program.Id)}}
	CreatePartnershipCustomer(c)
	require.Contains(t, recorder.Body.String(), `"success":true`)

	recorder = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/partnership/acme-vietnam", nil)
	c.Params = gin.Params{{Key: "code", Value: "acme-vietnam"}}
	GetPublicPartnershipProgram(c)

	var offer struct {
		Success bool `json:"success"`
		Data    struct {
			Name         string `json:"name"`
			CustomerName string `json:"customer_name"`
			Group        string `json:"group"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &offer))
	require.True(t, offer.Success, recorder.Body.String())
	assert.Equal(t, "Builder Hub", offer.Data.Name)
	assert.Equal(t, "Acme Vietnam", offer.Data.CustomerName)
	assert.Equal(t, "acme", offer.Data.Group)
}

func TestRemovePartnershipCustomerEndpointArchivesCustomer(t *testing.T) {
	setupPartnershipControllerTest(t)
	require.NoError(t, model.DB.Model(&model.Option{}).
		Where("key = ?", "GroupRatio").Update("value", `{"partner":0.9,"acme":0.9}`).Error)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"partner":0.9,"acme":0.9}`))
	program := &model.PartnershipProgram{
		Name: "Builder Hub", Code: "builder-remove", Group: "partner", Enabled: true,
	}
	require.NoError(t, model.CreatePartnershipProgram(program))
	customer := &model.PartnershipCustomer{
		Name: "Acme Vietnam", Code: "acme-remove-api", Group: "acme", Enabled: true,
	}
	require.NoError(t, model.CreatePartnershipCustomer(program.Id, customer))

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/partnership-programs/1/customers/2", nil)
	c.Params = gin.Params{
		{Key: "id", Value: strconv.Itoa(program.Id)},
		{Key: "customerId", Value: strconv.Itoa(customer.Id)},
	}
	RemovePartnershipCustomer(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"success":true`)
	var archived model.PartnershipCustomer
	require.NoError(t, model.DB.First(&archived, customer.Id).Error)
	assert.False(t, archived.Enabled)
	assert.NotZero(t, archived.RemovedAt)
}
