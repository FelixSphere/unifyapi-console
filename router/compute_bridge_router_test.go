/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every bridge call comes from compute-api's one address, so the per-IP global
// API limiter would cap all compute billing. With that limiter squeezed to one
// request per window, the bridge still answers (here: 404, because it is dark)
// while an ordinary /api route from the same address is throttled.
func TestComputeBridgeIsOutsideTheGlobalAPILimiter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousEnable, previousNum, previousDuration := common.GlobalApiRateLimitEnable, common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration
	previousRedis := common.RedisEnabled
	common.GlobalApiRateLimitEnable, common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration = true, 1, 180
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.GlobalApiRateLimitEnable, common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration = previousEnable, previousNum, previousDuration
		common.RedisEnabled = previousRedis
	})
	t.Setenv("COMPUTE_BRIDGE_SECRET", "")

	engine := gin.New()
	SetApiRouter(engine)
	serve := func(method, path string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(method, path, nil)
		request.RemoteAddr = "198.51.100.7:4000"
		engine.ServeHTTP(recorder, request)
		return recorder
	}

	for range 3 {
		dark := serve(http.MethodPost, "/api/compute/v1/verify")
		require.Equal(t, http.StatusNotFound, dark.Code)
		// Through the real router too: compute-api reads only an empty 404 as
		// bridge_disabled, so no middleware may add a body.
		require.Zero(t, dark.Body.Len(), "got %q", dark.Body.String())
	}
	serve(http.MethodGet, "/api/uptime/status")
	assert.Equal(t, http.StatusTooManyRequests, serve(http.MethodGet, "/api/uptime/status").Code,
		"control: the global limiter is active for ordinary /api routes in this setup")
}
