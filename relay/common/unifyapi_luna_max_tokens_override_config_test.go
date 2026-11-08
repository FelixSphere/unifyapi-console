package common

import (
	"testing"

	"github.com/tidwall/gjson"

	commonpkg "github.com/QuantumNous/new-api/common"
)

// lunaMaxTokensOverride is the param_override set on production channel 216
// ("Flatkey OpenAI · gpt-6-luna") on 2026-11-08. Prior value: empty.
//
// gpt-6-luna is a reasoning model and the upstream rejects max_tokens with
//
//	400 Unsupported parameter: 'max_tokens' is not supported with this model.
//	    Use 'max_completion_tokens' instead.
//
// The relay rewrites max_tokens -> max_completion_tokens only for
// dto.IsOpenAIReasoningOModel (o1/o3/o4) and dto.IsOpenAIGPT5Model (prefix
// "gpt-5"), so gpt-6-* is not covered. Per the "configuration before code"
// operator rule this is fixed with a channel param_override, not a new prefix
// in Go. Probed 2026-11-08: gpt-6-astra, gpt-6-sol and gpt-6.1-sol all accept
// max_tokens (HTTP 200), so no other channel needs this.
//
// Two operations, because `move` FAILS THE WHOLE REQUEST when the source path
// is missing (moveValue -> "source path does not exist" -> applyOperations
// returns an error -> newAPIErrorFromParamOverride, which sets SkipRetry).
// Every operation is therefore guarded by an existence condition, and the
// second operation covers a caller that sent both fields.
//
// On the `gte` guards: compareNumeric errors on a non-number, which would fail
// the request. That is safe here only because param_override runs on the
// re-marshalled DTO (compatible_handler.go and responses_handler.go both do
// common.Marshal(convertedRequest) first), and GeneralOpenAIRequest declares
// MaxTokens/MaxCompletionTokens as *uint with omitempty -- a JSON null or a
// string never survives to the override engine. If that ever changes, or if a
// raw-body path starts applying overrides, these guards must become type-safe
// (mode "prefix" with value "", or mode "full" against null with invert).
// Note also that enabling pass-through body on this channel SKIPS param
// override entirely and brings the 400 back.
const lunaMaxTokensOverride = `{"operations":[{"mode":"move","from":"max_tokens","to":"max_completion_tokens","logic":"AND","conditions":[{"path":"max_tokens","mode":"gte","value":0},{"path":"max_completion_tokens","mode":"gte","value":0,"invert":true,"pass_missing_key":true}]},{"mode":"delete","path":"max_tokens","logic":"AND","conditions":[{"path":"max_tokens","mode":"gte","value":0},{"path":"max_completion_tokens","mode":"gte","value":0}]}]}`

func TestLunaMaxTokensOverrideConfig(t *testing.T) {
	var override map[string]interface{}
	if err := commonpkg.Unmarshal([]byte(lunaMaxTokensOverride), &override); err != nil {
		t.Fatalf("override JSON does not parse: %v", err)
	}

	// Bodies as the override engine actually sees them: post-DTO, so every
	// token field is either an integer or absent.
	cases := []struct {
		name     string
		body     string
		wantMax  string // expected max_tokens; "" = absent
		wantComp string // expected max_completion_tokens; "" = absent
	}{
		{"max_tokens rewritten", `{"model":"gpt-6-luna","max_tokens":256}`, "", "256"},
		{"already correct -> untouched", `{"model":"gpt-6-luna","max_completion_tokens":256}`, "", "256"},
		{"neither set -> request still succeeds", `{"model":"gpt-6-luna","messages":[]}`, "", ""},
		{"both set -> max_tokens dropped, caller's value kept", `{"model":"gpt-6-luna","max_tokens":256,"max_completion_tokens":512}`, "", "512"},
		{"streaming body with other params", `{"model":"gpt-6-luna","max_tokens":1024,"stream":true,"temperature":0.7}`, "", "1024"},
		{"alias mapped to luna upstream", `{"model":"my-alias","max_tokens":64}`, "", "64"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := map[string]interface{}{"model": "gpt-6-luna", "upstream_model": "gpt-6-luna"}
			out, err := ApplyParamOverride([]byte(tc.body), override, ctx)
			if err != nil {
				t.Fatalf("ApplyParamOverride: %v", err)
			}
			check := func(path, want string) {
				got := gjson.GetBytes(out, path)
				if want == "" {
					if got.Exists() && got.Type != gjson.Null {
						t.Fatalf("%s = %q, want absent; body %s", path, got.String(), out)
					}
					return
				}
				if got.String() != want {
					t.Fatalf("%s = %q, want %q; body %s", path, got.String(), want, out)
				}
			}
			check("max_tokens", tc.wantMax)
			check("max_completion_tokens", tc.wantComp)
		})
	}
}

// TestLunaMaxTokensOverrideAssumesDTOMarshalling pins the assumption the live
// config depends on: the engine DOES error on a null or string max_tokens, so
// the only thing keeping channel 216 safe is that those shapes cannot reach
// it. If someone makes the guards type-safe this test can go; until then it
// documents why "it works in production" and "the engine rejects it" are both
// true.
func TestLunaMaxTokensOverrideAssumesDTOMarshalling(t *testing.T) {
	var override map[string]interface{}
	if err := commonpkg.Unmarshal([]byte(lunaMaxTokensOverride), &override); err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, body := range []string{
		`{"model":"gpt-6-luna","max_tokens":null}`,
		`{"model":"gpt-6-luna","max_completion_tokens":null}`,
		`{"model":"gpt-6-luna","max_tokens":"256"}`,
	} {
		ctx := map[string]interface{}{"model": "gpt-6-luna", "upstream_model": "gpt-6-luna"}
		if _, err := ApplyParamOverride([]byte(body), override, ctx); err == nil {
			t.Fatalf("expected the engine to reject %s; the gte guards are now type-safe and the comment above is stale", body)
		}
	}
}
