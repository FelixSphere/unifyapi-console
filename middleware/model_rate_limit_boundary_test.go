package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These pin the behaviour the production seed (seed-rate-limits.sql) depends
// on. The limiter had never been switched on before, so none of it was covered:
// the first time anyone would have learned where the boundary sits is when a
// customer hit it.

func withModelRateLimitSettings(t *testing.T, enabled bool, durationMinutes, total, success int) {
	t.Helper()
	prevEnabled := setting.ModelRequestRateLimitEnabled
	prevDuration := setting.ModelRequestRateLimitDurationMinutes
	prevCount := setting.ModelRequestRateLimitCount
	prevSuccess := setting.ModelRequestRateLimitSuccessCount
	prevGroup := setting.ModelRequestRateLimitGroup

	setting.ModelRequestRateLimitEnabled = enabled
	setting.ModelRequestRateLimitDurationMinutes = durationMinutes
	setting.ModelRequestRateLimitCount = total
	setting.ModelRequestRateLimitSuccessCount = success
	setting.ModelRequestRateLimitGroup = map[string][2]int{}

	t.Cleanup(func() {
		setting.ModelRequestRateLimitEnabled = prevEnabled
		setting.ModelRequestRateLimitDurationMinutes = prevDuration
		setting.ModelRequestRateLimitCount = prevCount
		setting.ModelRequestRateLimitSuccessCount = prevSuccess
		setting.ModelRequestRateLimitGroup = prevGroup
	})
}

// routerForUser stands in for the auth middleware, which is what puts the user
// id and group on the context before the limiter runs.
func routerForUser(userID int, group string, downstreamStatus int) http.Handler {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("id", userID)
		if group != "" {
			common.SetContextKey(c, constant.ContextKeyUserGroup, group)
		}
		c.Next()
	})
	r.Use(ModelRequestRateLimit())
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(downstreamStatus) })
	return r
}

func fire(router http.Handler) int {
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	return rec.Code
}

func TestRequestsUnderTheLimitAreNeverThrottled(t *testing.T) {
	useRateLimitMiniRedis(t)
	withModelRateLimitSettings(t, true, 1, 0, 5)
	router := routerForUser(101, "", http.StatusOK)

	// The whole point of a generous limit is that ordinary traffic never sees a
	// 429. If this regresses, customers get throttled for no reason.
	for i := 1; i <= 5; i++ {
		assert.Equal(t, http.StatusOK, fire(router), "request %d of 5 should pass", i)
	}
}

func TestTheSuccessLimitBlocksTheRequestAfterTheAllowance(t *testing.T) {
	useRateLimitMiniRedis(t)
	withModelRateLimitSettings(t, true, 1, 0, 3)
	router := routerForUser(102, "", http.StatusOK)

	for i := 1; i <= 3; i++ {
		require.Equal(t, http.StatusOK, fire(router), "request %d is within the allowance", i)
	}
	assert.Equal(t, http.StatusTooManyRequests, fire(router),
		"the request after the allowance must be refused")
}

func TestZeroSuccessCountMeansUnlimited(t *testing.T) {
	useRateLimitMiniRedis(t)
	withModelRateLimitSettings(t, true, 1, 0, 0)
	router := routerForUser(103, "", http.StatusOK)

	// checkRedisRateLimit returns early when maxCount is 0. Worth pinning: it is
	// the difference between "0 disables the check" and "0 blocks everything",
	// and the seed comment has been wrong about it once.
	for i := 1; i <= 20; i++ {
		require.Equal(t, http.StatusOK, fire(router), "request %d must not be limited", i)
	}
}

func TestDisablingTheFeatureBypassesTheLimiterEntirely(t *testing.T) {
	useRateLimitMiniRedis(t)
	withModelRateLimitSettings(t, false, 1, 0, 1)
	router := routerForUser(104, "", http.StatusOK)

	// The rollback path. If this regresses, the off switch does not switch off.
	for i := 1; i <= 10; i++ {
		require.Equal(t, http.StatusOK, fire(router), "request %d with limiting disabled", i)
	}
}

func TestAGroupOverrideRaisesTheAllowanceForThatGroup(t *testing.T) {
	useRateLimitMiniRedis(t)
	withModelRateLimitSettings(t, true, 1, 0, 2)
	setting.ModelRequestRateLimitGroup = map[string][2]int{
		"Vip User": {0, 6},
	}

	vip := routerForUser(105, "Vip User", http.StatusOK)
	for i := 1; i <= 6; i++ {
		require.Equal(t, http.StatusOK, fire(vip), "VIP request %d should use the override", i)
	}
	assert.Equal(t, http.StatusTooManyRequests, fire(vip))

	// A group with no override still gets the global default of 2.
	plain := routerForUser(106, "Chinhin", http.StatusOK)
	require.Equal(t, http.StatusOK, fire(plain))
	require.Equal(t, http.StatusOK, fire(plain))
	assert.Equal(t, http.StatusTooManyRequests, fire(plain),
		"an unlisted group falls back to the global default")
}

func TestFailedRequestsDoNotConsumeTheSuccessAllowance(t *testing.T) {
	useRateLimitMiniRedis(t)
	withModelRateLimitSettings(t, true, 1, 0, 2)
	failing := routerForUser(107, "", http.StatusBadGateway)

	// Only responses under 400 are recorded. A customer whose upstream is
	// erroring must not also lose their request budget.
	for i := 1; i <= 10; i++ {
		require.Equal(t, http.StatusBadGateway, fire(failing), "failure %d", i)
	}

	succeeding := routerForUser(107, "", http.StatusOK)
	assert.Equal(t, http.StatusOK, fire(succeeding),
		"the success budget should be untouched by failures")
}
