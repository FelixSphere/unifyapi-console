package openai

import (
	"encoding/json"
	"net/url"
	"strings"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// nonReasoningOpenAIPrefixes are OpenAI chat models with no reasoning mode.
// OpenAI answers reasoning_effort on them with "Unrecognized request argument
// supplied"; OpenRouter drops the field and answers normally. Listed
// explicitly rather than inferred, so an unknown new model keeps receiving the
// parameter instead of having it silently removed.
var nonReasoningOpenAIPrefixes = []string{"gpt-4o", "chatgpt-4o", "gpt-4.1", "gpt-4-", "gpt-3.5"}

// normalizeReasoningParams reconciles the two reasoning dialects callers send
// -- OpenAI's top-level reasoning_effort and OpenRouter's reasoning object --
// with what a plain OpenAI-type channel's upstream accepts. Measured
// 2026-09-25, each case below was a 400 from us and a 200 from OpenRouter:
//
//   - real OpenAI upstream + reasoning:{effort}: "Unknown parameter:
//     'reasoning'" (gpt-5-mini, gpt-5.6-sol). The effort is moved to
//     reasoning_effort and the object dropped.
//   - non-reasoning model + either parameter: "Unrecognized request argument"
//     (gpt-4o-mini). Both are dropped, as OpenRouter does.
//   - OpenRouter-backed type-1 channel + reasoning:{effort} + a channel
//     param_override of reasoning_effort: "reasoning_effort and
//     reasoning.effort are both provided with conflicting values" (channels
//     16 and 174). The effort is carried in reasoning_effort only, so the
//     override -- applied later, on the JSON -- replaces it instead of
//     contradicting it. The operator's override therefore wins, as intended.
func normalizeReasoningParams(info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) {
	if request == nil || info == nil {
		return
	}
	model := info.UpstreamModelName
	openRouterBackend := isOpenRouterBaseURL(info.ChannelBaseUrl)

	if !openRouterBackend && isNonReasoningOpenAIModel(model) {
		request.ReasoningEffort = ""
		request.Reasoning = nil
		return
	}
	if len(request.Reasoning) == 0 {
		return
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(request.Reasoning, &obj); err != nil {
		return // not ours to interpret; let the upstream judge it
	}
	var effort string
	if raw, ok := obj["effort"]; ok {
		_ = json.Unmarshal(raw, &effort)
	}
	_, hasBudget := obj["max_tokens"]

	if !openRouterBackend {
		// OpenAI has no reasoning object at all.
		if request.ReasoningEffort == "" && effort != "" {
			request.ReasoningEffort = effort
		}
		request.Reasoning = nil
		return
	}
	// OpenRouter backend: it accepts reasoning_effort as an alias for
	// reasoning.effort. Move the effort out unless the object also names a
	// token budget (effort and max_tokens together are the caller's own choice
	// and are left exactly as sent).
	if effort == "" || hasBudget {
		return
	}
	if request.ReasoningEffort == "" {
		request.ReasoningEffort = effort
	}
	delete(obj, "effort")
	if enabled, ok := obj["enabled"]; ok && string(enabled) == "true" {
		delete(obj, "enabled") // implied by an effort
	}
	if len(obj) == 0 {
		request.Reasoning = nil
		return
	}
	if out, err := json.Marshal(obj); err == nil {
		request.Reasoning = out
	}
}

func isNonReasoningOpenAIModel(model string) bool {
	for _, p := range nonReasoningOpenAIPrefixes {
		if strings.HasPrefix(model, p) {
			return true
		}
	}
	return false
}

func isOpenRouterBaseURL(base string) bool {
	if base == "" {
		return false
	}
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "openrouter.ai" || strings.HasSuffix(host, ".openrouter.ai")
}
