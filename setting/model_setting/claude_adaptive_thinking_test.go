package model_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A bare-prefix match here would silently downgrade claude-fable-5.1, which
// accepts the enabled shape, and throw away the caller's budget_tokens.
func TestRequiresAdaptiveThinkingMatchesTheModelAndItsSnapshotsOnly(t *testing.T) {
	settings := ClaudeSettings{AdaptiveThinkingModels: []string{"claude-fable-5", ""}}

	cases := []struct {
		model    string
		expected bool
	}{
		{model: "claude-fable-5", expected: true},
		{model: "claude-fable-5-20260801", expected: true},
		{model: "claude-fable-5.1", expected: false},
		{model: "claude-fable-5.1-20260901", expected: false},
		{model: "claude-fable-50", expected: false},
		{model: "claude-opus-4-8", expected: false},
		{model: "", expected: false},
	}

	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			assert.Equal(t, tc.expected, settings.RequiresAdaptiveThinking(tc.model))
		})
	}
}

func TestRequiresAdaptiveThinkingIsFalseWhenNothingIsConfigured(t *testing.T) {
	settings := ClaudeSettings{}

	assert.False(t, settings.RequiresAdaptiveThinking("claude-fable-5"))
}

func TestDefaultClaudeSettingsCarryTheModelThatRejectsTheEnabledShape(t *testing.T) {
	assert.Contains(t, defaultClaudeSettings.AdaptiveThinkingModels, "claude-fable-5")
}
