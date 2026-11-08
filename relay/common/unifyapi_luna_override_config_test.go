package common

import (
	"testing"

	"github.com/tidwall/gjson"

	commonpkg "github.com/QuantumNous/new-api/common"
)

// lunaToolsOverride is the param_override set on production channel 1
// ("Flatkey OpenAI") on 2026-11-08 (backup table
// agent_backup_luna_override_20261108). It replaces the Go fix from console
// #280: gpt-5.6-luna intermittently reasons by default and then refuses
// function tools with a 400, so when a request carries tools and the caller
// set no reasoning, send reasoning_effort "none".
//
// This test pins that the override engine still evaluates this exact JSON
// the way the fix needs. If an upstream merge changes condition semantics
// (pass_missing_key, invert, context lookup of upstream_model), it fails here
// instead of silently in production.
const lunaToolsOverride = `{"operations":[{"path":"reasoning_effort","mode":"set","value":"none","logic":"AND","conditions":[{"path":"upstream_model","mode":"full","value":"gpt-5.6-luna"},{"path":"tools.0.type","mode":"full","value":"function"},{"path":"reasoning_effort","mode":"prefix","value":"","invert":true,"pass_missing_key":true},{"path":"reasoning","mode":"prefix","value":"","invert":true,"pass_missing_key":true}]}]}`

const lunaTestTools = `"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object"}}}]`

func TestLunaToolsOverrideConfig(t *testing.T) {
	var override map[string]interface{}
	if err := commonpkg.Unmarshal([]byte(lunaToolsOverride), &override); err != nil {
		t.Fatalf("override JSON does not parse: %v", err)
	}

	cases := []struct {
		name     string
		upstream string
		body     string
		want     string // expected reasoning_effort after override; "" = absent
	}{
		{"luna, tools, effort unset -> none", "gpt-5.6-luna",
			`{"model":"gpt-5.6-luna",` + lunaTestTools + `}`, "none"},
		{"luna, tools, explicit medium kept", "gpt-5.6-luna",
			`{"model":"gpt-5.6-luna","reasoning_effort":"medium",` + lunaTestTools + `}`, "medium"},
		{"luna, tools, reasoning object -> untouched", "gpt-5.6-luna",
			`{"model":"gpt-5.6-luna","reasoning":{"effort":"low"},` + lunaTestTools + `}`, ""},
		{"luna, no tools -> untouched", "gpt-5.6-luna",
			`{"model":"gpt-5.6-luna"}`, ""},
		{"astra, tools -> untouched (rejects none)", "gpt-6-astra",
			`{"model":"gpt-6-astra",` + lunaTestTools + `}`, ""},
		{"gpt-4o, tools -> untouched", "gpt-4o",
			`{"model":"gpt-4o",` + lunaTestTools + `}`, ""},
		{"alias mapped to luna upstream -> none", "gpt-5.6-luna",
			`{"model":"my-alias",` + lunaTestTools + `}`, "none"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := map[string]interface{}{"model": tc.upstream, "upstream_model": tc.upstream}
			out, err := ApplyParamOverride([]byte(tc.body), override, ctx)
			if err != nil {
				t.Fatalf("ApplyParamOverride: %v", err)
			}
			got := gjson.GetBytes(out, "reasoning_effort")
			if tc.want == "" {
				if got.Exists() {
					t.Fatalf("reasoning_effort = %q, want absent; body %s", got.String(), out)
				}
				return
			}
			if got.String() != tc.want {
				t.Fatalf("reasoning_effort = %q, want %q; body %s", got.String(), tc.want, out)
			}
		})
	}
}
