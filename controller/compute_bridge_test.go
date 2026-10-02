/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package controller

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const computeBridgeTestSecret = "0123456789abcdef0123456789abcdef"

// The vector compute-api's client_test.go (TestSignatureVector) signs:
// secret, method, path, timestamp and body exactly as there. The expected hex
// is typed out rather than recomputed so that a change to the canonical string
// on this side fails here instead of silently agreeing with itself.
func TestComputeBridgeVerifiesTheSignatureVectorPinnedByComputeApi(t *testing.T) {
	const pinned = "aba83132084d4c2cfefd1fd39b324e90358975c6b5ade128a7a6b960285eb29c"
	body := `{"key":"sk-test"}`

	mac := hmac.New(sha256.New, []byte(computeBridgeTestSecret))
	mac.Write([]byte("POST\n/api/compute/v1/verify\n1757520000\n" + body))
	require.Equal(t, pinned, hex.EncodeToString(mac.Sum(nil)))

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/compute/v1/verify", strings.NewReader(body))
	c.Request.Header.Set("X-Compute-Timestamp", "1757520000")
	c.Request.Header.Set("X-Compute-Signature", pinned)
	verified, err := verifyComputeBridgeRequest(c, computeBridgeTestSecret, 1757520000)
	require.NoError(t, err)
	assert.Equal(t, body, string(verified))
}

func signComputeBridgeTestRequest(method, path string, ts int64, body string) string {
	mac := hmac.New(sha256.New, []byte(computeBridgeTestSecret))
	mac.Write([]byte(method + "\n" + path + "\n" + strconv.FormatInt(ts, 10) + "\n" + body))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestComputeBridgeRejectsTamperingAndRequestsOutsideTheWindow(t *testing.T) {
	const now = int64(1757520000)
	body := `{"hold_id":"h"}`
	path := "/api/compute/v1/holds/h/release"
	for _, tt := range []struct {
		name      string
		signedTs  int64
		sentBody  string
		sentPath  string
		wantValid bool
	}{
		{"valid", now, body, path, true},
		{"oldest accepted", now - 30, body, path, true},
		{"too old", now - 31, body, path, false},
		{"too far ahead", now + 31, body, path, false},
		{"body changed", now, `{"hold_id":"other"}`, path, false},
		{"path changed", now, body, "/api/compute/v1/holds/h/settle", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, tt.sentPath, strings.NewReader(tt.sentBody))
			c.Request.Header.Set("X-Compute-Timestamp", strconv.FormatInt(tt.signedTs, 10))
			c.Request.Header.Set("X-Compute-Signature", signComputeBridgeTestRequest(http.MethodPost, path, tt.signedTs, body))
			_, err := verifyComputeBridgeRequest(c, computeBridgeTestSecret, now)
			if tt.wantValid {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func newComputeBridgeTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/api/compute/v1", ComputeBridgeAuth())
	group.POST("/verify", ComputeVerify)
	group.POST("/holds", ComputeCreateHold)
	group.POST("/holds/:hold_id/settle", ComputeSettleHold)
	group.POST("/holds/:hold_id/extend", ComputeExtendHold)
	group.POST("/holds/:hold_id/release", ComputeReleaseHold)
	return engine
}

func callComputeBridge(t *testing.T, engine *gin.Engine, path, body string) (int, map[string]any) {
	t.Helper()
	ts := time.Now().Unix()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Compute-Timestamp", strconv.FormatInt(ts, 10))
	request.Header.Set("X-Compute-Signature", signComputeBridgeTestRequest(http.MethodPost, path, ts, body))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	var envelope map[string]any
	if recorder.Body.Len() > 0 {
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope), recorder.Body.String())
	}
	return recorder.Code, envelope
}

func TestComputeBridgeIsDarkWithoutASecret(t *testing.T) {
	engine := newComputeBridgeTestRouter()
	for _, secret := range []string{"", "0123456789abcdef0123456789abcde"} {
		t.Setenv("COMPUTE_BRIDGE_SECRET", secret)
		ts := time.Now().Unix()
		body := `{"key":"sk-test"}`
		request := httptest.NewRequest(http.MethodPost, "/api/compute/v1/verify", strings.NewReader(body))
		request.Header.Set("X-Compute-Timestamp", strconv.FormatInt(ts, 10))
		request.Header.Set("X-Compute-Signature", signComputeBridgeTestRequest(http.MethodPost, "/api/compute/v1/verify", ts, body))
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		assert.Equal(t, http.StatusNotFound, recorder.Code, "secret of length %d", len(secret))
		// compute-api reads only an empty-bodied 404 as bridge_disabled.
		assert.Zero(t, recorder.Body.Len(), "a disabled bridge writes no body at all, got %q", recorder.Body.String())
	}

	t.Setenv("COMPUTE_BRIDGE_SECRET", computeBridgeTestSecret)
	request := httptest.NewRequest(http.MethodPost, "/api/compute/v1/verify", strings.NewReader(`{"key":"sk-test"}`))
	request.Header.Set("X-Compute-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	request.Header.Set("X-Compute-Signature", strings.Repeat("0", 64))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func setupComputeBridgeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Tenant{}, &model.User{}, &model.Token{}, &model.TopUp{}, &model.Log{},
		&model.ComputeHold{}, &model.ComputeSettlement{}, &model.ComputeExtension{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousRedis, previousBatch, previousLogConsume := common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled
	model.DB, model.LOG_DB = db, db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)
	common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = false, false, true
	// InitLogDB with no LOG_SQL_DSN only points LOG_DB at DB and sets the
	// dialect's reserved-word column quoting, which token lookups by key need.
	t.Setenv("LOG_SQL_DSN", "")
	require.NoError(t, model.InitLogDB())
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetMainDatabaseType(previousMainType)
		common.SetLogDatabaseType(previousLogType)
		common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = previousRedis, previousBatch, previousLogConsume
	})
	t.Setenv("COMPUTE_BRIDGE_SECRET", computeBridgeTestSecret)
	return db
}

func createComputeBridgeCustomer(t *testing.T, db *gorm.DB, username string, quota int, tokenRemain int) (*model.User, *model.Tenant, *model.Token) {
	t.Helper()
	user := &model.User{Username: username, DisplayName: username, Quota: quota, AffCode: "aff-" + username,
		Status: common.UserStatusEnabled, Group: "default", Role: common.RoleCommonUser}
	require.NoError(t, db.Create(user).Error)
	tenant, err := model.EnsureTenantForUser(user.Id)
	require.NoError(t, err)
	token := &model.Token{UserId: user.Id, Key: "computebridge" + username, Name: "compute-key",
		Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: tokenRemain}
	require.NoError(t, db.Create(token).Error)
	return user, tenant, token
}

func TestComputeBridgeVerifyResolvesTheBillingIdentity(t *testing.T) {
	db := setupComputeBridgeTestDB(t)
	engine := newComputeBridgeTestRouter()
	user, tenant, token := createComputeBridgeCustomer(t, db, "verifier", 12_500_000, 4_000_000)

	code, envelope := callComputeBridge(t, engine, "/api/compute/v1/verify", `{"key":"sk-`+token.Key+`"}`)
	require.Equal(t, http.StatusOK, code, envelope)
	data := envelope["data"].(map[string]any)
	assert.EqualValues(t, user.Id, data["user_id"])
	assert.EqualValues(t, tenant.Id, data["tenant_id"])
	assert.EqualValues(t, token.Id, data["token_id"])
	assert.Equal(t, "active", data["status"])
	assert.EqualValues(t, 12_500_000, data["remain_quota"], "the balance is the tenant wallet's")
	assert.EqualValues(t, 4_000_000, data["token_remain_quota"])

	code, envelope = callComputeBridge(t, engine, "/api/compute/v1/verify", `{"key":"sk-unknown"}`)
	assert.Equal(t, http.StatusUnauthorized, code)
	assert.Equal(t, false, envelope["success"])
	assert.Equal(t, "invalid_key", envelope["message"])
	assert.Equal(t, "invalid_key", envelope["code"])

	require.NoError(t, model.SuspendTenant(tenant.Id, "unpaid"))
	code, envelope = callComputeBridge(t, engine, "/api/compute/v1/verify", `{"key":"sk-`+token.Key+`"}`)
	assert.Equal(t, http.StatusForbidden, code)
	assert.Equal(t, "suspended", envelope["message"])
	assert.Equal(t, "suspended", envelope["data"].(map[string]any)["status"])
}

func TestComputeBridgeHoldSettleExtendReleaseOverHTTP(t *testing.T) {
	db := setupComputeBridgeTestDB(t)
	engine := newComputeBridgeTestRouter()
	user, tenant, token := createComputeBridgeCustomer(t, db, "jobrunner", 2_000_000, 0)
	require.NoError(t, db.Model(token).Update("unlimited_quota", true).Error)
	expires := strconv.FormatInt(time.Now().Unix()+3600, 10)
	holdBody := `{"hold_id":"hold_1","user_id":` + strconv.Itoa(user.Id) + `,"token_id":` + strconv.Itoa(token.Id) +
		`,"quota":1270000,"job_id":"job_1","sku":"a10g-24gb-x1","expires_at":` + expires + `}`

	code, envelope := callComputeBridge(t, engine, "/api/compute/v1/holds", holdBody)
	require.Equal(t, http.StatusOK, code, envelope)
	assert.EqualValues(t, 730_000, envelope["data"].(map[string]any)["remain_quota"])
	code, _ = callComputeBridge(t, engine, "/api/compute/v1/holds", holdBody)
	require.Equal(t, http.StatusOK, code, "a replayed hold answers 200")

	tooBig := strings.Replace(strings.Replace(holdBody, "hold_1", "hold_2", 1), "1270000", "730001", 1)
	code, envelope = callComputeBridge(t, engine, "/api/compute/v1/holds", tooBig)
	assert.Equal(t, http.StatusPaymentRequired, code, envelope)
	assert.Equal(t, "insufficient_balance", envelope["message"])

	settle := `{"event_id":"ue_1","cumulative_quota":430000,"gpu_seconds":1290,"sku":"a10g-24gb-x1","final":false}`
	code, envelope = callComputeBridge(t, engine, "/api/compute/v1/holds/hold_1/settle", settle)
	require.Equal(t, http.StatusOK, code, envelope)
	assert.EqualValues(t, 430_000, envelope["data"].(map[string]any)["settled_quota"])
	assert.EqualValues(t, 840_000, envelope["data"].(map[string]any)["hold_remaining"])
	code, _ = callComputeBridge(t, engine, "/api/compute/v1/holds/hold_1/settle", settle)
	require.Equal(t, http.StatusOK, code)

	var logs []model.Log
	require.NoError(t, db.Where("type = ?", model.LogTypeConsume).Find(&logs).Error)
	require.Len(t, logs, 1, "a replayed settle must not write a second consume row")
	assert.Equal(t, "compute/a10g-24gb-x1", logs[0].ModelName)
	assert.Equal(t, 430_000, logs[0].Quota)
	assert.Equal(t, tenant.Id, logs[0].TenantId)
	assert.Zero(t, logs[0].PromptTokens)
	assert.Zero(t, logs[0].CompletionTokens)
	assert.Contains(t, logs[0].Other, `"hold_id":"hold_1"`)
	assert.Contains(t, logs[0].Other, `"job_id":"job_1"`)
	var stored model.Tenant
	require.NoError(t, db.First(&stored, tenant.Id).Error)
	assert.Equal(t, 430_000, stored.UsedQuota, "tenant usage counts settled spend, not the hold")

	code, envelope = callComputeBridge(t, engine, "/api/compute/v1/holds/hold_1/settle",
		`{"event_id":"ue_2","cumulative_quota":1270001,"gpu_seconds":4000,"final":false}`)
	assert.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "hold_exceeded", envelope["message"])

	code, envelope = callComputeBridge(t, engine, "/api/compute/v1/holds/hold_1/extend", `{"additional_quota":300000}`)
	assert.Equal(t, http.StatusBadRequest, code, "extend_id is required")
	assert.Equal(t, "invalid_request", envelope["message"])

	extend := `{"extend_id":"ext_01J8AAAAAAAAAAAAAAAAAAAAAA","additional_quota":300000}`
	for range 2 {
		code, envelope = callComputeBridge(t, engine, "/api/compute/v1/holds/hold_1/extend", extend)
		require.Equal(t, http.StatusOK, code, envelope)
		assert.EqualValues(t, 1_570_000, envelope["data"].(map[string]any)["quota"])
		assert.EqualValues(t, 430_000, envelope["data"].(map[string]any)["remain_quota"], "a retried extend reserves once")
	}
	code, envelope = callComputeBridge(t, engine, "/api/compute/v1/holds/hold_1/extend",
		`{"extend_id":"ext_01J8AAAAAAAAAAAAAAAAAAAAAA","additional_quota":300001}`)
	assert.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "idempotency_conflict", envelope["message"])

	code, envelope = callComputeBridge(t, engine, "/api/compute/v1/holds/hold_1/release", `{}`)
	require.Equal(t, http.StatusOK, code, envelope)
	assert.EqualValues(t, 1_140_000, envelope["data"].(map[string]any)["released_quota"])
	assert.EqualValues(t, 1_570_000, envelope["data"].(map[string]any)["remain_quota"])
	code, envelope = callComputeBridge(t, engine, "/api/compute/v1/holds/hold_1/release", `{}`)
	require.Equal(t, http.StatusOK, code)
	assert.EqualValues(t, 1_570_000, envelope["data"].(map[string]any)["remain_quota"], "release is idempotent")

	code, envelope = callComputeBridge(t, engine, "/api/compute/v1/holds/nope/release", `{}`)
	assert.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, "hold_not_found", envelope["message"], "an unknown hold is told apart from a dark bridge by its envelope")
}

func TestComputeBridgeRateLimiterRefillsAtItsRateUpToItsBurst(t *testing.T) {
	start := time.Unix(1757520000, 0)
	limiter := newComputeBridgeRateLimiter(50, 100)
	for i := range 100 {
		require.Zero(t, limiter.take(start), "request %d is within the burst", i)
	}
	wait := limiter.take(start)
	assert.Equal(t, 20*time.Millisecond, wait, "at 50/s the next token is 20ms away")
	assert.Zero(t, limiter.take(start.Add(20*time.Millisecond)))
	assert.Positive(t, limiter.take(start.Add(20*time.Millisecond)))

	idle := start.Add(time.Hour)
	for range 100 {
		require.Zero(t, limiter.take(idle))
	}
	assert.Positive(t, limiter.take(idle), "an idle bucket refills to the burst, not beyond it")
}

func TestComputeBridgeRateLimitEnvironment(t *testing.T) {
	for _, tt := range []struct {
		value       string
		rate, burst float64
	}{
		{"", 50, 100},
		{"20", 20, 40},
		{"20:5", 20, 5},
		{"0.5:1", 0.5, 1},
		{"fast", 50, 100},
		{"0", 50, 100},
		{"-3:10", 50, 100},
		{"20:0", 50, 100},
	} {
		rate, burst := parseComputeBridgeRateLimit(tt.value)
		assert.Equal(t, tt.rate, rate, "rate for %q", tt.value)
		assert.Equal(t, tt.burst, burst, "burst for %q", tt.value)
	}
}

func TestComputeBridgeAnswers429WithRetryAfterOverItsLimit(t *testing.T) {
	setupComputeBridgeTestDB(t)
	engine := newComputeBridgeTestRouter()
	previous := computeBridgeRateLimit()
	computeBridgeLimiter = newComputeBridgeRateLimiter(0.5, 1)
	t.Cleanup(func() { computeBridgeLimiter = previous })

	code, _ := callComputeBridge(t, engine, "/api/compute/v1/verify", `{"key":"sk-unknown"}`)
	assert.Equal(t, http.StatusUnauthorized, code, "the first request is within the burst")

	ts := time.Now().Unix()
	body := `{"key":"sk-unknown"}`
	request := httptest.NewRequest(http.MethodPost, "/api/compute/v1/verify", strings.NewReader(body))
	request.Header.Set("X-Compute-Timestamp", strconv.FormatInt(ts, 10))
	request.Header.Set("X-Compute-Signature", signComputeBridgeTestRequest(http.MethodPost, "/api/compute/v1/verify", ts, body))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusTooManyRequests, recorder.Code)
	assert.Equal(t, "2", recorder.Header().Get("Retry-After"))
	assert.Contains(t, recorder.Body.String(), `"message":"rate_limited"`)
}

func TestComputeBridgeIdsReusedOnAnotherHoldAreIdempotencyConflicts(t *testing.T) {
	db := setupComputeBridgeTestDB(t)
	engine := newComputeBridgeTestRouter()
	user, _, token := createComputeBridgeCustomer(t, db, "reuser", 5_000_000, 0)
	require.NoError(t, db.Model(token).Update("unlimited_quota", true).Error)
	expires := strconv.FormatInt(time.Now().Unix()+3600, 10)
	for _, id := range []string{"hold_a", "hold_b"} {
		body := `{"hold_id":"` + id + `","user_id":` + strconv.Itoa(user.Id) + `,"token_id":` + strconv.Itoa(token.Id) +
			`,"quota":1000000,"job_id":"job_` + id + `","sku":"a10g-24gb-x1","expires_at":` + expires + `}`
		code, envelope := callComputeBridge(t, engine, "/api/compute/v1/holds", body)
		require.Equal(t, http.StatusOK, code, envelope)
	}

	extend := `{"extend_id":"ext_01J8BBBBBBBBBBBBBBBBBBBBBB","additional_quota":100000}`
	code, envelope := callComputeBridge(t, engine, "/api/compute/v1/holds/hold_a/extend", extend)
	require.Equal(t, http.StatusOK, code, envelope)
	code, envelope = callComputeBridge(t, engine, "/api/compute/v1/holds/hold_b/extend", extend)
	assert.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "idempotency_conflict", envelope["message"])

	settle := `{"event_id":"ue_shared","cumulative_quota":1000,"gpu_seconds":3,"final":false}`
	code, envelope = callComputeBridge(t, engine, "/api/compute/v1/holds/hold_a/settle", settle)
	require.Equal(t, http.StatusOK, code, envelope)
	code, envelope = callComputeBridge(t, engine, "/api/compute/v1/holds/hold_b/settle", settle)
	assert.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "idempotency_conflict", envelope["message"])

	assert.EqualValues(t, 5_000_000-2_000_000-100_000, computeEntityQuota(user.Id), "refused reuses move nothing")
}

func TestComputeBridgeRefusedSettlementsRecordNothing(t *testing.T) {
	db := setupComputeBridgeTestDB(t)
	engine := newComputeBridgeTestRouter()
	user, tenant, token := createComputeBridgeCustomer(t, db, "refused", 5_000_000, 0)
	require.NoError(t, db.Model(token).Update("unlimited_quota", true).Error)
	body := `{"hold_id":"hold_r","user_id":` + strconv.Itoa(user.Id) + `,"token_id":` + strconv.Itoa(token.Id) +
		`,"quota":1000000,"job_id":"job_r","sku":"a10g-24gb-x1","expires_at":` + strconv.FormatInt(time.Now().Unix()+3600, 10) + `}`
	code, envelope := callComputeBridge(t, engine, "/api/compute/v1/holds", body)
	require.Equal(t, http.StatusOK, code, envelope)
	code, envelope = callComputeBridge(t, engine, "/api/compute/v1/holds/hold_r/settle", `{"event_id":"ue_r1","cumulative_quota":500,"gpu_seconds":1,"final":false}`)
	require.Equal(t, http.StatusOK, code, envelope)

	for _, tt := range []struct{ body, message string }{
		{`{"event_id":"ue_r2","cumulative_quota":900,"gpu_seconds":2,"sku":"h100-80gb-x8","final":false}`, "sku_mismatch"},
		{`{"event_id":"ue_r3","cumulative_quota":400,"gpu_seconds":2,"final":false}`, "cumulative_quota_regressed"},
		{`{"event_id":"ue_r1","cumulative_quota":600,"gpu_seconds":1,"final":false}`, "idempotency_conflict"},
	} {
		code, envelope = callComputeBridge(t, engine, "/api/compute/v1/holds/hold_r/settle", tt.body)
		assert.Equal(t, http.StatusConflict, code, tt.message)
		assert.Equal(t, tt.message, envelope["message"])
	}

	var consumeRows int64
	require.NoError(t, db.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&consumeRows).Error)
	assert.Equal(t, int64(1), consumeRows, "only the accepted settle is logged")
	var stored model.Tenant
	require.NoError(t, db.First(&stored, tenant.Id).Error)
	assert.Equal(t, 500, stored.UsedQuota)
	hold, err := model.GetComputeHold("hold_r")
	require.NoError(t, err)
	assert.Equal(t, 500, hold.SettledQuota)

	differentQuota := strings.Replace(body, `"quota":1000000`, `"quota":1000001`, 1)
	code, envelope = callComputeBridge(t, engine, "/api/compute/v1/holds", differentQuota)
	assert.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "idempotency_conflict", envelope["message"])
	assert.EqualValues(t, 4_000_000, computeEntityQuota(user.Id))
}

func TestComputeBridgeTellsAStaleTimestampFromABadSignature(t *testing.T) {
	setupComputeBridgeTestDB(t)
	engine := newComputeBridgeTestRouter()
	body := `{"key":"sk-test"}`
	for _, tt := range []struct {
		ts, signature, message string
	}{
		{strconv.FormatInt(time.Now().Unix()-120, 10), "", "invalid_timestamp"},
		{"", "", "invalid_timestamp"},
		{strconv.FormatInt(time.Now().Unix(), 10), strings.Repeat("0", 64), "invalid_signature"},
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/compute/v1/verify", strings.NewReader(body))
		request.Header.Set("X-Compute-Timestamp", tt.ts)
		request.Header.Set("X-Compute-Signature", tt.signature)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		assert.Equal(t, http.StatusUnauthorized, recorder.Code)
		assert.Contains(t, recorder.Body.String(), `"message":"`+tt.message+`"`)
	}
}
