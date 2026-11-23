/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// A per-user limit that is refused on save must leave the stored row as it
// was. This drives the real handler against a real table and reads the row
// back, because "the API answered success:false" has been true before of a
// value that was written anyway.
func TestARefusedUserLimitIsNotWrittenToTheChannel(t *testing.T) {
	originalDB := model.DB
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	model.DB = db
	t.Cleanup(func() {
		model.DB = originalDB
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	const stored = `{"user_rate_limit":{"rpm":20},"user_limit_bucket":"mythos-direct"}`
	require.NoError(t, db.Create(&model.Channel{
		Id: 4401, Type: constant.ChannelTypeOpenAI, Key: "k", Name: "guarded", Status: common.ChannelStatusEnabled,
		Models: "claude-mythos-5", Group: "default", OtherSettings: stored,
	}).Error)

	for _, refused := range []string{
		`{"user_rate_limit":{"rpm":-1}}`,
		`{"user_rate_limit":{"daily_tokens":"many"}}`,
		`{"user_rate_limit":{"rpm":1},"user_limit_bucket":"two words"}`,
		`{"user_rate_limit":{"rpm":1}`,
	} {
		body, err := json.Marshal(map[string]any{"id": 4401, "name": "guarded", "settings": refused})
		require.NoError(t, err)
		gin.SetMode(gin.TestMode)
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPut, "/api/channel/", bytes.NewReader(body))
		ctx.Request.Header.Set("Content-Type", "application/json")

		UpdateChannel(ctx)

		var answer struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
		}
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &answer), recorder.Body.String())
		assert.False(t, answer.Success, "%s must be refused", refused)

		var row model.Channel
		require.NoError(t, db.First(&row, 4401).Error)
		assert.Equal(t, stored, row.OtherSettings, "after refusing %s the stored settings are untouched", refused)
	}
}
