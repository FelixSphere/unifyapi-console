package openai

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Each case reproduces a request production answered with a 400 on
// 2026-09-25 while OpenRouter answered the identical request with a 200.
// Evidence: perf/eval/results/thinking-20260925T0540Z.json.gz (unifyai workspace).

func openAIInfo(model, baseURL string, override map[string]interface{}) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelType: constant.ChannelTypeOpenAI, ChannelBaseUrl: baseURL,
		UpstreamModelName: model, ParamOverride: override,
	}}
}

func req(model string) *dto.GeneralOpenAIRequest {
	return &dto.GeneralOpenAIRequest{Model: model, Messages: []dto.Message{{Role: "user", Content: "hi"}}}
}

func body(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	out := map[string]any{}
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

func TestReasoning_OpenAIUpstreamGetsReasoningEffortNotTheObject(t *testing.T) {
	// Was: 400 "Unknown parameter: 'reasoning'" (gpt-5-mini, gpt-5.6-sol).
	for _, model := range []string{"gpt-5-mini", "gpt-5.6-sol", "o4-mini"} {
		r := req(model)
		r.Reasoning = json.RawMessage(`{"effort":"medium"}`)
		normalizeReasoningParams(openAIInfo(model, "https://console.flatkey.ai", nil), r)
		assert.Equal(t, "medium", r.ReasoningEffort, model)
		assert.Empty(t, r.Reasoning, model)
		assert.NotContains(t, body(t, r), "reasoning", model)
	}
}

func TestReasoning_ExplicitReasoningEffortWinsOverTheObject(t *testing.T) {
	r := req("gpt-5-mini")
	r.ReasoningEffort = "high"
	r.Reasoning = json.RawMessage(`{"effort":"low"}`)
	normalizeReasoningParams(openAIInfo("gpt-5-mini", "https://api.openai.com", nil), r)
	assert.Equal(t, "high", r.ReasoningEffort)
	assert.Empty(t, r.Reasoning)
}

func TestReasoning_NonReasoningModelDropsBothParameters(t *testing.T) {
	// Was: 400 "Unrecognized request argument supplied: reasoning_effort" /
	// "...: reasoning" (gpt-4o-mini). OpenRouter drops them and answers.
	for _, model := range []string{"gpt-4o-mini", "gpt-4o", "gpt-4.1-mini", "gpt-3.5-turbo"} {
		r := req(model)
		r.ReasoningEffort = "medium"
		r.Reasoning = json.RawMessage(`{"effort":"medium"}`)
		normalizeReasoningParams(openAIInfo(model, "https://console.flatkey.ai", nil), r)
		b := body(t, r)
		assert.NotContains(t, b, "reasoning_effort", model)
		assert.NotContains(t, b, "reasoning", model)
	}
}

func TestReasoning_UnknownModelKeepsItsParameters(t *testing.T) {
	// An unlisted model is not assumed to be non-reasoning.
	r := req("gpt-7-preview")
	r.ReasoningEffort = "medium"
	normalizeReasoningParams(openAIInfo("gpt-7-preview", "https://api.openai.com", nil), r)
	assert.Equal(t, "medium", r.ReasoningEffort)
}

func TestReasoning_OpenRouterBackendMovesEffortAndKeepsTheRest(t *testing.T) {
	r := req("google/gemini-3.5-flash")
	r.Reasoning = json.RawMessage(`{"effort":"medium","exclude":true}`)
	normalizeReasoningParams(openAIInfo("google/gemini-3.5-flash", "https://openrouter.ai/api", nil), r)
	assert.Equal(t, "medium", r.ReasoningEffort)
	assert.JSONEq(t, `{"exclude":true}`, string(r.Reasoning))
}

func TestReasoning_OpenRouterBackendLeavesAnExplicitBudgetAlone(t *testing.T) {
	r := req("z-ai/glm-5.3")
	r.Reasoning = json.RawMessage(`{"effort":"high","max_tokens":2000}`)
	normalizeReasoningParams(openAIInfo("z-ai/glm-5.3", "https://openrouter.ai/api", nil), r)
	assert.Empty(t, r.ReasoningEffort)
	assert.JSONEq(t, `{"effort":"high","max_tokens":2000}`, string(r.Reasoning))
}

func TestReasoning_OpenRouterNonReasoningNameIsNotDropped(t *testing.T) {
	// OpenRouter itself tolerates the parameter; we only drop for real OpenAI.
	r := req("openai/gpt-4o-mini")
	r.ReasoningEffort = "low"
	normalizeReasoningParams(openAIInfo("openai/gpt-4o-mini", "https://openrouter.ai/api", nil), r)
	assert.Equal(t, "low", r.ReasoningEffort)
}

func TestReasoning_ChannelOverrideNoLongerConflictsWithTheCallersObject(t *testing.T) {
	// Was: 400 "reasoning_effort and reasoning.effort are both provided with
	// conflicting values" -- channels 16 and 174 carry the param_override
	// {"reasoning_effort":"low"} and the caller sent reasoning:{effort:medium}.
	// Runs the real override code over the converted body.
	override := map[string]interface{}{"reasoning_effort": "low"}
	info := openAIInfo("google/gemini-3.5-flash", "https://openrouter.ai/api", override)
	r := req("google/gemini-3.5-flash")
	r.Reasoning = json.RawMessage(`{"effort":"medium"}`)
	normalizeReasoningParams(info, r)
	raw, err := json.Marshal(r)
	require.NoError(t, err)
	out, err := relaycommon.ApplyParamOverride(raw, override, nil)
	require.NoError(t, err)
	b := map[string]any{}
	require.NoError(t, json.Unmarshal(out, &b))
	assert.Equal(t, "low", b["reasoning_effort"], "the operator's override wins")
	if reasoning, ok := b["reasoning"].(map[string]any); ok {
		assert.NotContains(t, reasoning, "effort", "a second, contradicting effort is what OpenRouter rejects")
	}
}

func TestReasoning_NoParametersIsANoop(t *testing.T) {
	r := req("gpt-5-mini")
	normalizeReasoningParams(openAIInfo("gpt-5-mini", "https://api.openai.com", nil), r)
	assert.Empty(t, r.ReasoningEffort)
	assert.Empty(t, r.Reasoning)
}

func TestIsOpenRouterBaseURL(t *testing.T) {
	assert.True(t, isOpenRouterBaseURL("https://openrouter.ai/api"))
	assert.True(t, isOpenRouterBaseURL("https://eu.openrouter.ai"))
	assert.False(t, isOpenRouterBaseURL("https://openrouter.ai.evil.example"))
	assert.False(t, isOpenRouterBaseURL("https://console.flatkey.ai"))
	assert.False(t, isOpenRouterBaseURL(""))
}
