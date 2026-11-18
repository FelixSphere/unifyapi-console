package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/model_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChatToResponsesPolicyCanBeLimitedToToolCalls(t *testing.T) {
	policy := &model_setting.GetGlobalSettings().ChatCompletionsToResponsesPolicy
	saved := *policy
	t.Cleanup(func() { *policy = saved })

	// The option row is stored as JSON; this is the shape production carries.
	require.NoError(t, common.UnmarshalJsonStr(
		`{"enabled":true,"all_channels":false,"channel_ids":[225],"model_patterns":["^gpt-6\\.2-sol$"],"only_with_tools":true,"instructions_as_input":true}`,
		policy))
	require.True(t, policy.OnlyWithTools)
	require.True(t, policy.InstructionsAsInput)

	// The channel and model still have to match, exactly as before.
	assert.True(t, ShouldChatCompletionsUseResponsesGlobal(225, 1, "gpt-6.2-sol"))
	assert.False(t, ShouldChatCompletionsUseResponsesGlobal(1, 1, "gpt-6.2-sol"))

	assert.True(t, ChatCompletionsToResponsesWantsRequest(true), "a call with tools is the one the native endpoint refuses")
	assert.False(t, ChatCompletionsToResponsesWantsRequest(false), "a plain chat call must stay on the native endpoint")
}

func TestChatToResponsesPolicyConvertsEveryCallByDefault(t *testing.T) {
	policy := &model_setting.GetGlobalSettings().ChatCompletionsToResponsesPolicy
	saved := *policy
	t.Cleanup(func() { *policy = saved })

	// A row written before these options existed keeps upstream's behaviour.
	*policy = model_setting.ChatCompletionsToResponsesPolicy{}
	require.NoError(t, common.UnmarshalJsonStr(
		`{"enabled":true,"all_channels":false,"channel_ids":[189],"model_patterns":["^gpt-6-astra$"]}`, policy))
	assert.False(t, policy.OnlyWithTools)
	assert.False(t, policy.InstructionsAsInput)

	assert.True(t, ChatCompletionsToResponsesWantsRequest(true))
	assert.True(t, ChatCompletionsToResponsesWantsRequest(false))
}
