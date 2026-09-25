package claude

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

// Every case here reproduces a request measured on 2026-09-25 that production
// answered with a 400 (or a 200 that silently dropped thinking) while
// OpenRouter answered the identical request normally. Raw evidence:
// perf/eval/results/thinking-20260925T0540Z.json.gz in the unifyai workspace.

func uintPtr(v uint) *uint { return &v }
func intPtr(v int) *int    { return &v }

func enabled(budget int) *dto.Thinking {
	return &dto.Thinking{Type: "enabled", BudgetTokens: intPtr(budget)}
}

func TestNormalizeThinking_FableEnabledBecomesAdaptive(t *testing.T) {
	req := &dto.ClaudeRequest{Model: "claude-fable-5", MaxTokens: uintPtr(4096), Thinking: enabled(2048)}
	NormalizeThinkingShape(req, "")
	require.Equal(t, "adaptive", req.Thinking.Type)
	assert.Nil(t, req.Thinking.BudgetTokens, "adaptive carries no budget")
	assert.JSONEq(t, `{"effort":"high"}`, string(req.OutputConfig))
}

func TestNormalizeThinking_FableKeepsTheCallersEffortWord(t *testing.T) {
	// reasoning_effort:"medium" reaches the adaptor as enabled/2048; without the
	// caller's word the budget rule would round it up to high.
	req := &dto.ClaudeRequest{Model: "claude-fable-5", MaxTokens: uintPtr(4096), Thinking: enabled(2048)}
	NormalizeThinkingShape(req, "medium")
	require.Equal(t, "adaptive", req.Thinking.Type)
	assert.JSONEq(t, `{"effort":"medium"}`, string(req.OutputConfig))
}

func TestNormalizeThinking_FableFloorBudgetIsLowEffort(t *testing.T) {
	req := &dto.ClaudeRequest{Model: "claude-fable-5", MaxTokens: uintPtr(4096), Thinking: enabled(1024)}
	NormalizeThinkingShape(req, "")
	assert.JSONEq(t, `{"effort":"low"}`, string(req.OutputConfig))
}

func TestNormalizeThinking_FableSnapshotMatchesButFable51DoesNot(t *testing.T) {
	snap := &dto.ClaudeRequest{Model: "claude-fable-5-20260801", MaxTokens: uintPtr(4096), Thinking: enabled(2048)}
	NormalizeThinkingShape(snap, "")
	assert.Equal(t, "adaptive", snap.Thinking.Type)

	// claude-fable-5.1 accepts enabled; converting it would throw away the
	// caller's exact budget_tokens.
	next := &dto.ClaudeRequest{Model: "claude-fable-5.1", MaxTokens: uintPtr(4096), Thinking: enabled(2048)}
	NormalizeThinkingShape(next, "")
	require.Equal(t, "enabled", next.Thinking.Type)
	assert.Equal(t, 2048, *next.Thinking.BudgetTokens)
	assert.Empty(t, next.OutputConfig)
}

func TestNormalizeThinking_AdaptiveBecomesEnabledForModelsThatPredateIt(t *testing.T) {
	for _, model := range []string{"claude-opus-4-5", "claude-sonnet-4-5", "claude-sonnet-4-5-20250929"} {
		req := &dto.ClaudeRequest{Model: model, MaxTokens: uintPtr(4096),
			Thinking: &dto.Thinking{Type: "adaptive"}, OutputConfig: json.RawMessage(`{"effort":"medium"}`)}
		NormalizeThinkingShape(req, "")
		require.Equal(t, "enabled", req.Thinking.Type, model)
		require.NotNil(t, req.Thinking.BudgetTokens, model)
		assert.Equal(t, 2048, *req.Thinking.BudgetTokens, model)
		assert.Empty(t, req.OutputConfig, "effort is now carried by the budget: %s", model)
	}
}

func TestNormalizeThinking_AdaptiveKeepsOtherOutputConfigFields(t *testing.T) {
	req := &dto.ClaudeRequest{Model: "claude-opus-4-5", MaxTokens: uintPtr(4096),
		Thinking: &dto.Thinking{Type: "adaptive"}, OutputConfig: json.RawMessage(`{"effort":"low","format":{"type":"json"}}`)}
	NormalizeThinkingShape(req, "")
	assert.Equal(t, minThinkingBudget, *req.Thinking.BudgetTokens)
	assert.JSONEq(t, `{"format":{"type":"json"}}`, string(req.OutputConfig))
}

func TestNormalizeThinking_BudgetStaysBelowMaxTokens(t *testing.T) {
	// Anthropic 400s when budget_tokens >= max_tokens.
	req := &dto.ClaudeRequest{Model: "claude-opus-4-5", MaxTokens: uintPtr(3000),
		Thinking: &dto.Thinking{Type: "adaptive"}, OutputConfig: json.RawMessage(`{"effort":"high"}`)}
	NormalizeThinkingShape(req, "")
	assert.Less(t, *req.Thinking.BudgetTokens, int(*req.MaxTokens))
	assert.GreaterOrEqual(t, *req.Thinking.BudgetTokens, minThinkingBudget)

	tiny := &dto.ClaudeRequest{Model: "claude-opus-4-5", MaxTokens: uintPtr(500), Thinking: &dto.Thinking{Type: "adaptive"}}
	NormalizeThinkingShape(tiny, "")
	assert.Equal(t, minThinkingBudget, *tiny.Thinking.BudgetTokens)
	assert.Greater(t, int(*tiny.MaxTokens), minThinkingBudget, "max_tokens raised to hold the minimum budget")
}

func TestNormalizeThinking_LeavesModelsThatAcceptBothShapesAlone(t *testing.T) {
	for _, model := range []string{"claude-opus-4-8", "claude-sonnet-5", "claude-opus-5"} {
		a := &dto.ClaudeRequest{Model: model, MaxTokens: uintPtr(4096), Thinking: &dto.Thinking{Type: "adaptive", Display: "summarized"},
			OutputConfig: json.RawMessage(`{"effort":"high"}`)}
		NormalizeThinkingShape(a, "")
		assert.Equal(t, "adaptive", a.Thinking.Type, model)
		assert.Equal(t, "summarized", a.Thinking.Display, model)
		e := &dto.ClaudeRequest{Model: model, MaxTokens: uintPtr(4096), Thinking: enabled(2048)}
		NormalizeThinkingShape(e, "")
		assert.Equal(t, "enabled", e.Thinking.Type, model)
		assert.Equal(t, 2048, *e.Thinking.BudgetTokens, model)
	}
}

func TestNormalizeThinking_IsIdempotent(t *testing.T) {
	req := &dto.ClaudeRequest{Model: "claude-fable-5", MaxTokens: uintPtr(4096), Thinking: enabled(2048)}
	NormalizeThinkingShape(req, "medium")
	first, _ := json.Marshal(req)
	NormalizeThinkingShape(req, "medium")
	second, _ := json.Marshal(req)
	assert.JSONEq(t, string(first), string(second))
}

func TestNormalizeThinking_NoThinkingIsUntouched(t *testing.T) {
	req := &dto.ClaudeRequest{Model: "claude-fable-5", MaxTokens: uintPtr(4096)}
	NormalizeThinkingShape(req, "high")
	assert.Nil(t, req.Thinking)
	assert.Empty(t, req.OutputConfig)
}

// --- through the adaptor, i.e. the path an OpenAI-format caller takes ---

func convertChat(t *testing.T, req dto.GeneralOpenAIRequest) *dto.ClaudeRequest {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{}
	out, err := (&Adaptor{}).ConvertOpenAIRequest(c, info, &req)
	require.NoError(t, err)
	claudeReq, ok := out.(*dto.ClaudeRequest)
	require.True(t, ok, "got %T", out)
	return claudeReq
}

func chatReq(model string) dto.GeneralOpenAIRequest {
	return dto.GeneralOpenAIRequest{Model: model, MaxTokens: uintPtr(4096),
		Messages: []dto.Message{{Role: "user", Content: "hi"}}}
}

func TestAdaptor_FableReasoningEffortBecomesAdaptiveWithSameEffort(t *testing.T) {
	// Was: 400 "thinking.type.enabled is not supported for this model". #184
	// fixed only the native /v1/messages path.
	req := chatReq("claude-fable-5")
	req.ReasoningEffort = "medium"
	out := convertChat(t, req)
	require.NotNil(t, out.Thinking)
	assert.Equal(t, "adaptive", out.Thinking.Type)
	assert.JSONEq(t, `{"effort":"medium"}`, string(out.OutputConfig))
}

func TestAdaptor_EffortOnlyReasoningObjectNowThinks(t *testing.T) {
	// Was: 200 with no thinking at all -- the converter honoured only
	// reasoning.max_tokens and dropped an effort-only object.
	req := chatReq("claude-opus-4-8")
	req.Reasoning = json.RawMessage(`{"effort":"high"}`)
	out := convertChat(t, req)
	require.NotNil(t, out.Thinking)
	assert.Equal(t, "enabled", out.Thinking.Type)
	assert.Equal(t, 4096-1 >= *out.Thinking.BudgetTokens, true)

	fable := chatReq("claude-fable-5")
	fable.Reasoning = json.RawMessage(`{"effort":"low"}`)
	fo := convertChat(t, fable)
	require.NotNil(t, fo.Thinking)
	assert.Equal(t, "adaptive", fo.Thinking.Type)
	assert.JSONEq(t, `{"effort":"low"}`, string(fo.OutputConfig))
}

func TestAdaptor_ReasoningObjectThatDisablesThinkingAddsNone(t *testing.T) {
	for _, raw := range []string{`{"enabled":false}`, `{"effort":"none"}`, `{"exclude":true}`} {
		req := chatReq("claude-opus-4-8")
		req.Reasoning = json.RawMessage(raw)
		out := convertChat(t, req)
		assert.Nil(t, out.Thinking, raw)
	}
}

func TestAdaptor_ThinkingDropsTemperatureOtherThanOne(t *testing.T) {
	req := chatReq("claude-sonnet-4-6")
	temp := 0.2
	req.Temperature = &temp
	req.Reasoning = json.RawMessage(`{"effort":"medium"}`)
	out := convertChat(t, req)
	require.NotNil(t, out.Thinking)
	assert.Nil(t, out.Temperature, "extended thinking rejects temperature other than 1")
}
