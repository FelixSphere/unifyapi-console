package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/setting/model_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setInstructionsAsInput(t *testing.T, on bool) {
	t.Helper()
	policy := &model_setting.GetGlobalSettings().ChatCompletionsToResponsesPolicy
	saved := policy.InstructionsAsInput
	policy.InstructionsAsInput = on
	t.Cleanup(func() { policy.InstructionsAsInput = saved })
}

// A chat request with a system prompt, a tool call and its result, converted
// the way chatCompletionsViaResponses converts it.
func convertedAgentTurn(t *testing.T) *dto.OpenAIResponsesRequest {
	t.Helper()
	var chat dto.GeneralOpenAIRequest
	require.NoError(t, common.UnmarshalJsonStr(`{
		"model": "gpt-6.2-sol",
		"messages": [
			{"role": "system", "content": "You are the billing assistant."},
			{"role": "developer", "content": "Answer in one sentence."},
			{"role": "user", "content": "Weather in Paris?"},
			{"role": "assistant", "content": null, "tool_calls": [{"id": "call_1", "type": "function", "function": {"name": "get_weather", "arguments": "{\"city\":\"Paris\"}"}}]},
			{"role": "tool", "tool_call_id": "call_1", "content": "{\"temp_c\":11}"}
		],
		"tools": [{"type": "function", "function": {"name": "get_weather", "parameters": {"type": "object"}}}]
	}`, &chat))
	req, err := relayconvert.ChatCompletionsRequestToResponsesRequest(&chat)
	require.NoError(t, err)
	return req
}

func inputItems(t *testing.T, req *dto.OpenAIResponsesRequest) []map[string]any {
	t.Helper()
	var items []map[string]any
	require.NoError(t, common.Unmarshal(req.Input, &items))
	return items
}

func TestConvertedSystemPromptStaysInTheCacheablePrefix(t *testing.T) {
	setInstructionsAsInput(t, true)
	req := convertedAgentTurn(t)
	before := inputItems(t, req)
	require.NotEmpty(t, req.Instructions, "the converter is expected to lift system messages into instructions")

	keepConvertedSystemPromptCacheable(req)

	assert.Empty(t, req.Instructions)
	after := inputItems(t, req)
	require.Len(t, after, len(before)+1)
	assert.Equal(t, map[string]any{
		"role":    "developer",
		"content": "You are the billing assistant.\n\nAnswer in one sentence.",
	}, after[0])
	assert.Equal(t, before, after[1:], "the conversation itself must be forwarded unchanged and in order")

	body, err := common.Marshal(req)
	require.NoError(t, err)
	var sent map[string]any
	require.NoError(t, common.Unmarshal(body, &sent))
	assert.NotContains(t, sent, "instructions", "the upstream must not receive the prompt twice")
	assert.Contains(t, sent, "tools")
}

func TestConvertedRequestIsUntouchedUnlessThePolicyAsks(t *testing.T) {
	setInstructionsAsInput(t, false)
	req := convertedAgentTurn(t)
	wantInstructions, wantInput := string(req.Instructions), string(req.Input)

	keepConvertedSystemPromptCacheable(req)

	assert.Equal(t, wantInstructions, string(req.Instructions))
	assert.Equal(t, wantInput, string(req.Input))
}

func TestConvertedRequestWithoutASystemPromptIsUntouched(t *testing.T) {
	setInstructionsAsInput(t, true)
	var chat dto.GeneralOpenAIRequest
	require.NoError(t, common.UnmarshalJsonStr(`{"model":"gpt-6.2-sol","messages":[{"role":"user","content":"hi"}]}`, &chat))
	req, err := relayconvert.ChatCompletionsRequestToResponsesRequest(&chat)
	require.NoError(t, err)
	wantInput := string(req.Input)

	keepConvertedSystemPromptCacheable(req)

	assert.Empty(t, req.Instructions)
	assert.Equal(t, wantInput, string(req.Input))
}
