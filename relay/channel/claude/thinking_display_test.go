package claude

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Anthropic bills thinking tokens whether or not the thinking text comes back,
// and on the newer models it does not come back by default. Measured n=3 on the
// production box (FlatKey channel 154, /v1/messages, claude-opus-5): the plain
// thinking request answered 200 with zero thinking characters three times out
// of three, while still charging for the thinking inside output_tokens.
// OpenRouter returns reasoning for the same request.
//
// Operator decision 2026-09-26: if we bill for thinking, we return it.

func thinkingOf(t *testing.T, typ, display string) *dto.ClaudeRequest {
	t.Helper()
	return &dto.ClaudeRequest{
		Model:     "claude-sonnet-5",
		MaxTokens: uintPtr(4096),
		Thinking:  &dto.Thinking{Type: typ, Display: display},
	}
}

func TestDisplayDefault_AddedWhenTheCallerAskedToThinkAndSaidNothingElse(t *testing.T) {
	for _, typ := range []string{"enabled", "adaptive"} {
		t.Run(typ, func(t *testing.T) {
			req := thinkingOf(t, typ, "")
			ApplyThinkingDisplayDefault(req, false)
			assert.Equal(t, "summarized", req.Thinking.Display)
		})
	}
}

func TestDisplayDefault_NeverOverridesWhatTheCallerChose(t *testing.T) {
	// "omitted" is the case that matters: it is the caller paying for thinking
	// and deliberately not wanting it back. Overriding it would be us deciding
	// what a customer's request means.
	for _, display := range []string{"omitted", "summarized"} {
		t.Run(display, func(t *testing.T) {
			req := thinkingOf(t, "adaptive", display)
			ApplyThinkingDisplayDefault(req, false)
			assert.Equal(t, display, req.Thinking.Display)
		})
	}
}

func TestDisplayDefault_LeavesARequestThatDidNotAskToThinkAlone(t *testing.T) {
	req := &dto.ClaudeRequest{Model: "claude-sonnet-5", MaxTokens: uintPtr(4096)}
	ApplyThinkingDisplayDefault(req, false)
	assert.Nil(t, req.Thinking, "no thinking parameter may be invented")

	// thinking.type="disabled" is the caller switching thinking off; display
	// alongside it is meaningless and some models reject the pair.
	off := thinkingOf(t, "disabled", "")
	ApplyThinkingDisplayDefault(off, false)
	assert.Empty(t, off.Thinking.Display)
}

func TestDisplayDefault_NotForcedOnACallerWhoExcludedReasoning(t *testing.T) {
	// OpenRouter's reasoning:{"exclude":true}. Nothing on this path honours it
	// today; this only declines to force the opposite on them.
	req := thinkingOf(t, "enabled", "")
	ApplyThinkingDisplayDefault(req, true)
	assert.Empty(t, req.Thinking.Display)
}

// --- display must survive the shape conversion, in both directions ---

func withAdaptiveLists(t *testing.T, requires, rejects []string) {
	t.Helper()
	settings := model_setting.GetClaudeSettings()
	originalRequires, originalRejects := settings.AdaptiveThinkingModels, settings.AdaptiveThinkingUnsupportedModels
	t.Cleanup(func() {
		settings.AdaptiveThinkingModels = originalRequires
		settings.AdaptiveThinkingUnsupportedModels = originalRejects
	})
	settings.AdaptiveThinkingModels = requires
	settings.AdaptiveThinkingUnsupportedModels = rejects
}

func TestNormalizeThinking_CarriesDisplayThroughEnabledToAdaptive(t *testing.T) {
	withAdaptiveLists(t, []string{"claude-fable-5"}, nil)
	req := &dto.ClaudeRequest{Model: "claude-fable-5", MaxTokens: uintPtr(4096),
		Thinking: &dto.Thinking{Type: "enabled", BudgetTokens: intPtr(2048), Display: "omitted"}}
	NormalizeThinkingShape(req, "")
	require.Equal(t, "adaptive", req.Thinking.Type)
	assert.Equal(t, "omitted", req.Thinking.Display,
		"rebuilding the thinking object must not drop the caller's display")
}

func TestNormalizeThinking_CarriesDisplayThroughAdaptiveToEnabled(t *testing.T) {
	withAdaptiveLists(t, nil, []string{"claude-opus-4-5"})
	req := &dto.ClaudeRequest{Model: "claude-opus-4-5", MaxTokens: uintPtr(4096),
		Thinking:     &dto.Thinking{Type: "adaptive", Display: "omitted"},
		OutputConfig: json.RawMessage(`{"effort":"medium"}`)}
	NormalizeThinkingShape(req, "")
	require.Equal(t, "enabled", req.Thinking.Type)
	assert.Equal(t, "omitted", req.Thinking.Display)
}

// --- both entry points, because a fix on one path only is how this class of
// defect has reached production twice ---

func convertClaude(t *testing.T, req *dto.ClaudeRequest) *dto.ClaudeRequest {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	out, err := (&Adaptor{}).ConvertClaudeRequest(c, &relaycommon.RelayInfo{}, req)
	require.NoError(t, err)
	claudeReq, ok := out.(*dto.ClaudeRequest)
	require.True(t, ok, "got %T", out)
	return claudeReq
}

func TestAdaptor_NativeMessagesRequestGetsTheDisplayDefault(t *testing.T) {
	req := &dto.ClaudeRequest{Model: "claude-sonnet-5", MaxTokens: uintPtr(4096),
		Thinking: enabled(2048)}
	out := convertClaude(t, req)
	require.NotNil(t, out.Thinking)
	assert.Equal(t, "summarized", out.Thinking.Display)
}

func TestAdaptor_NativeMessagesRequestKeepsAnExplicitOmitted(t *testing.T) {
	req := &dto.ClaudeRequest{Model: "claude-sonnet-5", MaxTokens: uintPtr(4096),
		Thinking: &dto.Thinking{Type: "enabled", BudgetTokens: intPtr(2048), Display: "omitted"}}
	out := convertClaude(t, req)
	require.NotNil(t, out.Thinking)
	assert.Equal(t, "omitted", out.Thinking.Display)
}

func TestAdaptor_ChatReasoningEffortGetsTheDisplayDefault(t *testing.T) {
	req := chatReq("claude-sonnet-5")
	req.ReasoningEffort = "medium"
	out := convertChat(t, req)
	require.NotNil(t, out.Thinking)
	assert.Equal(t, "summarized", out.Thinking.Display)
}

func TestAdaptor_ChatReasoningObjectGetsTheDisplayDefault(t *testing.T) {
	req := chatReq("claude-sonnet-5")
	req.Reasoning = json.RawMessage(`{"effort":"high"}`)
	out := convertChat(t, req)
	require.NotNil(t, out.Thinking)
	assert.Equal(t, "summarized", out.Thinking.Display)
}

func TestAdaptor_ChatRequestWithoutReasoningStaysWithoutThinking(t *testing.T) {
	out := convertChat(t, chatReq("claude-sonnet-5"))
	assert.Nil(t, out.Thinking, "a caller who did not ask to think must not be made to pay for it")
}

func TestAdaptor_ChatExcludeIsNotOverridden(t *testing.T) {
	req := chatReq("claude-sonnet-5")
	req.Reasoning = json.RawMessage(`{"effort":"high","exclude":true}`)
	out := convertChat(t, req)
	require.NotNil(t, out.Thinking)
	assert.Empty(t, out.Thinking.Display)
}
