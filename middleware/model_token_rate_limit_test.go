package middleware

import (
	"context"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withTokenLimit(t *testing.T, global int, groups map[string]int) {
	t.Helper()
	prevCount := setting.ModelRequestTokenLimitCount
	prevGroups := setting.ModelRequestTokenLimitGroup
	prevDuration := setting.ModelRequestRateLimitDurationMinutes

	setting.ModelRequestTokenLimitCount = global
	setting.ModelRequestTokenLimitGroup = groups
	setting.ModelRequestRateLimitDurationMinutes = 1

	t.Cleanup(func() {
		setting.ModelRequestTokenLimitCount = prevCount
		setting.ModelRequestTokenLimitGroup = prevGroups
		setting.ModelRequestRateLimitDurationMinutes = prevDuration
	})
}

func tokenRouter(userID int, group string) http.Handler {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("id", userID)
		if group != "" {
			common.SetContextKey(c, constant.ContextKeyUserGroup, group)
		}
		c.Next()
	})
	r.Use(ModelRequestTokenRateLimit())
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func TestACustomerUnderTheirTokenAllowanceIsAdmitted(t *testing.T) {
	useRateLimitMiniRedis(t)
	withTokenLimit(t, 10_000, nil)
	r := tokenRouter(501, "")

	model.RecordUserTokens(context.Background(), 501, 9_000)
	assert.Equal(t, http.StatusOK, fire(r), "9k of a 10k allowance must pass")
}

func TestACustomerOverTheirTokenAllowanceIsRefused(t *testing.T) {
	useRateLimitMiniRedis(t)
	withTokenLimit(t, 10_000, nil)
	r := tokenRouter(502, "")

	model.RecordUserTokens(context.Background(), 502, 10_000)

	code, body := fireBody(r)
	require.Equal(t, http.StatusTooManyRequests, code)
	assert.Contains(t, body, "Token rate limit reached")
	// Same rule as the request-count message: customers read this.
	assert.False(t, containsHan(body), "message must not be Chinese: %s", body)
}

func TestATokenLimitOfZeroMeansUnlimited(t *testing.T) {
	useRateLimitMiniRedis(t)
	withTokenLimit(t, 0, nil)
	r := tokenRouter(503, "")

	model.RecordUserTokens(context.Background(), 503, 50_000_000)
	assert.Equal(t, http.StatusOK, fire(r), "0 disables the check, as elsewhere")
}

func TestAGroupTokenOverrideBeatsTheGlobalDefault(t *testing.T) {
	useRateLimitMiniRedis(t)
	withTokenLimit(t, 1_000, map[string]int{"Vip User": 100_000})

	vip := tokenRouter(504, "Vip User")
	model.RecordUserTokens(context.Background(), 504, 50_000)
	assert.Equal(t, http.StatusOK, fire(vip), "VIP is well inside its override")

	// An unlisted group falls back to the global default, so a new customer is
	// never throttled merely because nobody added them to the map.
	plain := tokenRouter(505, "SomeNewCustomer")
	model.RecordUserTokens(context.Background(), 505, 5_000)
	assert.Equal(t, http.StatusTooManyRequests, fire(plain))
}

func TestTokensAreCountedPerCustomerNotGlobally(t *testing.T) {
	useRateLimitMiniRedis(t)
	withTokenLimit(t, 10_000, nil)

	noisy := tokenRouter(506, "")
	model.RecordUserTokens(context.Background(), 506, 20_000)
	require.Equal(t, http.StatusTooManyRequests, fire(noisy))

	// The whole point: one customer burning their allowance must not touch
	// anybody else's.
	quiet := tokenRouter(507, "")
	assert.Equal(t, http.StatusOK, fire(quiet), "a different customer is unaffected")
}
