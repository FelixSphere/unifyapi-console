package middleware

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// UNIFYAPI-FORK: key material may only be logged for a key we do NOT hold.
//
// logAuthRejection records eight characters of the key the caller presented.
// That is safe where ValidateUserToken has just failed -- the value is not one
// of ours, and the prefix is what makes the line answerable ("which key are
// they actually sending?").
//
// It is NOT safe on the IP-allow-list branch. ValidateUserToken has already
// SUCCEEDED there: the token exists and is enabled, and the request is refused
// only because the client address is not on its list. Eight characters of a
// live 48-character key would go to the platform log, which a super admin can
// read from a web page -- and the console's own token API is UserAuth, so a
// root admin cannot otherwise see another user's key at all. The token is known
// by that point, so it is named instead.
//
// This reads the source because the property is about WHERE the call appears,
// which no runtime assertion can reach.
func TestOnlyUnrecognisedKeysAreEverLogged(t *testing.T) {
	body, err := os.ReadFile("auth.go")
	require.NoError(t, err)
	src := string(body)

	calls := regexp.MustCompile(`logAuthRejection\(c,\s*"([^"]+)"\s*,\s*key\)`).FindAllStringSubmatch(src, -1)
	require.Len(t, calls, 1,
		"logAuthRejection(..., key) leaks eight characters of the presented key; it may appear exactly once")
	assert.Equal(t, "token not recognised", calls[0][1],
		"the only rejection allowed to log key material is the one where the key is not ours")

	// The allow-list branch must still log something -- silently dropping it
	// would be a different regression, and that branch is reachable: one
	// production token carries an allow list.
	assert.Contains(t, src, `logAuthRejectionForToken(c, "client ip outside the token's allow list", token)`,
		"the allow-list rejection must still be recorded, by token rather than by key")
}

func TestTheTokenNamedRejectionCarriesNoKeyMaterial(t *testing.T) {
	body, err := os.ReadFile("auth.go")
	require.NoError(t, err)
	fn := string(body)
	start := strings.Index(fn, "func logAuthRejectionForToken")
	require.Greater(t, start, 0)
	end := strings.Index(fn[start:], "\nfunc ")
	require.Greater(t, end, 0)
	helper := fn[start : start+end]

	assert.NotContains(t, helper, "redactedKeyPrefix",
		"a rejection for a token we recognise must not reach for the key at all")
	assert.NotContains(t, helper, "key",
		"no key value, and no variable named key, may appear in this path")
}
