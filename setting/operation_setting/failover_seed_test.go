package operation_setting

// UNIFYAPI-FORK: the seed that turns a second supplier into actual redundancy.
//
// Adding a supplier does nothing on its own. controller/relay.go picks the
// channel INSIDE the retry loop, so RetryTimes is the number of alternate
// channels a failed request may be re-sent to; at 0 the loop runs once and a
// healthy second supplier is never tried. 0 is where a rebuilt database lands,
// because the value's only home is the admin UI -- measured 2026-10-30, the
// hosts had already drifted apart there (production 2, staging 1).
//
// These assertions pin the two halves that have to agree: the seeded value,
// and the status codes it is allowed to act on.

import (
	"os"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const failoverSeedPath = "../../seed-failover.sql"

func seededRetryTimes(t *testing.T) int {
	t.Helper()
	body, err := os.ReadFile(failoverSeedPath)
	require.NoError(t, err, "seed-failover.sql must exist; production failover comes from it")

	m := regexp.MustCompile(`\('RetryTimes',\s*'(\d+)'\)`).FindSubmatch(body)
	require.NotNil(t, m, "seed-failover.sql does not set RetryTimes")

	n, err := strconv.Atoi(string(m[1]))
	require.NoError(t, err)
	return n
}

func TestSeededRetryTimesActuallyAllowsFailover(t *testing.T) {
	n := seededRetryTimes(t)

	// The whole point. 0 is the code default and means "one channel, no
	// failover" -- seeding that would be indistinguishable from not seeding.
	require.GreaterOrEqual(t, n, 1,
		"RetryTimes=%d gives no alternate channel, so a second supplier is spare capacity and not redundancy", n)

	// No upper bound is asserted. How many alternates to try is an operator
	// decision, set in System Settings -> Models -> Routing & Reliability and
	// already validated there as 0-10; a test that pinned a ceiling would turn
	// one engineer's preference into a gate on the operator's own dial.
}

func TestFailoverCoversTheSupplierEdgeErrorsWeActuallySee(t *testing.T) {
	// 520 is what FlatKey's Cloudflare returns when its origin fails -- a
	// supplier-side error another channel may well not have, so it must stay
	// retryable. (It is not evidence the seed was missing: the one 520 a
	// customer saw, on 2026-10-30, was served under RetryTimes=2.)
	assert.True(t, ShouldRetryByStatusCode(520),
		"520 is FlatKey's Cloudflare origin error; failover must be allowed to try another channel")

	// 524 must NOT be retried: the edge already spent its full timeout, so a
	// retry buys a second full timeout and a worse customer experience.
	assert.True(t, IsAlwaysSkipRetryStatusCode(524),
		"524 means the edge already burned its timeout; retrying doubles the wait")
	assert.True(t, IsAlwaysSkipRetryStatusCode(504), "504 is the same bargain as 524")

	// A deterministic client error gains nothing from another channel.
	assert.False(t, ShouldRetryByStatusCode(400),
		"a malformed request fails identically on every channel")
}
