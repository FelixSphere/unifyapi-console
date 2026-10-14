package i18n

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// OpenRouter answers the same situation with the byte-identical sentence
// "<model> is not a valid model ID". Ours used to say exactly that, so a 400 in
// the logs gave no way to tell whether this gateway had rejected the name or
// merely forwarded an upstream rejection -- and that cost real time during an
// incident review, when a passthrough was briefly mistaken for a regression we
// had just shipped.
func TestUnknownModelMessageCannotBeMistakenForUpstream(t *testing.T) {
	const upstreamWording = "is not a valid model ID"

	for _, locale := range []string{"en", "zh-CN", "zh-TW"} {
		t.Run(locale, func(t *testing.T) {
			raw, err := os.ReadFile("locales/" + locale + ".yaml")
			require.NoError(t, err)

			var line string
			for _, candidate := range strings.Split(string(raw), "\n") {
				if strings.HasPrefix(candidate, "distributor.unknown_model:") {
					line = candidate
					break
				}
			}
			require.NotEmpty(t, line, "distributor.unknown_model is missing from %s", locale)

			assert.NotContains(t, line, upstreamWording,
				"this is OpenRouter's exact wording; a log line would not say who rejected the request")
			assert.Contains(t, line, "{{.Model}}",
				"the caller has to be told which name was rejected")
		})
	}
}
