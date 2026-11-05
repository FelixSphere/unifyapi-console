package middleware

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// UNIFYAPI-FORK: these pin the two properties the rejection line is for --
// that it identifies the key well enough to match against tokens.key, and
// that it never carries the key itself.

func TestRedactedKeyPrefixMatchesTheTokensKeyColumn(t *testing.T) {
	// The real shape: tokens.key for the customer in the 2026-11-04 report
	// begins "Wxb1MnvY", which is exactly what `select left(key,8)` returns,
	// so an operator can match the log line against the column directly.
	got := redactedKeyPrefix("Wxb1MnvYrest0fTheSecret")
	require.True(t, strings.HasPrefix(got, "Wxb1MnvY"),
		"the prefix must be the first 8 chars so it can be matched against left(key,8); got %q", got)
}

func TestRedactedKeyPrefixNeverCarriesTheWholeKey(t *testing.T) {
	const secret = "Wxb1MnvYrest0fTheSecret"
	assert.NotContains(t, redactedKeyPrefix(secret), secret,
		"the full key must never reach a log readable by anyone with SSM access")

	// A short string is reported by length rather than echoed: echoing it
	// would publish the whole of a key that merely happens to be short.
	short := redactedKeyPrefix("abc")
	assert.NotContains(t, short, "abc")
	assert.Contains(t, short, "3")
}

func TestRedactedKeyPrefixSeparatesNoKeyFromWrongKey(t *testing.T) {
	// These look identical to a caller and have opposite fixes: one is a
	// missing Authorization header, the other is a bad credential.
	assert.Equal(t, "(no key sent)", redactedKeyPrefix(""))
	assert.NotEqual(t, redactedKeyPrefix(""), redactedKeyPrefix("Wxb1MnvYrest"))
}

func TestLogAuthRejectionReportsCallerAndPathWithoutPanicking(t *testing.T) {
	// c.Request must be present: logAuthRejection reads Context(), URL and
	// User-Agent off it, and auth middleware runs with a real request.
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("User-Agent", "Python-urllib/3.14")
	c.Request = req

	require.NotPanics(t, func() {
		logAuthRejection(c, "token not recognised", "Wxb1MnvYrest")
	})
}
