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
	require.NoError(t, db.AutoMigrate(&model.PartnershipProgram{}, &model.PartnershipEnrollment{}))
	previousDB := model.DB
	previousRatios := ratio_setting.GroupRatio2JSONString()
	previousUsable := setting.UserUsableGroups2JSONString()
	model.DB = db
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"partner":0.9}`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{}`))
	t.Cleanup(func() {
		model.DB = previousDB
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
