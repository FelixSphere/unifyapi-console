package claude

import (
	"encoding/json"
	"fmt"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"
)

// Anthropic's minimum budget_tokens; a request below it is a 400.
const minThinkingBudget = 1024

// effortBudgets maps output_config.effort onto budget_tokens for models that
// only understand the enabled shape. low sits on the floor; the rest mirror the
// reasoning_effort budgets the OpenAI->Claude converter already uses.
var effortBudgets = map[string]int{"low": minThinkingBudget, "medium": 2048, "high": 4096, "max": 8192}

// NormalizeThinkingShape rewrites the thinking parameter into the one shape the
// target model accepts, in both directions:
//
//   - enabled -> adaptive, for models that reject enabled (claude-fable-5):
//     "thinking.type.enabled is not supported for this model".
//   - adaptive -> enabled, for models that predate adaptive (claude-opus-4-5,
//     claude-sonnet-4-5): "adaptive thinking is not supported on this model".
//
// It runs at the Claude adaptor, after every inbound format has been converted,
// so the native /v1/messages path, the OpenAI chat path (reasoning_effort) and
// the OpenRouter-style reasoning object all converge here. Before this, only
// the native path handled the first direction (#184) and nothing handled the
// second; OpenRouter accepts all of these requests, so each was a 400 a caller
// got only from us.
//
// preferredEffort, when non-empty, is the caller's own effort word (the OpenAI
// chat path knows it; by the time a request is in Claude shape it survives only
// as a coarse budget). Idempotent.
func NormalizeThinkingShape(request *dto.ClaudeRequest, preferredEffort string) {
	if request == nil || request.Thinking == nil {
		return
	}
	settings := model_setting.GetClaudeSettings()
	switch request.Thinking.Type {
	case "enabled":
		if !settings.RequiresAdaptiveThinking(request.Model) {
			return
		}
		effort := preferredEffort
		if _, known := effortBudgets[effort]; !known {
			// Same rule as the native path: the floor asks for the least
			// thinking, anything above it (or no budget named) for the most.
			effort = "high"
			if request.Thinking.BudgetTokens != nil && *request.Thinking.BudgetTokens <= minThinkingBudget {
				effort = "low"
			}
		}
		request.Thinking = &dto.Thinking{Type: "adaptive"}
		request.OutputConfig = withOutputConfigEffort(request.OutputConfig, effort)
	case "adaptive":
		if !settings.RejectsAdaptiveThinking(request.Model) {
			return
		}
		effort := outputConfigEffort(request.OutputConfig)
		if effort == "" {
			effort = preferredEffort
		}
		budget, known := effortBudgets[effort]
		if !known {
			budget = effortBudgets["high"] // adaptive with no effort means "think as needed"
		}
		budget = fitBudget(request, budget)
		request.Thinking = &dto.Thinking{Type: "enabled", BudgetTokens: &budget}
		request.OutputConfig = withoutOutputConfigEffort(request.OutputConfig)
	}
}

// fitBudget keeps budget_tokens strictly below max_tokens, which Anthropic
// requires, raising max_tokens to the floor only when it cannot hold the
// minimum budget at all (the same move the -thinking adapter already makes).
func fitBudget(request *dto.ClaudeRequest, budget int) int {
	if request.MaxTokens == nil || *request.MaxTokens == 0 {
		return budget
	}
	maxTokens := int(*request.MaxTokens)
	if budget < maxTokens {
		return budget
	}
	budget = int(float64(maxTokens) * model_setting.GetClaudeSettings().ThinkingAdapterBudgetTokensPercentage)
	if budget < minThinkingBudget {
		budget = minThinkingBudget
		raised := uint(minThinkingBudget + 256)
		if uint(maxTokens) < raised {
			request.MaxTokens = &raised
		}
	}
	return budget
}

func outputConfigEffort(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var cfg dto.OutputConfigForEffort
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return ""
	}
	return cfg.Effort
}

func withOutputConfigEffort(raw json.RawMessage, effort string) json.RawMessage {
	cfg := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &cfg)
	}
	cfg["effort"] = effort
	out, err := json.Marshal(cfg)
	if err != nil {
		return json.RawMessage(fmt.Sprintf(`{"effort":%q}`, effort))
	}
	return out
}

// withoutOutputConfigEffort drops effort (the budget now carries it) but keeps
// any other output_config fields the caller set.
func withoutOutputConfigEffort(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	cfg := map[string]any{}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil
	}
	delete(cfg, "effort")
	if len(cfg) == 0 {
		return nil
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		return nil
	}
	return out
}

// openAIRequestedEffort is the effort word an OpenAI-format caller asked for,
// from reasoning_effort or OpenRouter's reasoning.effort.
func openAIRequestedEffort(request *dto.GeneralOpenAIRequest) string {
	if request == nil {
		return ""
	}
	if request.ReasoningEffort != "" {
		return request.ReasoningEffort
	}
	if len(request.Reasoning) > 0 {
		var r struct {
			Effort string `json:"effort"`
		}
		if json.Unmarshal(request.Reasoning, &r) == nil {
			return r.Effort
		}
	}
	return ""
}

// ApplyRequestedReasoning turns OpenRouter's reasoning:{"effort":...} into
// thinking when the converter produced none. The converter only honours
// reasoning.max_tokens, so an effort-only reasoning object -- the shape
// OpenRouter documents and most of its clients send -- was dropped and the
// model answered without thinking: a 200 that silently ignored the parameter
// (measured 2026-09-25 on claude-opus-4-5, -sonnet-4-5, -opus-4-8, -sonnet-4-6;
// OpenRouter returned thinking for all four). The enabled shape it builds is
// then normalised by NormalizeThinkingShape like any other.
func ApplyRequestedReasoning(claudeRequest *dto.ClaudeRequest, request *dto.GeneralOpenAIRequest) {
	if claudeRequest == nil || request == nil || claudeRequest.Thinking != nil || len(request.Reasoning) == 0 {
		return
	}
	var r struct {
		Enabled *bool  `json:"enabled"`
		Effort  string `json:"effort"`
	}
	if json.Unmarshal(request.Reasoning, &r) != nil {
		return
	}
	if r.Enabled != nil && !*r.Enabled {
		return
	}
	budget, known := effortBudgets[r.Effort]
	if !known {
		if r.Enabled == nil || r.Effort == "none" {
			return // nothing asked for thinking
		}
		budget = effortBudgets["medium"] // {"enabled":true}: OpenRouter's default effort
	}
	budget = fitBudget(claudeRequest, budget)
	claudeRequest.Thinking = &dto.Thinking{Type: "enabled", BudgetTokens: &budget}
	// Extended thinking accepts no temperature but 1, and no top_k.
	if claudeRequest.Temperature != nil && *claudeRequest.Temperature != 1 {
		claudeRequest.Temperature = nil
	}
	claudeRequest.TopK = nil
}
