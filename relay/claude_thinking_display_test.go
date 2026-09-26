package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The native /v1/messages path rebuilds the thinking object when it translates
// the enabled shape into the adaptive one. display is not part of that shape --
// it is the caller saying whether they want the thinking text back -- so losing
// it here silently overrules an explicit display:"omitted" and returns (and
// bills for) content the caller asked us not to send.
func TestAdaptiveThinkingCompatibilityKeepsTheCallersDisplay(t *testing.T) {
	withAdaptiveThinkingModels(t, "claude-fable-5")

	for _, display := range []string{"omitted", "summarized"} {
		t.Run(display, func(t *testing.T) {
			request := &dto.ClaudeRequest{
				Model:    "claude-fable-5",
				Thinking: &dto.Thinking{Type: "enabled", BudgetTokens: intPointer(2048), Display: display},
			}
			applyAdaptiveThinkingCompatibility(request)
			require.Equal(t, "adaptive", request.Thinking.Type)
			assert.Equal(t, display, request.Thinking.Display)
		})
	}
}

// A caller who named no display must not have one invented here: the default
// belongs at the adaptor, where both entry points converge, so that the two
// paths cannot drift apart again.
func TestAdaptiveThinkingCompatibilityInventsNoDisplay(t *testing.T) {
	withAdaptiveThinkingModels(t, "claude-fable-5")

	request := &dto.ClaudeRequest{
		Model:    "claude-fable-5",
		Thinking: &dto.Thinking{Type: "enabled", BudgetTokens: intPointer(2048)},
	}
	applyAdaptiveThinkingCompatibility(request)
	require.Equal(t, "adaptive", request.Thinking.Type)
	assert.Empty(t, request.Thinking.Display)
}
