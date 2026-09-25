package setting

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A separate file from rate_limit_seed_test.go on purpose: that one is an
// existing test, and existing tests are not edited without the operator's
// approval (see AGENTS.md). Adding coverage is always allowed.

func tokenSeedValue(t *testing.T, key string) string {
	t.Helper()
	body, err := os.ReadFile("../seed-rate-limits.sql")
	require.NoError(t, err)

	re := regexp.MustCompile(`\('` + regexp.QuoteMeta(key) + `',\s*\n?\s*'([^']*)'\)`)
	m := re.FindSubmatch(body)
	require.NotNil(t, m, "seed file does not set %s", key)
	return string(m[1])
}

func TestSeededTokenLimitsAdmitTheBusiestMinuteEverObserved(t *testing.T) {
	// Production, 2026-09-24: the most tokens any single user spent in one
	// minute since 2026-08-04. A seed below this would throttle traffic we have
	// already served and been paid for.
	const observedPeakTokensPerMinute = 2_950_198

	raw := tokenSeedValue(t, "ModelRequestTokenLimitCount")
	var global int
	for _, r := range raw {
		require.True(t, r >= '0' && r <= '9', "limit must be a plain integer, got %q", raw)
		global = global*10 + int(r-'0')
	}
	assert.Greater(t, global, observedPeakTokensPerMinute,
		"the global token limit must leave headroom above real traffic")

	var groups map[string]int
	require.NoError(t, json.Unmarshal([]byte(tokenSeedValue(t, "ModelRequestTokenLimitGroup")), &groups))
	require.NotEmpty(t, groups)
	for group, limit := range groups {
		assert.Greater(t, limit, observedPeakTokensPerMinute,
			"group %q would throttle a minute we have already served", group)
	}
}

func TestSeededTokenGroupsParseIntoTheSettingTheMiddlewareReads(t *testing.T) {
	raw := tokenSeedValue(t, "ModelRequestTokenLimitGroup")
	require.NoError(t, UpdateModelRequestTokenLimitGroupByJSONString(raw))
	t.Cleanup(func() { ModelRequestTokenLimitGroup = map[string]int{} })

	limit, found := GetGroupTokenLimit("Vip User")
	require.True(t, found, "Vip User must have an override in the seed")
	assert.Positive(t, limit)

	_, missing := GetGroupTokenLimit("SomeCustomerAddedTomorrow")
	assert.False(t, missing, "an unlisted group must fall through to the global default")
}
