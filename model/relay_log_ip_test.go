package model

import (
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// The client IP on a relay log is what lets a support report ("we sent
// requests and got nothing") or an abuse report be answered at all: without
// it an address resolves to an account only if that account also happens to
// have signed in to the console from the same address, which in production
// was true for 2 of 31 observed addresses.
//
// It used to be written only when the log's own user had switched
// `record_ip_log` on. Nobody ever had -- 0 of 185 production logins, in
// either direction -- so the column was empty on every one of 76441 consume
// rows and 11038 error rows. These tests pin the two halves of the fix: the
// platform records the address unconditionally, and the user-facing switch
// now governs only whether that address is shown back to the user.

func TestClientIPForLogSurvivesAContextWithoutARequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.Nil(t, c.Request, "precondition: a freshly created test context has no request")

	// gin's ClientIP dereferences c.Request.RemoteAddr with no nil check, so
	// this is a panic, not an empty string, if the guard is removed. It runs
	// inside the writer that turns a deducted quota into a billable row.
	require.NotPanics(t, func() {
		require.Equal(t, "", clientIPForLog(c))
	})

	require.NotPanics(t, func() {
		require.Equal(t, "", clientIPForLog(nil))
	})
}

func TestClientIPForLogReturnsTheAddressWhenThereIsOne(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.RemoteAddr = "203.0.113.9:51314"
	c.Request = req

	require.Equal(t, "203.0.113.9", clientIPForLog(c))
}

func TestUserLogViewHidesTheIPUnlessTheUserAskedForIt(t *testing.T) {
	for _, logType := range []int{LogTypeConsume, LogTypeError} {
		logs := []*Log{{Type: logType, Ip: "203.0.113.9", Other: "{}"}}
		formatUserLogs(logs, 0, false)
		require.Equal(t, "", logs[0].Ip, "type %d: a user who did not switch it on must not be shown the address", logType)

		logs = []*Log{{Type: logType, Ip: "203.0.113.9", Other: "{}"}}
		formatUserLogs(logs, 0, true)
		require.Equal(t, "203.0.113.9", logs[0].Ip, "type %d: a user who did switch it on must see it", logType)
	}
}

// The switch is about usage and error logs, which is what it says on the
// settings page. Login and audit rows have carried an address for as long as
// they have existed and the user can read it today, so the strip must not
// reach them -- taking that away would be a regression dressed as a privacy
// improvement.
func TestUserLogViewStillShowsTheIPOnLoginAndAuditRows(t *testing.T) {
	for _, logType := range []int{LogTypeLogin, LogTypeManage, LogTypeTopup} {
		logs := []*Log{{Type: logType, Ip: "203.0.113.9", Other: "{}"}}
		formatUserLogs(logs, 0, false)
		require.Equal(t, "203.0.113.9", logs[0].Ip, "type %d must keep the address it has always shown", logType)
	}
}

// Both relay log writers must stamp the address with no reference to the
// user's setting. Asserting it against a live database would need a database;
// asserting it against the source catches the thing that actually goes wrong,
// which is someone restoring the per-user gate -- and with it both the empty
// column and a per-request GetUserSetting lookup on the hot path.
func TestRelayLogWritersDoNotConsultTheUserSettingForTheIP(t *testing.T) {
	src, err := os.ReadFile("log.go")
	require.NoError(t, err)

	for _, fn := range []string{"func RecordErrorLog(", "func RecordConsumeLog("} {
		body := functionBodyForIPTest(t, string(src), fn)

		require.Contains(t, body, "Ip:                clientIPForLog(c),",
			"%s must stamp the client IP unconditionally", fn)
		require.NotContains(t, body, "RecordIpLog",
			"%s must not gate the IP on the user's display setting", fn)
		require.NotContains(t, body, "GetUserSetting",
			"%s is the per-request hot path; it must not read the user setting", fn)
	}
}

func functionBodyForIPTest(t *testing.T, src string, signature string) string {
	t.Helper()
	start := strings.Index(src, signature)
	require.GreaterOrEqual(t, start, 0, "%s not found in model/log.go", signature)
	rest := src[start:]
	end := regexp.MustCompile(`(?m)^}$`).FindStringIndex(rest)
	require.NotNil(t, end, "could not find the end of %s", signature)
	return rest[:end[1]]
}
