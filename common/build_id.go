package common

import (
	"regexp"
	"strings"
)

// BuildID identifies the frontend bundle compiled into this binary.
//
// Rsbuild stamps one id per production build into the bundle
// (import.meta.env.VITE_REACT_APP_BUILD_ID) and into index.html as
// <meta name="unifyapi-build" content="…">; main.go reads it back from the
// embedded page at startup. Unlike Version, which comes from the VERSION file
// and is not bumped per release, it changes on every build, so a browser tab
// can tell that the server it talks to was built from a different bundle than
// the one it is running. It is echoed on every response as BuildIDHeader and
// in /api/status as build_id; the consumer is web/src/lib/stale-bundle.ts.
var BuildID = ""

// BuildIDHeader carries BuildID on every HTTP response.
const BuildIDHeader = "X-UnifyAPI-Build"

var (
	buildIDMetaTag  = regexp.MustCompile(`<meta\b[^>]*\bname="unifyapi-build"[^>]*>`)
	metaContentAttr = regexp.MustCompile(`\bcontent="([^"]*)"`)
)

// ExtractBuildID returns the build id stamped into an index.html page, or ""
// when the page carries none (a dev build, or a dist produced before the
// stamping existed).
func ExtractBuildID(indexPage []byte) string {
	tag := buildIDMetaTag.Find(indexPage)
	if tag == nil {
		return ""
	}
	match := metaContentAttr.FindSubmatch(tag)
	if match == nil {
		return ""
	}
	return strings.TrimSpace(string(match[1]))
}
