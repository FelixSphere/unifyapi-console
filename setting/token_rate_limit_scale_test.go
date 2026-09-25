package setting

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The existing seed tests only check the allowance clears TODAY's traffic,
// which is nine users. That would happily accept a value that binds the moment
// the gateway carries the scale it is being sized for. This pins the target
// scale itself.
//
// A third file rather than an addition to the other two: both are existing
// tests now, and existing tests are not edited without the operator's approval.

// Derived in the seed file's own comment, from production measurements:
//
//	500 seats, 30% active in a peak minute      = 150 concurrent
//	x 6 requests/min each (agentic tooling)     = 900 req/min
//	x the measured p95 of 38,680 tokens/request = 34,812,000 tokens/min
const tokensPerMinuteAt500Seats = 34_812_000

// requestsPerMinuteAt500Seats is the same 900 req/min with a 3x burst for CI
// runs and batch jobs.
const requestsPerMinuteAt500Seats = 2_700

func scaleSeedValue(t *testing.T, key string) string {
	t.Helper()
	body, err := os.ReadFile("../seed-rate-limits.sql")
	require.NoError(t, err)
	re := regexp.MustCompile(`\('` + regexp.QuoteMeta(key) + `',\s*\n?\s*'([^']*)'\)`)
	m := re.FindSubmatch(body)
	require.NotNil(t, m, "seed file does not set %s", key)
	return string(m[1])
}

func scaleSeedInt(t *testing.T, key string) int {
	t.Helper()
	raw := scaleSeedValue(t, key)
	out := 0
	for _, r := range raw {
		require.True(t, r >= '0' && r <= '9', "%s must be a plain integer, got %q", key, raw)
		out = out*10 + int(r-'0')
	}
	return out
}

func TestTheSeedClearsAFiveHundredSeatCompany(t *testing.T) {
	tokens := scaleSeedInt(t, "ModelRequestTokenLimitCount")
	assert.Greaterf(t, tokens, tokensPerMinuteAt500Seats,
		"the token allowance must clear %d tokens/min, the heavy-context load of one 500-seat customer",
		tokensPerMinuteAt500Seats)

	requests := scaleSeedInt(t, "ModelRequestRateLimitSuccessCount")
	assert.Greaterf(t, requests, requestsPerMinuteAt500Seats,
		"the request allowance must clear %d req/min, a 500-seat peak with burst",
		requestsPerMinuteAt500Seats)
}

func TestNamedCustomerGroupsAreNotTighterThanTheGlobalDefault(t *testing.T) {
	// A named group exists to give a customer MORE room. One accidentally set
	// below the default would silently punish the customers we care most about,
	// and nothing else would notice.
	globalTokens := scaleSeedInt(t, "ModelRequestTokenLimitCount")

	var groups map[string]int
	require.NoError(t, json.Unmarshal([]byte(scaleSeedValue(t, "ModelRequestTokenLimitGroup")), &groups))
	require.NotEmpty(t, groups)
	for name, limit := range groups {
		assert.GreaterOrEqualf(t, limit, globalTokens,
			"named group %q has a SMALLER token allowance (%d) than the global default (%d)",
			name, limit, globalTokens)
	}
}
