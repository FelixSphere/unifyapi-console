package controller

import (
	"net"
	"net/url"
	"os"
	"strings"
)

func sub2apiAccountsURL() string {
	raw := strings.TrimSpace(os.Getenv("UNIFYAPI_SUB2API_ADMIN_URL"))
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery ||
		u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return ""
	}
	loopback := u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback()
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return ""
	}
	u.Path = "/admin/accounts"
	u.RawPath = ""
	return u.String()
}
