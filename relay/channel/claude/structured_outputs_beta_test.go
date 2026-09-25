package claude

import (
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func betaHeaderFor(t *testing.T, callerBeta string, info *relaycommon.RelayInfo) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	if callerBeta != "" {
		c.Request.Header.Set("anthropic-beta", callerBeta)
	}
	outbound := http.Header{}
	CommonClaudeHeadersOperation(c, &outbound, info)
	return outbound.Get("anthropic-beta")
}

// Anthropic accepts output_format and ignores it unless the beta is requested,
// so the caller gets HTTP 200 and prose. Mapping response_format onto
// output_format is inert on its own; this is what makes it take effect.
func TestStructuredOutputsRequestsTheBeta(t *testing.T) {
	got := betaHeaderFor(t, "", &relaycommon.RelayInfo{ClaudeStructuredOutputs: true})
	assert.Contains(t, got, model_setting.GetClaudeSettings().StructuredOutputsBeta)
}

// anthropic-beta is a comma-separated list. Setting rather than merging would
// silently drop a beta the caller asked for.
func TestStructuredOutputsBetaMergesWithTheCallersOwn(t *testing.T) {
	got := betaHeaderFor(t, "prompt-caching-2024-07-31", &relaycommon.RelayInfo{ClaudeStructuredOutputs: true})
	assert.Contains(t, got, "prompt-caching-2024-07-31", "the caller's beta must survive")
	assert.Contains(t, got, model_setting.GetClaudeSettings().StructuredOutputsBeta)
}

// A request that never asked for structured output must not be opted into a
// beta: it changes how the upstream bills and behaves.
func TestNoStructuredOutputsMeansNoBeta(t *testing.T) {
	assert.Empty(t, betaHeaderFor(t, "", &relaycommon.RelayInfo{}))
	assert.Equal(t, "prompt-caching-2024-07-31",
		betaHeaderFor(t, "prompt-caching-2024-07-31", &relaycommon.RelayInfo{}))
}

// The flag is set from the outbound request, on both entry paths.
func TestMarkStructuredOutputsFollowsOutputFormat(t *testing.T) {
	withFormat := &dto.ClaudeRequest{OutputFormat: []byte(`{"type":"json_schema","schema":{}}`)}
	without := &dto.ClaudeRequest{}

	info := &relaycommon.RelayInfo{}
	markStructuredOutputs(info, withFormat)
	assert.True(t, info.ClaudeStructuredOutputs)

	info = &relaycommon.RelayInfo{}
	markStructuredOutputs(info, without)
	assert.False(t, info.ClaudeStructuredOutputs)

	require.NotPanics(t, func() {
		markStructuredOutputs(nil, withFormat)
		markStructuredOutputs(&relaycommon.RelayInfo{}, nil)
	})
}
