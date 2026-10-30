package gemini

import (
	"encoding/json"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/dto"
)

// Gemini's own thinkingBudget ranges (mirrors relaykit's clampThinkingBudget).
const (
	flashMaxBudget     = 24576
	flashLiteMinBudget = 512
	proMinBudget       = 128
	proMaxBudget       = 32768
)

// effortShare is the fraction of a model's maximum budget each effort word
// asks for -- the same proportions the "-thinking" suffix adapter already uses,
// so a caller gets the same thinking whichever way they asked for it.
var effortShare = map[string]int{"minimal": 5, "low": 20, "medium": 50, "high": 80, "xhigh": 100, "max": 100}

// ApplyRequestedThinking turns an OpenAI-format caller's reasoning request into
// Gemini thinkingConfig on native Gemini channels.
//
// The converter reads reasoning_effort only when the MODEL NAME carries a
// "-thinking" or effort suffix, and never reads OpenRouter's reasoning object.
// A plain model name with either parameter therefore reached Gemini with no
// thinkingConfig at all -- a 200 that silently ignored the request. It only
// showed on the flash-lite models, whose default budget is 0 (measured
// 2026-09-25, channels 124 and 129: 5 output tokens and no thoughts from us,
// 280-1400 reasoning tokens from OpenRouter for the same request, streaming and
// non-streaming alike); models that think by default hid the same drop.
//
// thinkingBudget rather than thinkingLevel: 2.5 models reject thinkingLevel,
// Gemini 3 still accepts thinkingBudget, and several channel models are
// "-latest" aliases whose generation is not visible here. It is the one shape
// that cannot 400 on a generation we guessed wrong.
//
// A thinkingConfig already present (from a model suffix) is left alone.
func ApplyRequestedThinking(geminiRequest *dto.GeminiChatRequest, model string, request *dto.GeneralOpenAIRequest) {
	if geminiRequest == nil || request == nil || geminiRequest.GenerationConfig.ThinkingConfig != nil {
		return
	}
	effort, budget, off := requestedReasoning(request)
	if off {
		// Only the 2.5 flash family can switch thinking off; the pros and the
		// Gemini 3 models refuse a zero budget ("Reasoning is mandatory").
		if strings.HasPrefix(model, "gemini-2.5-flash") {
			zero := 0
			geminiRequest.GenerationConfig.ThinkingConfig = &dto.GeminiThinkingConfig{ThinkingBudget: &zero}
		}
		return
	}
	if budget == 0 {
		share, known := effortShare[effort]
		if !known {
			return // nothing asked for thinking
		}
		budget = maxBudget(model) * share / 100
	}
	budget = clampBudget(model, budget)
	geminiRequest.GenerationConfig.ThinkingConfig = &dto.GeminiThinkingConfig{
		IncludeThoughts: true, // the caller asked to reason; return the thoughts, as OpenRouter does
		ThinkingBudget:  &budget,
	}
}

// requestedReasoning reads reasoning_effort, or OpenRouter's
// reasoning:{effort, max_tokens, enabled}. off means the caller explicitly
// turned reasoning off.
func requestedReasoning(request *dto.GeneralOpenAIRequest) (effort string, budget int, off bool) {
	effort = request.ReasoningEffort
	if len(request.Reasoning) > 0 {
		var r struct {
			Enabled   *bool  `json:"enabled"`
			Effort    string `json:"effort"`
			MaxTokens int    `json:"max_tokens"`
		}
		if json.Unmarshal(request.Reasoning, &r) == nil {
			if r.Enabled != nil && !*r.Enabled {
				return "", 0, true
			}
			if effort == "" {
				effort = r.Effort
			}
			budget = r.MaxTokens
			if effort == "" && budget == 0 && r.Enabled != nil && *r.Enabled {
				effort = "medium" // {"enabled":true}: OpenRouter's default effort
			}
		}
	}
	if effort == "none" {
		return "", 0, true
	}
	return effort, budget, false
}

func isPro(model string) bool { return strings.Contains(model, "-pro") }

func maxBudget(model string) int {
	if isPro(model) {
		return proMaxBudget
	}
	return flashMaxBudget
}

func clampBudget(model string, budget int) int {
	lo, hi := 1, flashMaxBudget
	switch {
	case isPro(model):
		lo, hi = proMinBudget, proMaxBudget
	case strings.Contains(model, "flash-lite"):
		lo = flashLiteMinBudget
	}
	if budget < lo {
		return lo
	}
	if budget > hi {
		return hi
	}
	return budget
}
