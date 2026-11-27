/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// UNIFYAPI-FORK: a request refused before the relay must show in its owner's
// usage log (customer report 2026-11-27: an IP allow-list 403 and a
// model_not_found 503 that never appeared on their log page).

const (
	rejectedLogOwnerKey   = "rejectedlogownerkeyrejectedlogownerkeyrejectedlo"
	rejectedLogFencedKey  = "rejectedlogfencedkeyrejectedlogfencedkeyrejecte"
	rejectedLogAllowedIP  = "10.0.0.1"
	rejectedLogStrangerIP = "45.61.224.14"
)

type rejectedLogFixture struct {
	db     *gorm.DB
	userId int
	engine *gin.Engine
}

func setupRejectedLogFixture(t *testing.T) *rejectedLogFixture {
	t.Helper()
	require.NoError(t, i18n.Init())
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.User{}, &model.Token{}, &model.Tenant{},
		&model.CustomerWallet{}, &model.Channel{}, &model.Ability{}, &model.Log{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousRedis, previousCache := common.RedisEnabled, common.MemoryCacheEnabled
	previousOptions, previousRatios := common.OptionMap, ratio_setting.GroupRatio2JSONString()
	previousErrorLog, previousWrite := constant.ErrorLogEnabled, runRejectedRequestLogWrite
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.RedisEnabled, common.MemoryCacheEnabled = false, false
	common.OptionMap = map[string]string{}
	constant.ErrorLogEnabled = true
	runRejectedRequestLogWrite = func(write func()) { write() }
	rejectedRequestLogs = &rejectedRequestLogLimiter{}
	t.Setenv("LOG_SQL_DSN", "")
	require.NoError(t, model.InitLogDB())
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousRatios))
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetMainDatabaseType(previousType)
		common.SetLogDatabaseType(previousLogType)
		common.RedisEnabled, common.MemoryCacheEnabled = previousRedis, previousCache
		common.OptionMap = previousOptions
		constant.ErrorLogEnabled, runRejectedRequestLogWrite = previousErrorLog, previousWrite
		rejectedRequestLogs = &rejectedRequestLogLimiter{}
		_ = sqlDB.Close()
	})

	owner := &model.User{Username: "keyowner", Password: "not-a-real-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AffCode: "keyowner", AuthVersion: 1}
	require.NoError(t, db.Create(owner).Error)
	allowList := rejectedLogAllowedIP
	require.NoError(t, db.Create(&model.Token{UserId: owner.Id, Key: rejectedLogOwnerKey, Name: "open", Status: common.TokenStatusEnabled,
		ExpiredTime: -1, UnlimitedQuota: true}).Error)
	require.NoError(t, db.Create(&model.Token{UserId: owner.Id, Key: rejectedLogFencedKey, Name: "fenced", Status: common.TokenStatusEnabled,
		ExpiredTime: -1, UnlimitedQuota: true, AllowIps: &allowList}).Error)

	// gpt-4o is served in the owner's group. claude-held is served, but only
	// in a group the owner cannot use: that is the 503 model_not_found shape.
	priority, weight := int64(0), uint(100)
	for _, ch := range []struct {
		id           int
		group, model string
	}{{9301, "default", "gpt-4o"}, {9302, "elsewhere", "claude-held"}} {
		require.NoError(t, db.Create(&model.Channel{Id: ch.id, Type: constant.ChannelTypeOpenAI, Key: "upstream-key", Status: common.ChannelStatusEnabled,
			Name: "channel", Weight: &weight, Models: ch.model, Group: ch.group, Priority: &priority}).Error)
		require.NoError(t, db.Create(&model.Ability{Group: ch.group, Model: ch.model, ChannelId: ch.id, Enabled: true,
			Priority: &priority, Weight: weight}).Error)
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(RequestId())
	engine.GET("/v1/models", TokenAuth(), func(c *gin.Context) { c.Status(http.StatusOK) })
	engine.POST("/v1/chat/completions", TokenAuth(), Distribute(), func(c *gin.Context) {
		// Stands in for controller.Relay. "relay-fails" is an error raised
		// after the hand-off, which the relay logs itself.
		if strings.Contains(c.GetHeader("X-Test"), "relay-fails") {
			c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": "upstream failed"}})
			return
		}
		c.Status(http.StatusOK)
	})
	return &rejectedLogFixture{db: db, userId: owner.Id, engine: engine}
}

func (f *rejectedLogFixture) send(t *testing.T, method, path, key, clientIP, body, testHeader string) int {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = clientIP + ":40000"
	request.Header.Set("Authorization", "Bearer sk-"+key)
	request.Header.Set("Content-Type", "application/json")
	if testHeader != "" {
		request.Header.Set("X-Test", testHeader)
	}
	recorder := httptest.NewRecorder()
	f.engine.ServeHTTP(recorder, request)
	return recorder.Code
}

func (f *rejectedLogFixture) logs(t *testing.T) []model.Log {
	t.Helper()
	var rows []model.Log
	require.NoError(t, f.db.Order("id").Find(&rows).Error)
	return rows
}

func TestAnIPAllowListRefusalIsInTheKeyOwnersLog(t *testing.T) {
	f := setupRejectedLogFixture(t)

	status := f.send(t, http.MethodPost, "/v1/chat/completions", rejectedLogFencedKey, rejectedLogStrangerIP,
		`{"model":"gpt-4o","messages":[]}`, "")
	require.Equal(t, http.StatusForbidden, status)

	rows := f.logs(t)
	require.Len(t, rows, 1)
	row := rows[0]
	assert.Equal(t, model.LogTypeError, row.Type)
	assert.Equal(t, f.userId, row.UserId)
	assert.Equal(t, "keyowner", row.Username, "looked up: the allow list refuses before auth loads the user")
	assert.Equal(t, "fenced", row.TokenName)
	assert.NotZero(t, row.TokenId)
	assert.Equal(t, 0, row.Quota, "a refusal is never billed")
	assert.Equal(t, 0, row.ChannelId)
	assert.Equal(t, "您的 IP 不在令牌允许访问的列表中 (client IP: "+rejectedLogStrangerIP+")", row.Content)
	assert.Equal(t, rejectedLogStrangerIP, row.Ip)
	assert.NotEmpty(t, row.RequestId)
	assert.NotContains(t, row.Content+row.Other, rejectedLogFencedKey, "the key is never written")

	other, err := common.StrToMap(row.Other)
	require.NoError(t, err)
	assert.Equal(t, true, other["rejected"])
	assert.EqualValues(t, http.StatusForbidden, other["status_code"])
	assert.Equal(t, "access_denied", other["error_code"])
	assert.Equal(t, "/v1/chat/completions", other["request_path"])
}

func TestAModelNotFoundRefusalIsLoggedWithTheModel(t *testing.T) {
	f := setupRejectedLogFixture(t)

	status := f.send(t, http.MethodPost, "/v1/chat/completions", rejectedLogOwnerKey, rejectedLogAllowedIP,
		`{"model":"claude-held","messages":[]}`, "")
	require.Equal(t, http.StatusServiceUnavailable, status)

	rows := f.logs(t)
	require.Len(t, rows, 1)
	assert.Equal(t, model.LogTypeError, rows[0].Type)
	assert.Equal(t, "claude-held", rows[0].ModelName)
	assert.Equal(t, "open", rows[0].TokenName)
	assert.Equal(t, "default", rows[0].Group)
	assert.NotContains(t, rows[0].Content, "X-Oneapi-Request-Id", "the stored message carries no request id suffix")
	other, err := common.StrToMap(rows[0].Other)
	require.NoError(t, err)
	assert.Equal(t, true, other["rejected"])
	assert.EqualValues(t, http.StatusServiceUnavailable, other["status_code"])
	assert.Equal(t, "model_not_found", other["error_code"])
}

func TestRequestsThatWriteNoRefusalRow(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, key, body, testHeader string
		status                                    int
	}{
		// Nobody can be shown this one, and writing it would let anyone put
		// rows in someone's log.
		{"an unknown key", http.MethodPost, "/v1/chat/completions", "notakeynotakeynotakeynotakeynotakeynotakeynotake",
			`{"model":"gpt-4o"}`, "", http.StatusUnauthorized},
		{"a request that succeeds", http.MethodPost, "/v1/chat/completions", rejectedLogOwnerKey,
			`{"model":"gpt-4o","messages":[]}`, "", http.StatusOK},
		{"a route with no Distribute that succeeds", http.MethodGet, "/v1/models", rejectedLogOwnerKey,
			"", "", http.StatusOK},
		// controller/relay.go writes this one; a second row would double it.
		{"an error after the relay took the request", http.MethodPost, "/v1/chat/completions", rejectedLogOwnerKey,
			`{"model":"gpt-4o","messages":[]}`, "relay-fails", http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setupRejectedLogFixture(t)
			require.Equal(t, tc.status, f.send(t, tc.method, tc.path, tc.key, rejectedLogAllowedIP, tc.body, tc.testHeader))
			assert.Empty(t, f.logs(t))
		})
	}
}

func TestRefusalRowsAreCappedPerTokenPerMinute(t *testing.T) {
	limiter := &rejectedRequestLogLimiter{}
	minute := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)

	for i := 0; i < rejectedRequestLogsPerTokenPerMinute; i++ {
		require.True(t, limiter.allow(7, minute.Add(time.Duration(i)*time.Second/2)), "row %d is within the cap", i+1)
	}
	assert.False(t, limiter.allow(7, minute.Add(59*time.Second)), "row 61 in the same minute is dropped")
	assert.True(t, limiter.allow(8, minute.Add(59*time.Second)), "another token has its own allowance")
	assert.True(t, limiter.allow(7, minute.Add(time.Minute)), "the next minute starts again")
	assert.Len(t, limiter.counts, 1, "only the current minute's tokens are held")
}

func TestTheCapAppliesToRealRefusals(t *testing.T) {
	f := setupRejectedLogFixture(t)
	// Pin the clock so the minute cannot turn partway through the loop.
	now := time.Date(2026, 11, 27, 10, 0, 30, 0, time.UTC)
	previousClock := rejectedRequestLogClock
	rejectedRequestLogClock = func() time.Time { return now }
	t.Cleanup(func() { rejectedRequestLogClock = previousClock })
	rejectedRequestLogs = &rejectedRequestLogLimiter{window: now.Unix() / 60, counts: map[int]int{}}
	var fenced model.Token
	require.NoError(t, f.db.Where("name = ?", "fenced").First(&fenced).Error)
	rejectedRequestLogs.counts[fenced.Id] = rejectedRequestLogsPerTokenPerMinute - 1

	for i := 0; i < 3; i++ {
		require.Equal(t, http.StatusForbidden, f.send(t, http.MethodPost, "/v1/chat/completions", rejectedLogFencedKey,
			rejectedLogStrangerIP, `{"model":"gpt-4o"}`, ""))
	}
	assert.Len(t, f.logs(t), 1, "only the one request left under the cap is written")
}

func TestARejectedRequestLogWriteWithoutADatabaseDoesNotPanic(t *testing.T) {
	previousLogDB := model.LOG_DB
	model.LOG_DB = nil
	t.Cleanup(func() { model.LOG_DB = previousLogDB })
	assert.NotPanics(t, func() { model.RecordRejectedRequestLog(&model.Log{UserId: 1}) })
}
