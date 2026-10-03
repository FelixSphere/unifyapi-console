// UNIFYAPI-BRAND: ours. Upstream has no file of this name.
//
// Make an upstream rejection say something the CALLER can act on.
//
// A request that OpenRouter forwards to a provider comes back wrapped twice.
// The caller sent this:
//
//	{"model":"qwen3.7-max","max_tokens":512,"reasoning":{"max_tokens":600}}
//
// and, before this file existed, received this:
//
//	Provider returned error ({"raw":"data: {\"error\":{\"code\":\"invalid_parameter_error\",
//	\"message\":\"max_completion_tokens [512] must be greater than thinking_budget [600]\"...}}",
//	"provider_name":"Alibaba","is_byok":false})
//
// Two separate problems. The sentence that matters is buried inside a JSON
// string inside a metadata blob, behind OpenRouter's own contentless "Provider
// returned error"; and once you dig it out it names max_completion_tokens and
// thinking_budget -- two parameters that appear NOWHERE in what the caller
// sent. They cannot map the complaint back onto their own request.
//
// This is deliberately additive and deliberately conservative:
//
//   - The provider's own sentence is preserved verbatim. We never paraphrase an
//     upstream, because a paraphrase would be us asserting something the vendor
//     did not say, and the vendor's exact wording is what a support ticket and a
//     web search need.
//   - When the envelope cannot be parsed, or carries nothing better than what we
//     already have, this returns the EXACT string the old code produced. Every
//     provider other than the ones we have actually reproduced keeps its current
//     behaviour byte for byte.
//   - callerParamAliases holds only names verified against production. Guessing
//     an alias would tell a caller to change a parameter that is not their
//     problem, which is worse than the raw vendor text.
package types

import (
	"encoding/json"
	"fmt"
	"strings"
)

// callerParamAliases maps a parameter name as it appears in UPSTREAM error text
// to the name the caller actually sent on an OpenAI-format request.
//
// Add an entry only after reproducing the rejection and confirming the mapping;
// see relaykit/types/upstream_error_text_unifyapi_test.go for the recorded
// production payload each one came from.
var callerParamAliases = map[string]string{
	// Alibaba (Qwen), reached through OpenRouter. Reproduced 2026-09-25 against
	// production: reasoning.max_tokens must be STRICTLY less than max_tokens,
	// despite the vendor wording "must be greater than" -- 511 passes against a
	// 512 cap, 512 does not.
	"max_completion_tokens": "max_tokens",
	"thinking_budget":       "reasoning.max_tokens",
}

// openRouterErrorMetadata is the shape OpenRouter puts in error.metadata when a
// downstream provider is the one that refused.
type openRouterErrorMetadata struct {
	Raw          string `json:"raw"`
	ProviderName string `json:"provider_name"`
}

// DescribeUpstreamError renders an upstream error for the caller.
//
// It returns the legacy `message (metadata)` rendering unless it can do strictly
// better, so a provider whose envelope we do not recognise is unaffected.
func DescribeUpstreamError(message string, metadata json.RawMessage) string {
	legacy := fmt.Sprintf("%s (%s)", message, metadata)
	if len(metadata) == 0 {
		return annotateCallerParams(message)
	}

	var meta openRouterErrorMetadata
	if err := json.Unmarshal(metadata, &meta); err != nil {
		return annotateCallerParams(legacy)
	}

	inner := extractProviderMessage(meta.Raw)
	if inner == "" {
		return annotateCallerParams(legacy)
	}

	// The provider's sentence, attributed, instead of OpenRouter's placeholder.
	// The placeholder is dropped only because we have something strictly more
	// informative to put in its place.
	described := inner
	if meta.ProviderName != "" {
		described = fmt.Sprintf("%s rejected the request: %s", meta.ProviderName, inner)
	}
	return annotateCallerParams(described)
}

// extractProviderMessage digs the provider's own message out of the raw field.
//
// The raw field is the provider's HTTP body as OpenRouter received it, so on a
// streaming request it still carries the SSE `data: ` framing and trailing
// blank lines.
func extractProviderMessage(raw string) string {
	candidate := strings.TrimSpace(raw)
	if candidate == "" {
		return ""
	}
	// Take the first SSE data frame if this is a stream; a rejection is a single
	// frame, and later frames would be a different error.
	for _, line := range strings.Split(candidate, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if line == "" || line == "[DONE]" {
			continue
		}
		candidate = line
		break
	}

	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(candidate), &body); err != nil {
		return ""
	}
	if body.Error.Message != "" {
		return body.Error.Message
	}
	return body.Message
}

// annotateCallerParams appends, once, the caller-side name of every upstream
// parameter the message mentions.
//
// Appended rather than substituted: the vendor's own token is what the caller
// will paste into a search or a support ticket, and substituting it would make
// our text disagree with every other record of the same failure.
func annotateCallerParams(message string) string {
	if message == "" {
		return message
	}
	var notes []string
	// Ordered by position in the message so the note reads in the same sequence
	// as the sentence it explains; map iteration order would shuffle it and make
	// the output untestable.
	type hit struct {
		at      int
		upsteam string
		caller  string
	}
	var hits []hit
	for upstream, caller := range callerParamAliases {
		if at := strings.Index(message, upstream); at >= 0 {
			hits = append(hits, hit{at: at, upsteam: upstream, caller: caller})
		}
	}
	if len(hits) == 0 {
		return message
	}
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && hits[j].at < hits[j-1].at; j-- {
			hits[j], hits[j-1] = hits[j-1], hits[j]
		}
	}
	for _, h := range hits {
		notes = append(notes, fmt.Sprintf("%s is the %q you sent", h.upsteam, h.caller))
	}
	return fmt.Sprintf("%s (%s)", message, strings.Join(notes, "; "))
}
