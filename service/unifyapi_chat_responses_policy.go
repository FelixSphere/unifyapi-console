package service

import "github.com/QuantumNous/new-api/setting/model_setting"

// ChatCompletionsToResponsesWantsRequest narrows the chat-to-Responses policy
// to the requests that need it.
//
// The policy exists for models whose /v1/chat/completions endpoint refuses
// function tools ("use /v1/responses"). Upstream's policy converts every
// matching call, tools or not. A plain chat call already works natively, and
// the Responses API has no equivalent for several chat parameters (stop, n,
// logprobs, the penalties), so converting it can only lose something. With
// only_with_tools set, a call that carries no tools is left on the native path.
func ChatCompletionsToResponsesWantsRequest(hasTools bool) bool {
	if !model_setting.GetGlobalSettings().ChatCompletionsToResponsesPolicy.OnlyWithTools {
		return true
	}
	return hasTools
}
