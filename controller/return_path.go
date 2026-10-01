package controller

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/setting/system_setting"
)

// dashboardURL joins a dashboard path onto the configured server address.
// ServerAddress is operator-entered, so it has carried a trailing slash before;
// the trim lives here rather than at every call site.
func dashboardURL(suffix string) string {
	return strings.TrimRight(system_setting.ServerAddress, "/") + suffix
}

func paymentReturnPath(suffix string) string {
	return dashboardURL(suffix)
}

// passwordResetLink builds the link mailed to someone who forgot their
// password. QueryEscape, not raw interpolation: "+" is a legal character in a
// mailbox name and decodes to a space in a query string, so a plus-addressed
// account received a link whose email no longer matched the one its code was
// filed under, and the confirm step answered "link is invalid or has expired".
func passwordResetLink(email, code string) string {
	return dashboardURL(fmt.Sprintf("/user/reset?email=%s&token=%s",
		url.QueryEscape(email), url.QueryEscape(code)))
}
