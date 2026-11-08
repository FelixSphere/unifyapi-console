package common

import (
	"testing"

	"github.com/tidwall/gjson"

	commonpkg "github.com/QuantumNous/new-api/common"
)

// gpt6LunaChannelOverride is the param_override on production channel 216
// ("Flatkey OpenAI · gpt-6-luna") as of 2026-11-08 12:12Z. Three operations:
//
//  1. move max_tokens -> max_completion_tokens when only max_tokens is set
//     (gpt-6-luna answers 400 "Unsupported parameter: 'max_tokens'"; the
//     relay's own rename covers o-series and gpt-5*, not gpt-6*);
//  2. delete max_tokens when both are set, so the caller's
//     max_completion_tokens wins;
//  3. reasoning_effort "none" when a function tool is present at any position
//     and the caller set no reasoning (gpt-6-luna otherwise answers 400
//     "Function tools with reasoning_effort are not supported" on EVERY call).
//
// Backups: agent_backup_luna6_override_20261108 (417, original),
// agent_backup_luna6_override_20261108b (373), agent_backup_tools_pos_20261108
// (755, before the any-position tool check). Production is also watched by
// scripts/channel-config-drift.sh in the workspace repo.
const gpt6LunaChannelOverride = `{"operations":[{"mode":"move","from":"max_tokens","to":"max_completion_tokens","logic":"AND","conditions":[{"path":"max_tokens","mode":"full","value":null,"invert":true},{"path":"max_completion_tokens","mode":"full","value":null,"pass_missing_key":true}]},{"mode":"delete","path":"max_tokens","logic":"AND","conditions":[{"path":"max_tokens","mode":"prefix","value":""}]},{"path":"reasoning_effort","mode":"set","value":"none","logic":"AND","conditions":[{"path":"upstream_model","mode":"full","value":"gpt-6-luna"},{"path":"tools.#(type==\"function\").type","mode":"full","value":"function"},{"path":"reasoning_effort","mode":"prefix","value":"","invert":true,"pass_missing_key":true},{"path":"reasoning","mode":"prefix","value":"","invert":true,"pass_missing_key":true}]}]}`

func TestGPT6LunaChannelOverrideConfig(t *testing.T) {
	var override map[string]interface{}
	if err := commonpkg.Unmarshal([]byte(gpt6LunaChannelOverride), &override); err != nil {
		t.Fatalf("override JSON does not parse: %v", err)
	}
	fn := `{"type":"function","function":{"name":"get_weather"}}`
	custom := `{"type":"custom","custom":{"name":"g"}}`

	cases := []struct {
		name, upstream, body string
		effort, mct          string // expected values; "" = absent
	}{
		{"tools, effort unset, max_tokens", "gpt-6-luna", `{"max_tokens":300,"tools":[` + fn + `]}`, "none", "300"},
		{"tools, effort unset, max_completion_tokens", "gpt-6-luna", `{"max_completion_tokens":300,"tools":[` + fn + `]}`, "none", "300"},
		{"tools, effort unset, no cap", "gpt-6-luna", `{"tools":[` + fn + `]}`, "none", ""},
		{"custom tool first, function second", "gpt-6-luna", `{"tools":[` + custom + `,` + fn + `]}`, "none", ""},
		{"custom tool only", "gpt-6-luna", `{"tools":[` + custom + `]}`, "", ""},
		{"empty tools", "gpt-6-luna", `{"tools":[]}`, "", ""},
		{"tools, explicit medium kept", "gpt-6-luna", `{"reasoning_effort":"medium","tools":[` + fn + `]}`, "medium", ""},
		{"tools, reasoning object untouched", "gpt-6-luna", `{"reasoning":{"effort":"low"},"tools":[` + fn + `]}`, "", ""},
		{"no tools, max_tokens renamed", "gpt-6-luna", `{"max_tokens":50}`, "", "50"},
		{"both caps, caller's max_completion_tokens wins", "gpt-6-luna", `{"max_tokens":900,"max_completion_tokens":20}`, "", "20"},
		{"max_tokens null dropped, no error", "gpt-6-luna", `{"max_tokens":null}`, "", ""},
		{"max_tokens 0 carried", "gpt-6-luna", `{"max_tokens":0}`, "", "0"},
		{"other model: no effort added", "gpt-6-astra", `{"max_tokens":30,"tools":[` + fn + `]}`, "", "30"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := map[string]interface{}{"model": tc.upstream, "upstream_model": tc.upstream}
			out, err := ApplyParamOverride([]byte(tc.body), override, ctx)
			if err != nil {
				t.Fatalf("ApplyParamOverride: %v (an operation error fails the whole request)", err)
			}
			check := func(path, want string) {
				got := gjson.GetBytes(out, path)
				if want == "" {
					if got.Exists() {
						t.Fatalf("%s = %s, want absent; body %s", path, got.Raw, out)
					}
					return
				}
				if got.String() != want {
					t.Fatalf("%s = %q, want %q; body %s", path, got.String(), want, out)
				}
			}
			check("reasoning_effort", tc.effort)
			check("max_completion_tokens", tc.mct)
			check("max_tokens", "")
		})
	}
}
