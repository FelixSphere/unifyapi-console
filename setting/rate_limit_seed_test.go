package setting

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The seed file is the source of truth for production rate limits, and two
// mistakes in it would be invisible until a customer is throttled:
//
//  1. the per-group array is [total, success]; writing it the other way round
//     silently swaps a customer's limits,
//  2. ModelRequestRateLimitSuccessCount has no "0 means unlimited" escape, so
//     leaving it at the code default of 1000 caps every customer the moment the
//     feature is enabled.
//
// These read the real file rather than a copy, so editing the seed carelessly
// fails here instead of in production.

const seedPath = "../seed-rate-limits.sql"

func seedValue(t *testing.T, key string) string {
	t.Helper()
	body, err := os.ReadFile(seedPath)
	require.NoError(t, err, "seed file must exist; production rate limits come from it")

	re := regexp.MustCompile(`\('` + regexp.QuoteMeta(key) + `',\s*\n?\s*'([^']*)'\)`)
	m := re.FindSubmatch(body)
	require.NotNil(t, m, "seed file does not set %s", key)
	return string(m[1])
}

func TestSeededGroupRateLimitsAreTotalThenSuccess(t *testing.T) {
	raw := seedValue(t, "ModelRequestRateLimitGroup")

	require.NoError(t, UpdateModelRequestRateLimitGroupByJSONString(raw))
	t.Cleanup(func() { ModelRequestRateLimitGroup = map[string][2]int{} })

	total, success, found := GetGroupRateLimit("Vip User")
	require.True(t, found, "Vip User must have an override in the seed")

	// If the array order is ever flipped, success would exceed total, which is
	// nonsense: every success is also a request.
	assert.Greater(t, total, success,
		"[0] is the total (failures included) and must exceed [1], the success count")

	var parsed map[string][2]int
	require.NoError(t, json.Unmarshal([]byte(raw), &parsed))
	for group, limits := range parsed {
		assert.Greater(t, limits[0], limits[1],
			"group %q has total <= success, so the pair is probably reversed", group)
	}
}

func TestSeededLimitsAdmitTheBusiestMinuteEverObserved(t *testing.T) {
	// Production, 2026-09-24: the highest one-minute request count any single
	// user has produced since 2026-08-04 was 2003. A seed that does not clear
	// that would throttle traffic we already know to be legitimate.
	const observedPeakPerMinute = 2003

	var successCount int
	_, err := fmtSscan(seedValue(t, "ModelRequestRateLimitSuccessCount"), &successCount)
	require.NoError(t, err)

	assert.Greater(t, successCount, observedPeakPerMinute,
		"the global success limit must leave headroom above real traffic")

	raw := seedValue(t, "ModelRequestRateLimitGroup")
	var parsed map[string][2]int
	require.NoError(t, json.Unmarshal([]byte(raw), &parsed))
	for group, limits := range parsed {
		assert.Greater(t, limits[1], observedPeakPerMinute,
			"group %q would throttle a minute we have already seen in production", group)
	}
}

func fmtSscan(s string, out *int) (int, error) {
	v := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, os.ErrInvalid
		}
		v = v*10 + int(r-'0')
	}
	*out = v
	return 1, nil
}
