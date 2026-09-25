package gemini

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Reproduces what production did on 2026-09-25 (channels 124 and 129, type 24):
// an OpenAI-format caller asked gemini-2.5-flash-lite / gemini-3.1-flash-lite-
// preview to reason and got 5 output tokens and no thoughts, streaming and
// non-streaming, while OpenRouter returned 280-1400 reasoning tokens for the
// identical request. Evidence: perf/eval/results-thinking/thinking2-20260925T0659Z.json.gz.

func uintP(v uint) *uint { return &v }

func convertChatToGemini(t *testing.T, req dto.GeneralOpenAIRequest) *dto.GeminiChatRequest {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{OriginModelName: req.Model,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: req.Model}}
	out, err := (&Adaptor{}).ConvertOpenAIRequest(c, info, &req)
	require.NoError(t, err)
	g, ok := out.(*dto.GeminiChatRequest)
	require.True(t, ok, "got %T", out)
	return g
}

func geminiChat(model string) dto.GeneralOpenAIRequest {
	return dto.GeneralOpenAIRequest{Model: model, MaxTokens: uintP(4096),
		Messages: []dto.Message{{Role: "user", Content: "hi"}}}
}

func TestGeminiReasoning_EffortOnPlainModelNameNowThinks(t *testing.T) {
	for _, model := range []string{"gemini-2.5-flash-lite", "gemini-3.1-flash-lite-preview", "gemini-2.5-flash", "gemini-flash-latest"} {
		req := geminiChat(model)
		req.ReasoningEffort = "medium"
		tc := convertChatToGemini(t, req).GenerationConfig.ThinkingConfig
		require.NotNil(t, tc, model)
		require.NotNil(t, tc.ThinkingBudget, model)
		assert.Equal(t, flashMaxBudget*50/100, *tc.ThinkingBudget, model)
		assert.True(t, tc.IncludeThoughts, "thoughts are returned, as OpenRouter does: %s", model)
		assert.Empty(t, tc.ThinkingLevel, "2.5 rejects thinkingLevel; budget is the one safe shape: %s", model)
	}
}

func TestGeminiReasoning_OpenRouterReasoningObjectIsRead(t *testing.T) {
	req := geminiChat("gemini-2.5-flash-lite")
	req.Reasoning = json.RawMessage(`{"effort":"high"}`)
	tc := convertChatToGemini(t, req).GenerationConfig.ThinkingConfig
	require.NotNil(t, tc)
	assert.Equal(t, flashMaxBudget*80/100, *tc.ThinkingBudget)

	budget := geminiChat("gemini-2.5-flash-lite")
	budget.Reasoning = json.RawMessage(`{"max_tokens":3000}`)
	tb := convertChatToGemini(t, budget).GenerationConfig.ThinkingConfig
	require.NotNil(t, tb)
	assert.Equal(t, 3000, *tb.ThinkingBudget, "an explicit budget is used as sent")
}

func budgetOf(t *testing.T, g *dto.GeminiChatRequest) int {
	t.Helper()
	tc := g.GenerationConfig.ThinkingConfig
	require.NotNil(t, tc)
	require.NotNil(t, tc.ThinkingBudget)
	return *tc.ThinkingBudget
}

func TestGeminiReasoning_BudgetsAreClampedPerFamily(t *testing.T) {
	low := geminiChat("gemini-2.5-flash-lite")
	low.ReasoningEffort = "minimal" // 5% of 24576 = 1228, above flash-lite's 512 floor
	assert.Equal(t, 1228, budgetOf(t, convertChatToGemini(t, low)))

	tiny := geminiChat("gemini-2.5-flash-lite")
	tiny.Reasoning = json.RawMessage(`{"max_tokens":100}`)
	assert.Equal(t, flashLiteMinBudget, budgetOf(t, convertChatToGemini(t, tiny)))

	pro := geminiChat("gemini-2.5-pro")
	pro.ReasoningEffort = "high"
	assert.Equal(t, proMaxBudget*80/100, budgetOf(t, convertChatToGemini(t, pro)))

	huge := geminiChat("gemini-3.1-pro-preview")
	huge.Reasoning = json.RawMessage(`{"max_tokens":999999}`)
	assert.Equal(t, proMaxBudget, budgetOf(t, convertChatToGemini(t, huge)))
}

func TestGeminiReasoning_TurningItOffOnlyWhereThatIsAllowed(t *testing.T) {
	flash := geminiChat("gemini-2.5-flash")
	flash.Reasoning = json.RawMessage(`{"enabled":false}`)
	tc := convertChatToGemini(t, flash).GenerationConfig.ThinkingConfig
	require.NotNil(t, tc)
	assert.Equal(t, 0, *tc.ThinkingBudget)

	// "Reasoning is mandatory for this endpoint and cannot be disabled" -- a zero
	// budget would turn a 200 into a 400 on these.
	for _, model := range []string{"gemini-2.5-pro", "gemini-3.8-flash"} {
		req := geminiChat(model)
		req.ReasoningEffort = "none"
		assert.Nil(t, convertChatToGemini(t, req).GenerationConfig.ThinkingConfig, model)
	}
}

func TestGeminiReasoning_NoReasoningRequestedAddsNothing(t *testing.T) {
	assert.Nil(t, convertChatToGemini(t, geminiChat("gemini-2.5-flash-lite")).GenerationConfig.ThinkingConfig)
	odd := geminiChat("gemini-2.5-flash-lite")
	odd.ReasoningEffort = "turbo" // not an effort word we know; leave the upstream default
	assert.Nil(t, convertChatToGemini(t, odd).GenerationConfig.ThinkingConfig)
}

func TestGeminiReasoning_ExistingThinkingConfigWins(t *testing.T) {
	g := &dto.GeminiChatRequest{}
	level := &dto.GeminiThinkingConfig{IncludeThoughts: true, ThinkingLevel: "high"}
	g.GenerationConfig.ThinkingConfig = level
	ApplyRequestedThinking(g, "gemini-3.8-flash", &dto.GeneralOpenAIRequest{ReasoningEffort: "low"})
	assert.Same(t, level, g.GenerationConfig.ThinkingConfig, "a suffix-derived config is not overwritten")
}

func TestGeminiReasoning_ResponsesAPIEffortNowThinks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	model := "gemini-2.5-flash-lite"
	info := &relaycommon.RelayInfo{OriginModelName: model, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: model}}
	req := dto.OpenAIResponsesRequest{Model: model, Input: json.RawMessage(`"hi"`), Reasoning: &dto.Reasoning{Effort: "low"}}
	out, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(c, info, req)
	require.NoError(t, err)
	tc := out.(*dto.GeminiChatRequest).GenerationConfig.ThinkingConfig
	require.NotNil(t, tc)
	assert.Equal(t, flashMaxBudget*20/100, *tc.ThinkingBudget)
}
