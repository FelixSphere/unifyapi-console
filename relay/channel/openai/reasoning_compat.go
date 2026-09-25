package openai

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
)

// errEffortAndBudget refuses a reasoning effort and a token budget sent
// together. A fresh error per call, because the handlers apply options such as
// skip-retry to it in place.
func errEffortAndBudget() error {
	return types.NewErrorWithStatusCode(
		errors.New("only one of reasoning.effort (or its alias reasoning_effort) and reasoning.max_tokens can be specified"),
		types.ErrorCodeInvalidRequest,
		http.StatusBadRequest,
		types.ErrOptionWithSkipRetry(),
	)
}

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
func normalizeReasoningParams(info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) error {
	if request == nil || info == nil {
		return nil
	}
	model := info.UpstreamModelName
	openRouterBackend := isOpenRouterBaseURL(info.ChannelBaseUrl)

	if !openRouterBackend && isNonReasoningOpenAIModel(model) {
		request.ReasoningEffort = ""
		request.Reasoning = nil
		return nil
	}
	if len(request.Reasoning) == 0 {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(request.Reasoning, &obj); err != nil {
		return nil // not ours to interpret; let the upstream judge it
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
		return nil
	}
	// OpenRouter backend: it accepts reasoning_effort as an alias for
	// reasoning.effort, and it rejects an effort and a token budget together in
	// whichever dialect each arrived -- measured 2026-09-25 on channel 16,
	// gemini-3.5-flash and glm-5.3, both shapes answering 400 "Only one of
	// reasoning.effort and reasoning.max_tokens can be specified".
	//
	// Scope: only the TOP-LEVEL alias next to a budget is refused here. An
	// effort and a budget together INSIDE the object are deliberately left as
	// sent -- TestReasoning_OpenRouterBackendLeavesAnExplicitBudgetAlone pins
	// that, and changing a pinned contract needs the operator, not this fix.
	// (That shape also answers 400 upstream, so the two now behave
	// differently for the same reason; raised rather than decided here.)
	//
	// The alias is the regression: the older comment read "effort and
	// max_tokens together are the caller's own choice", but it only looked for
	// an effort INSIDE the object, so reasoning_effort survived next to a
	// budget. OpenRouter expands the alias into reasoning.effort, sees both,
	// and answers 400 -- from a request shape that looks perfectly legal.
	// Refusing here costs no upstream round trip, and skip-retry stops the
	// relay trying the model's other channels for a call none can accept.
	if hasBudget && effort == "" && request.ReasoningEffort != "" {
		return errEffortAndBudget()
	}
	// Unchanged from before: nothing to move, or the object carries its own
	// budget and is passed through exactly as sent.
	if effort == "" || hasBudget {
		return nil
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
		return nil
	}
	if out, err := json.Marshal(obj); err == nil {
		request.Reasoning = out
	}
	return nil
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
