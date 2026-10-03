// UNIFYAPI-BRAND: ours. Upstream has no file of this name.
package types

import (
	"encoding/json"
	"strings"
	"testing"

	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
)

// The payload below is copied verbatim from a production request log on
// 2026-09-25 (request 202609251659200926658838268d9d62NNSGqzn, channel 144 "OR
// Qwen . qwen3.7-max"). Keeping the real bytes matters: the raw field carries
// SSE framing and trailing blank lines that a hand-written fixture would tidy
// away, and that framing is exactly what the parser has to survive.
const prodQwenMetadata = `{"raw":"data: {\"error\":{\"code\":\"invalid_parameter_error\",\"param\":null,\"message\":\"max_completion_tokens [512] must be greater than thinking_budget [600]\",\"type\":\"invalid_request_error\"},\"id\":\"chatcmpl-5f9f4c97-ee75-96ec-a860-f0d4a25a283b\"}\n\n","provider_name":"Alibaba","is_byok":false}`

func TestDescribeUpstreamErrorSurfacesProviderSentence(t *testing.T) {
	got := DescribeUpstreamError("Provider returned error", json.RawMessage(prodQwenMetadata))

	// The provider's own sentence, verbatim -- not paraphrased.
	if !strings.Contains(got, "max_completion_tokens [512] must be greater than thinking_budget [600]") {
		t.Fatalf("provider sentence missing from %q", got)
	}
	if !strings.Contains(got, "Alibaba rejected the request") {
		t.Errorf("provider not attributed in %q", got)
	}
	// OpenRouter's contentless placeholder is gone, and so is the JSON envelope.
	if strings.Contains(got, "Provider returned error") {
		t.Errorf("placeholder should be replaced once we have the real sentence: %q", got)
	}
	if strings.Contains(got, `\"`) || strings.Contains(got, "is_byok") {
		t.Errorf("raw JSON envelope leaked into the caller-facing message: %q", got)
	}
}

func TestDescribeUpstreamErrorNamesTheCallersOwnParameters(t *testing.T) {
	got := DescribeUpstreamError("Provider returned error", json.RawMessage(prodQwenMetadata))

	// This is the whole point of the change: the caller sent max_tokens and
	// reasoning.max_tokens, and the vendor complains about two names that appear
	// nowhere in their request.
	if !strings.Contains(got, `max_completion_tokens is the "max_tokens" you sent`) {
		t.Errorf("missing max_tokens alias in %q", got)
	}
	if !strings.Contains(got, `thinking_budget is the "reasoning.max_tokens" you sent`) {
		t.Errorf("missing reasoning.max_tokens alias in %q", got)
	}

	// Ordered by position in the sentence, not by map iteration, or this test
	// passes and fails at random.
	maxAt := strings.Index(got, "max_completion_tokens is the")
	budgetAt := strings.Index(got, "thinking_budget is the")
	if maxAt < 0 || budgetAt < 0 || maxAt > budgetAt {
		t.Errorf("aliases should follow the order they appear in the message: %q", got)
	}
}

// The alias text is worthless if the sanitiser eats it on the way out.
// MaskSensitiveInfo runs on every client-facing error, and it previously
// rewrote anything containing a dot: "thinking.type.enabled ..." reached
// customers as "***.***.enabled". reasoning.max_tokens is exactly that shape.
func TestDescribeUpstreamErrorSurvivesSensitiveInfoMasking(t *testing.T) {
	got := kitutil.MaskSensitiveInfo(
		DescribeUpstreamError("Provider returned error", json.RawMessage(prodQwenMetadata)))

	if !strings.Contains(got, "reasoning.max_tokens") {
		t.Fatalf("masking destroyed the dotted parameter name: %q", got)
	}
	if strings.Contains(got, "***") {
		t.Errorf("nothing here is sensitive; masking should be a no-op: %q", got)
	}
}

// Everything the transform cannot improve must come out byte for byte as the
// old code produced it. This is what makes the change safe for the providers we
// have not reproduced.
func TestDescribeUpstreamErrorFallsBackToLegacyRendering(t *testing.T) {
	cases := []struct {
		name     string
		message  string
		metadata string
	}{
		{"metadata is not json", "Provider returned error", `not json at all`},
		{"metadata has no raw field", "Provider returned error", `{"provider_name":"Acme"}`},
		{"raw is not json", "Provider returned error", `{"raw":"upstream exploded","provider_name":"Acme"}`},
		{"raw carries no message", "Provider returned error", `{"raw":"{\"error\":{\"code\":\"x\"}}","provider_name":"Acme"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.message + " (" + tc.metadata + ")"
			if got := DescribeUpstreamError(tc.message, json.RawMessage(tc.metadata)); got != want {
				t.Errorf("legacy rendering changed\n got: %q\nwant: %q", got, want)
			}
		})
	}
}

func TestDescribeUpstreamErrorWithoutMetadataIsUnchanged(t *testing.T) {
	const msg = "some upstream said no"
	if got := DescribeUpstreamError(msg, nil); got != msg {
		t.Errorf("got %q, want %q", got, msg)
	}
	if got := DescribeUpstreamError("", nil); got != "" {
		t.Errorf("empty message should stay empty, got %q", got)
	}
}

// A provider that names a parameter we have an alias for still gets annotated
// even when there is no OpenRouter envelope in front of it.
func TestAnnotateAppliesToDirectProviderErrors(t *testing.T) {
	got := DescribeUpstreamError("thinking_budget [600] is too large", nil)
	if !strings.Contains(got, `thinking_budget is the "reasoning.max_tokens" you sent`) {
		t.Errorf("direct provider error not annotated: %q", got)
	}
}

// An unrelated error must not grow a spurious tail.
func TestAnnotateLeavesUnrelatedErrorsAlone(t *testing.T) {
	const msg = "rate limit exceeded, please retry"
	if got := DescribeUpstreamError(msg, nil); got != msg {
		t.Errorf("unrelated error was annotated: %q", got)
	}
}
