package model

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The first commit said the table's retention was "bounded from the start".
// DeletePlatformEventsBefore existed and NOTHING called it, so the bound was a
// sentence in a commit message and the table grew without limit. A function
// that is never invoked looks identical to one that works.
func TestRetentionIsActuallyStartedNotJustDefined(t *testing.T) {
	body, err := os.ReadFile("../main.go")
	require.NoError(t, err)
	assert.Contains(t, string(body), "model.StartPlatformEventRetention(",
		"platform_events retention must be started at boot; defining the delete function is not the same as bounding the table")
}

func TestRetentionDefaultsRatherThanDeletingEverything(t *testing.T) {
	src, err := os.ReadFile("platform_event.go")
	require.NoError(t, err)
	fn := string(src)
	start := strings.Index(fn, "func StartPlatformEventRetention")
	require.Greater(t, start, 0)
	body := fn[start : start+strings.Index(fn[start:], "\nfunc ")]

	// keepDays <= 0 must become a sane default. Treating 0 as "keep nothing"
	// would silently empty the table on a misconfiguration.
	assert.Contains(t, body, "if keepDays <= 0", "a non-positive retention must not mean 'delete everything'")
	assert.Contains(t, body, "recover()", "retention runs in a goroutine; a panic there takes the process down")
	assert.Contains(t, body, "DB == nil", "retention starts before InitDB on a cold boot")
}

func TestResolveIPOwnersIsSafeBeforeTheDatabaseExists(t *testing.T) {
	saved := DB
	DB = nil
	t.Cleanup(func() { DB = saved })

	assert.NotPanics(t, func() {
		got := ResolveIPOwners([]string{"185.196.0.16", "", "185.196.0.16"})
		assert.Empty(t, got, "no database means 'not matched', never a wrong match")
	})
	assert.Empty(t, ResolveIPOwners(nil))
}
