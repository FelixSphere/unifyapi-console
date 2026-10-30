package openai

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The top-level reasoning_effort alias next to a reasoning.max_tokens budget
// is refused with 400 on an OpenRouter-backed channel. OpenRouter expands the
// alias into reasoning.effort, sees both, and answers "Only one of
// reasoning.effort and reasoning.max_tokens can be specified" -- measured
// 2026-09-25 on channel 16 against gemini-3.5-flash and glm-5.3.
//
// This is the shape that regressed: the earlier code looked for an effort only
// INSIDE the object, so the alias survived beside a budget and produced that
// 400 from a request that looks perfectly legal. An effort and a budget both
// INSIDE the object are NOT covered here -- an existing test pins that as
// passed through untouched, and changing a pinned contract is the operator's
// call.
func TestReasoningEffortWithBudgetIsRefused(t *testing.T) {
	const orBase = "https://openrouter.ai/api"

	for name, request := range map[string]*dto.GeneralOpenAIRequest{
		"alias next to a budget":        {ReasoningEffort: "low", Reasoning: []byte(`{"max_tokens":600}`)},
		"alias next to a budget, high":  {ReasoningEffort: "high", Reasoning: []byte(`{"max_tokens":2000}`)},
		"alias beside enabled + budget": {ReasoningEffort: "low", Reasoning: []byte(`{"enabled":true,"max_tokens":600}`)},
	} {
		t.Run(name, func(t *testing.T) {
			err := normalizeReasoningParams(openAIInfo("gemini-3.5-flash", orBase, nil), request)
			require.Error(t, err)

			// A plain error is wrapped as 500 by every relay handler, which
			// would report a caller's bad request as our outage.
			wrapped := types.NewError(err, types.ErrorCodeConvertRequestFailed)
			assert.Equal(t, http.StatusBadRequest, wrapped.StatusCode)
		})
	}
}

// The shapes either side of the conflict must still pass through untouched --
// a budget alone and an effort alone are both accepted upstream, and refusing
// them would break working callers.
func TestReasoningBudgetOrEffortAloneIsAccepted(t *testing.T) {
	const orBase = "https://openrouter.ai/api"

	budgetOnly := &dto.GeneralOpenAIRequest{Reasoning: []byte(`{"max_tokens":600}`)}
	require.NoError(t, normalizeReasoningParams(openAIInfo("gemini-3.5-flash", orBase, nil), budgetOnly))
	assert.JSONEq(t, `{"max_tokens":600}`, string(budgetOnly.Reasoning))
	assert.Empty(t, budgetOnly.ReasoningEffort)

	// An effort alone is carried in reasoning_effort only, so a channel
	// param_override replaces it instead of contradicting it.
	effortOnly := &dto.GeneralOpenAIRequest{Reasoning: []byte(`{"effort":"low"}`)}
	require.NoError(t, normalizeReasoningParams(openAIInfo("gemini-3.5-flash", orBase, nil), effortOnly))
	assert.Equal(t, "low", effortOnly.ReasoningEffort)
	assert.Empty(t, effortOnly.Reasoning)
}
