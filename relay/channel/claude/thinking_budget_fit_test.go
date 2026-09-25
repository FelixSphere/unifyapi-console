package claude

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func budgetPointer(value int) *int { return &value }

func maxTokensPointer(value uint) *uint { return &value }

// Anthropic rejects the whole request when budget_tokens is not strictly below
// max_tokens. The OpenAI chat converter picks budget_tokens from the effort word
// alone (low 1280, medium 2048, high 4096) and never looks at max_tokens, so
// claude-opus-4-5 and claude-sonnet-4-5 answered 400 to reasoning_effort on
// production after #190 while the same models answered 200 to the equivalent
// reasoning:{"effort":...}. Only the second entry point clamped.
func TestEnabledShapeKeepsBudgetUnderMaxTokens(t *testing.T) {
	settings := model_setting.GetClaudeSettings()
	originalAdaptive := settings.AdaptiveThinkingModels
	t.Cleanup(func() { settings.AdaptiveThinkingModels = originalAdaptive })
	// A model that accepts the enabled shape, so the branch under test returns
	// early instead of converting to adaptive.
	settings.AdaptiveThinkingModels = []string{"claude-fable-5"}

	cases := []struct {
		name             string
		maxTokens        uint
		budget           int
		expectedBudget   int
		expectedMaxAfter uint
	}{
		{
			name: "budget above max_tokens is pulled under it",
			// reasoning_effort:"low" on max_tokens 512: 1280 > 512, so 80% of
			// 512 is 409, below the 1024 floor, so the floor wins and
			// max_tokens is raised to hold it.
			maxTokens: 512, budget: 1280, expectedBudget: 1024, expectedMaxAfter: 1280,
		},
		{
			name:      "a budget that already fits is left alone",
			maxTokens: 8192, budget: 4096, expectedBudget: 4096, expectedMaxAfter: 8192,
		},
		{
			name: "budget equal to max_tokens is still too big",
			// Anthropic requires strictly greater, not greater-or-equal.
			maxTokens: 4096, budget: 4096, expectedBudget: 3276, expectedMaxAfter: 4096,
		},
		{
			name:      "reasoning_effort high on a mid max_tokens",
			maxTokens: 2048, budget: 4096, expectedBudget: 1638, expectedMaxAfter: 2048,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := &dto.ClaudeRequest{
				Model:     "claude-opus-4-5",
				MaxTokens: maxTokensPointer(tc.maxTokens),
				Thinking:  &dto.Thinking{Type: "enabled", BudgetTokens: budgetPointer(tc.budget)},
			}

			NormalizeThinkingShape(request, "low")

			require.NotNil(t, request.Thinking)
			assert.Equal(t, "enabled", request.Thinking.Type, "the model accepts this shape")
			require.NotNil(t, request.Thinking.BudgetTokens)
			assert.Equal(t, tc.expectedBudget, *request.Thinking.BudgetTokens)
			require.NotNil(t, request.MaxTokens)
			assert.Equal(t, tc.expectedMaxAfter, *request.MaxTokens)
			assert.Less(t, *request.Thinking.BudgetTokens, int(*request.MaxTokens),
				"budget_tokens must stay strictly below max_tokens or Anthropic 400s")
		})
	}
}

func TestEnabledShapeWithoutABudgetIsUntouched(t *testing.T) {
	settings := model_setting.GetClaudeSettings()
	originalAdaptive := settings.AdaptiveThinkingModels
	t.Cleanup(func() { settings.AdaptiveThinkingModels = originalAdaptive })
	settings.AdaptiveThinkingModels = []string{"claude-fable-5"}

	request := &dto.ClaudeRequest{
		Model:     "claude-opus-4-5",
		MaxTokens: maxTokensPointer(512),
		Thinking:  &dto.Thinking{Type: "enabled"},
	}

	NormalizeThinkingShape(request, "")

	assert.Equal(t, "enabled", request.Thinking.Type)
	assert.Nil(t, request.Thinking.BudgetTokens)
	require.NotNil(t, request.MaxTokens)
	assert.Equal(t, uint(512), *request.MaxTokens, "max_tokens must not move when nothing needed fitting")
}

// The clamp must not disturb the conversion it sits in front of.
func TestModelThatRejectsEnabledStillConvertsToAdaptive(t *testing.T) {
	settings := model_setting.GetClaudeSettings()
	originalAdaptive := settings.AdaptiveThinkingModels
	t.Cleanup(func() { settings.AdaptiveThinkingModels = originalAdaptive })
	settings.AdaptiveThinkingModels = []string{"claude-fable-5"}

	request := &dto.ClaudeRequest{
		Model:     "claude-fable-5",
		MaxTokens: maxTokensPointer(512),
		Thinking:  &dto.Thinking{Type: "enabled", BudgetTokens: budgetPointer(1280)},
	}

	NormalizeThinkingShape(request, "low")

	require.NotNil(t, request.Thinking)
	assert.Equal(t, "adaptive", request.Thinking.Type)
	assert.Nil(t, request.Thinking.BudgetTokens, "adaptive carries no budget")
	assert.JSONEq(t, `{"effort":"low"}`, string(request.OutputConfig))
	require.NotNil(t, request.MaxTokens)
	assert.Equal(t, uint(512), *request.MaxTokens, "the adaptive path must not inherit the clamp's max_tokens bump")
}
