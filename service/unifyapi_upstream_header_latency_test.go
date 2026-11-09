package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// UNIFYAPI-FORK: upstream new-api records no upstream-side timing at all, so a
// request that spent 40s waiting for a first token and one that spent 40s
// generating were indistinguishable in the log. Measured 2026-11-09 against the
// providers we buy from: they withhold response headers while the model reasons
// (luna pool 5.0-6.8s to headers, Anthropic pool 2.2-3.6s, first body chunk only
// 0.27-1.25s later), so on a streaming relay this number IS the upstream's
// time-to-first-token.

func latencyCtx(t *testing.T, ms int) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if ms != 0 {
		common.SetContextKey(c, constant.ContextKeyUpstreamHeaderMs, ms)
	}
	return c
}

func TestUpstreamHeaderLatencyNestsUnderAdminInfo(t *testing.T) {
	other := map[string]interface{}{"model_ratio": 1.0}
	attachUpstreamHeaderLatency(latencyCtx(t, 6421), other)

	adminInfo, ok := other["admin_info"].(map[string]interface{})
	require.True(t, ok, "admin_info should be created when absent")
	require.Equal(t, 6421, adminInfo["upstream_header_ms"])
	require.Equal(t, 1.0, other["model_ratio"], "existing keys must survive")
}

func TestUpstreamHeaderLatencyKeepsExistingAdminInfo(t *testing.T) {
	other := map[string]interface{}{
		"admin_info": map[string]interface{}{"use_channel": []string{"216"}},
	}
	attachUpstreamHeaderLatency(latencyCtx(t, 12), other)

	adminInfo := other["admin_info"].(map[string]interface{})
	require.Equal(t, []string{"216"}, adminInfo["use_channel"],
		"must not clobber what the caller already put under admin_info")
	require.Equal(t, 12, adminInfo["upstream_header_ms"])
}

// Non-relay paths never set the key. Writing a zero there would be worse than
// writing nothing: it would read as "the upstream answered instantly".
func TestUpstreamHeaderLatencyIsANoOpWhenUnset(t *testing.T) {
	other := map[string]interface{}{}
	attachUpstreamHeaderLatency(latencyCtx(t, 0), other)
	require.NotContains(t, other, "admin_info")

	attachUpstreamHeaderLatency(nil, other)
	require.NotContains(t, other, "admin_info")

	require.NotPanics(t, func() { attachUpstreamHeaderLatency(latencyCtx(t, 5), nil) })
}
