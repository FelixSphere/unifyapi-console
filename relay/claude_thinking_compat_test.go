package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withAdaptiveThinkingModels points the global Claude settings at an explicit
// list for the duration of one test, because the conversion reads process
// state rather than taking it as an argument.
func withAdaptiveThinkingModels(t *testing.T, models ...string) {
	t.Helper()
	settings := model_setting.GetClaudeSettings()
	original := settings.AdaptiveThinkingModels
	t.Cleanup(func() { settings.AdaptiveThinkingModels = original })
	settings.AdaptiveThinkingModels = models
}

func intPointer(value int) *int { return &value }

// Production returned a hard 400 to every caller using Anthropic's documented
// thinking parameter against claude-fable-5. These pin the translation that
// stops that, including the boundary, which decides how much the caller is
// billed to think.
func TestAdaptiveThinkingCompatibilityTranslatesTheEnabledShape(t *testing.T) {
	withAdaptiveThinkingModels(t, "claude-fable-5")

	cases := []struct {
		name           string
		budgetTokens   *int
		expectedEffort string
	}{
		{name: "at the 1024 floor asks for the least thinking", budgetTokens: intPointer(1024), expectedEffort: `{"effort":"low"}`},
		{name: "below the floor is still low", budgetTokens: intPointer(512), expectedEffort: `{"effort":"low"}`},
		{name: "one token above the floor is high", budgetTokens: intPointer(1025), expectedEffort: `{"effort":"high"}`},
		{name: "a large budget is high", budgetTokens: intPointer(32000), expectedEffort: `{"effort":"high"}`},
		{name: "no budget named is treated as the high end", budgetTokens: nil, expectedEffort: `{"effort":"high"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := &dto.ClaudeRequest{
				Model:    "claude-fable-5",
				Thinking: &dto.Thinking{Type: "enabled", BudgetTokens: tc.budgetTokens},
			}

			applyAdaptiveThinkingCompatibility(request)

			require.NotNil(t, request.Thinking)
			assert.Equal(t, "adaptive", request.Thinking.Type)
			assert.Nil(t, request.Thinking.BudgetTokens, "adaptive has no token budget to carry")
			assert.JSONEq(t, tc.expectedEffort, string(request.OutputConfig))
		})
	}
}

func TestAdaptiveThinkingCompatibilityLeavesEverythingElseAlone(t *testing.T) {
	withAdaptiveThinkingModels(t, "claude-fable-5")

	t.Run("a model that accepts the enabled shape keeps its budget", func(t *testing.T) {
		request := &dto.ClaudeRequest{
			Model:    "claude-opus-4-8",
			Thinking: &dto.Thinking{Type: "enabled", BudgetTokens: intPointer(4096)},
		}

		applyAdaptiveThinkingCompatibility(request)

		assert.Equal(t, "enabled", request.Thinking.Type)
		require.NotNil(t, request.Thinking.BudgetTokens)
		assert.Equal(t, 4096, *request.Thinking.BudgetTokens)
		assert.Empty(t, string(request.OutputConfig))
	})

	t.Run("a request already in the adaptive shape is untouched", func(t *testing.T) {
		request := &dto.ClaudeRequest{
			Model:    "claude-fable-5",
			Thinking: &dto.Thinking{Type: "adaptive", Display: "summarized"},
		}

		applyAdaptiveThinkingCompatibility(request)

		assert.Equal(t, "adaptive", request.Thinking.Type)
		assert.Equal(t, "summarized", request.Thinking.Display)
		assert.Empty(t, string(request.OutputConfig), "an effort the caller did not ask for must not appear")
	})

	t.Run("a request that asked for no thinking stays that way", func(t *testing.T) {
		request := &dto.ClaudeRequest{Model: "claude-fable-5"}

		applyAdaptiveThinkingCompatibility(request)

		assert.Nil(t, request.Thinking)
		assert.Empty(t, string(request.OutputConfig))
	})

	t.Run("a nil request does not panic", func(t *testing.T) {
		assert.NotPanics(t, func() { applyAdaptiveThinkingCompatibility(nil) })
	})
}
