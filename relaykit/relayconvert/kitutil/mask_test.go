package kitutil

import "testing"

// A vendor error names the parameter the caller must change. Masking it makes
// the message useless: the customer is told something is unsupported but not
// what to edit. Observed in production 2026-09-24 on claude-fable-5, where
// Anthropic's "thinking.type.enabled is not supported for this model, use
// thinking.type.adaptive and output_config.effort" reached the customer as
// `"***.***.enabled" is not supported`.
func TestMaskSensitiveInfoKeepsParameterPaths(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"anthropic thinking path", `"thinking.type.enabled" is not supported for this model. Use "thinking.type.adaptive" and "output_config.effort"`},
		{"go struct field", "json: cannot unmarshal string into Go struct field dto.GeneralOpenAIRequest.messages of type []dto.Message"},
		{"json pointer", "invalid value at messages.0.content"},
		{"response format", "response_format.json_schema.schema is required"},
		{"filename", "failed to read config.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MaskSensitiveInfo(tc.in); got != tc.in {
				t.Errorf("parameter path was masked\n  in:  %s\n  got: %s", tc.in, got)
			}
		})
	}
}

// The masking exists to keep upstream hostnames out of customer-facing errors.
// Fixing the false positives above must not weaken that.
func TestMaskSensitiveInfoStillMasksHosts(t *testing.T) {
	cases := []struct{ in, want string }{
		{"connect failed to api.openai.com", "connect failed to ***.***.com"},
		{"openrouter.ai refused", "***.ai refused"},
		{"console.flatkey.ai timed out", "***.***.ai timed out"},
		{"see https://api.test.org/v1/users", "see https://***.org/***/***"},
		{"host 192.168.1.10 unreachable", "host ***.***.***.*** unreachable"},
		{"sub.domain.co.uk failed", "***.***.co.uk failed"},
	}
	for _, tc := range cases {
		if got := MaskSensitiveInfo(tc.in); got != tc.want {
			t.Errorf("host not masked as expected\n  in:   %s\n  got:  %s\n  want: %s", tc.in, got, tc.want)
		}
	}
}
