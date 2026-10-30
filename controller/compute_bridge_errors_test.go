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
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Contract revision 2026-10-02 (unify-compute docs/spec/console-billing-bridge.md).

func serveComputeBridge(engine *gin.Engine, path, body, ts, signature string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Compute-Timestamp", ts)
	request.Header.Set("X-Compute-Signature", signature)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func computeBridgeMessage(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope), recorder.Body.String())
	assert.Equal(t, false, envelope["success"])
	message, _ := envelope["message"].(string)
	assert.Equal(t, message, envelope["code"])
	return message
}

// An oversized body is 413 payload_too_large whether or not it is signed, and
// whatever its timestamp: before this, it was refused as 401 invalid_signature
// and sent the caller looking for a wrong secret.
func TestComputeBridgeRefusesABodyOver32KiBAs413BeforeTheSignature(t *testing.T) {
	t.Setenv("COMPUTE_BRIDGE_SECRET", computeBridgeTestSecret)
	engine := newComputeBridgeTestRouter()
	path := "/api/compute/v1/verify"
	now := time.Now().Unix()
	tooBig := `{"key":"` + strings.Repeat("a", 32768) + `"}`
	require.Greater(t, len(tooBig), 32768)

	for _, tt := range []struct {
		name, ts, signature string
	}{
		{"correctly signed", strconv.FormatInt(now, 10), signComputeBridgeTestRequest(http.MethodPost, path, now, tooBig)},
		{"bad signature", strconv.FormatInt(now, 10), strings.Repeat("0", 64)},
		{"stale timestamp", strconv.FormatInt(now-120, 10), ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			recorder := serveComputeBridge(engine, path, tooBig, tt.ts, tt.signature)
			assert.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
			assert.Equal(t, "payload_too_large", computeBridgeMessage(t, recorder))
		})
	}
}

// Exactly 32 KiB is still accepted for size, so it reaches the signature check.
func TestComputeBridgeAcceptsABodyOfExactly32KiBForSize(t *testing.T) {
	t.Setenv("COMPUTE_BRIDGE_SECRET", computeBridgeTestSecret)
	engine := newComputeBridgeTestRouter()
	atLimit := `{"key":"` + strings.Repeat("a", 32768-len(`{"key":""}`)) + `"}`
	require.Len(t, atLimit, 32768)

	recorder := serveComputeBridge(engine, "/api/compute/v1/verify", atLimit, strconv.FormatInt(time.Now().Unix(), 10), strings.Repeat("0", 64))
	assert.Equal(t, http.StatusUnauthorized, recorder.Code)
	assert.Equal(t, "invalid_signature", computeBridgeMessage(t, recorder))
}

// The signature still covers the exact bytes after the size check buffered them.
func TestComputeBridgeSignatureStillCoversTheBodyAfterTheSizeCheck(t *testing.T) {
	setupComputeBridgeTestDB(t)
	engine := newComputeBridgeTestRouter()
	path := "/api/compute/v1/holds/hold_absent/release"
	now := time.Now().Unix()
	body := `{}`

	recorder := serveComputeBridge(engine, path, body, strconv.FormatInt(now, 10), signComputeBridgeTestRequest(http.MethodPost, path, now, body))
	assert.Equal(t, http.StatusNotFound, recorder.Code)
	assert.Equal(t, "hold_not_found", computeBridgeMessage(t, recorder))

	recorder = serveComputeBridge(engine, path, `{ }`, strconv.FormatInt(now, 10), signComputeBridgeTestRequest(http.MethodPost, path, now, body))
	assert.Equal(t, http.StatusUnauthorized, recorder.Code)
	assert.Equal(t, "invalid_signature", computeBridgeMessage(t, recorder))
}

// A failing database is 500 internal_error; the detail stays in the log.
func TestComputeBridgeAnswersInternalErrorWhenTheDatabaseFails(t *testing.T) {
	db := setupComputeBridgeTestDB(t)
	engine := newComputeBridgeTestRouter()
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	code, envelope := callComputeBridge(t, engine, "/api/compute/v1/holds/hold_x/release", `{}`)
	assert.Equal(t, http.StatusInternalServerError, code)
	assert.Equal(t, "internal_error", envelope["message"])
	assert.Equal(t, "internal_error", envelope["code"])
	assert.Len(t, envelope, 3, "no detail beyond success, message and code: %v", envelope)
}

// A panic inside a bridge handler is answered by the bridge, not by the
// server-wide recovery whose body carries the panic text and no machine code.
func TestComputeBridgeAnswersInternalErrorOnAPanic(t *testing.T) {
	t.Setenv("COMPUTE_BRIDGE_SECRET", computeBridgeTestSecret)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(gin.CustomRecovery(func(c *gin.Context, err any) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Panic detected", "type": "new_api_panic"}})
	}))
	engine.Group("/api/compute/v1", ComputeBridgeAuth()).POST("/verify", func(c *gin.Context) {
		panic("secret detail that must stay in the log")
	})
	path := "/api/compute/v1/verify"
	now := time.Now().Unix()
	body := `{"key":"sk-test"}`

	recorder := serveComputeBridge(engine, path, body, strconv.FormatInt(now, 10), signComputeBridgeTestRequest(http.MethodPost, path, now, body))
	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
	assert.Equal(t, "internal_error", computeBridgeMessage(t, recorder))
	assert.NotContains(t, recorder.Body.String(), "secret detail")
}
