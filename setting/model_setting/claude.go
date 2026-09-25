package model_setting

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

//var claudeHeadersSettings = map[string][]string{}
//
//var ClaudeThinkingAdapterEnabled = true
//var ClaudeThinkingAdapterMaxTokens = 8192
//var ClaudeThinkingAdapterBudgetTokensPercentage = 0.8

// ClaudeSettings 定义Claude模型的配置
type ClaudeSettings struct {
	HeadersSettings                       map[string]map[string][]string `json:"model_headers_settings"`
	DefaultMaxTokens                      map[string]int                 `json:"default_max_tokens"`
	ThinkingAdapterEnabled                bool                           `json:"thinking_adapter_enabled"`
	ThinkingAdapterBudgetTokensPercentage float64                        `json:"thinking_adapter_budget_tokens_percentage"`
	// AdaptiveThinkingModels lists models that reject thinking.type="enabled"
	// and accept only thinking.type="adaptive" with output_config.effort.
	//
	// This is configuration rather than a hardcoded prefix list because the
	// hardcoded lists are what let this break: claude-fable-5 shipped, rejected
	// the standard parameter, and nothing in the relay knew. A vendor that
	// changes a parameter shape on the next model should cost an option edit,
	// not a release.
	AdaptiveThinkingModels []string `json:"adaptive_thinking_models"`
	// AdaptiveThinkingUnsupportedModels is the mirror image: models that reject
	// thinking.type="adaptive" ("adaptive thinking is not supported on this
	// model") and accept only the enabled+budget_tokens shape. Measured on
	// 2026-09-25 against production channels 101 and 156; OpenRouter accepts
	// the adaptive shape for both, so a caller switching over got a 400 only
	// from us.
	AdaptiveThinkingUnsupportedModels []string `json:"adaptive_thinking_unsupported_models"`
	// StructuredOutputsBeta is the anthropic-beta value that makes output_format
	// do anything. Anthropic gates structured outputs behind a dated beta flag,
	// and without it the field is accepted and ignored: the request succeeds and
	// the model answers in prose. That is the exact silent failure the
	// response_format mapping set out to fix, so the mapping alone is inert.
	//
	// Configurable because the date moves. A new beta string should cost an
	// option edit, not a release.
	StructuredOutputsBeta string `json:"structured_outputs_beta"`
}

// 默认配置
var defaultClaudeSettings = ClaudeSettings{
	HeadersSettings:        map[string]map[string][]string{},
	ThinkingAdapterEnabled: true,
	DefaultMaxTokens: map[string]int{
		"default": 8192,
	},
	ThinkingAdapterBudgetTokensPercentage: 0.8,
	AdaptiveThinkingModels:                []string{"claude-fable-5"},
	AdaptiveThinkingUnsupportedModels:     []string{"claude-opus-4-5", "claude-sonnet-4-5"},
	StructuredOutputsBeta:                 "structured-outputs-2025-11-13",
}

// 全局实例
var claudeSettings = defaultClaudeSettings

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("claude", &claudeSettings)
}

// GetClaudeSettings 获取Claude配置
func GetClaudeSettings() *ClaudeSettings {
	// check default max tokens must have default key
	if _, ok := claudeSettings.DefaultMaxTokens["default"]; !ok {
		claudeSettings.DefaultMaxTokens["default"] = 8192
	}
	return &claudeSettings
}

// RequiresAdaptiveThinking reports whether the model refuses
// thinking.type="enabled".
//
// An entry matches the model exactly, or as a dated snapshot of it
// ("claude-fable-5" matches "claude-fable-5-20260801"). It deliberately does
// NOT match on bare prefix: "claude-fable-5" must not capture
// "claude-fable-5.1", which accepts the enabled shape and would otherwise be
// silently downgraded to adaptive, losing the caller's budget_tokens.
func (c *ClaudeSettings) RequiresAdaptiveThinking(model string) bool {
	return matchesModelList(c.AdaptiveThinkingModels, model)
}

// RejectsAdaptiveThinking reports whether the model refuses
// thinking.type="adaptive". Same matching rule as RequiresAdaptiveThinking.
func (c *ClaudeSettings) RejectsAdaptiveThinking(model string) bool {
	return matchesModelList(c.AdaptiveThinkingUnsupportedModels, model)
}

func matchesModelList(list []string, model string) bool {
	for _, candidate := range list {
		if candidate == "" {
			continue
		}
		if model == candidate || strings.HasPrefix(model, candidate+"-") {
			return true
		}
	}
	return false
}

func (c *ClaudeSettings) WriteHeaders(originModel string, httpHeader *http.Header) {
	if headers, ok := c.HeadersSettings[originModel]; ok {
		for headerKey, headerValues := range headers {
			mergedValues := normalizeHeaderListValues(
				append(append([]string(nil), httpHeader.Values(headerKey)...), headerValues...),
			)
			if len(mergedValues) == 0 {
				continue
			}
			httpHeader.Set(headerKey, strings.Join(mergedValues, ","))
		}
	}
}

func normalizeHeaderListValues(values []string) []string {
	normalizedValues := make([]string, 0, len(values))
	seenValues := make(map[string]struct{}, len(values))
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			normalizedItem := strings.TrimSpace(item)
			if normalizedItem == "" {
				continue
			}
			if _, exists := seenValues[normalizedItem]; exists {
				continue
			}
			seenValues[normalizedItem] = struct{}{}
			normalizedValues = append(normalizedValues, normalizedItem)
		}
	}
	return normalizedValues
}

func (c *ClaudeSettings) GetDefaultMaxTokens(model string) int {
	if maxTokens, ok := c.DefaultMaxTokens[model]; ok {
		return maxTokens
	}
	return c.DefaultMaxTokens["default"]
}

// ValidateClaudeDefaultMaxTokens validates the JSON persisted by the option
// API. Zero stays allowed — the current Messages API accepts max_tokens: 0 as
// cache pre-warming — but negative values are rejected because they would
// wrap into huge unsigned values during request conversion.
func ValidateClaudeDefaultMaxTokens(value string) error {
	var settings map[string]int
	if err := common.UnmarshalJsonStr(value, &settings); err != nil {
		return fmt.Errorf("Claude default max tokens must be a JSON map of model to integer: %w", err)
	}
	if settings == nil {
		return fmt.Errorf("Claude default max tokens must be a JSON map of model to integer")
	}
	for model, maxTokens := range settings {
		if maxTokens < 0 {
			return fmt.Errorf("negative Claude default max_tokens %d for %q", maxTokens, model)
		}
	}
	return nil
}

// WriteStructuredOutputsBeta merges the structured-outputs beta into
// anthropic-beta, preserving anything the caller already asked for there.
//
// anthropic-beta is a comma-separated list, so this must merge rather than set:
// a caller combining structured outputs with another beta would otherwise lose
// theirs.
func (c *ClaudeSettings) WriteStructuredOutputsBeta(httpHeader *http.Header) {
	if c.StructuredOutputsBeta == "" {
		return
	}
	mergedValues := normalizeHeaderListValues(
		append(append([]string(nil), httpHeader.Values("anthropic-beta")...), c.StructuredOutputsBeta),
	)
	if len(mergedValues) == 0 {
		return
	}
	httpHeader.Set("anthropic-beta", strings.Join(mergedValues, ","))
}
