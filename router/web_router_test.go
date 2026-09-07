package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The SPA's stale-bundle handling (web/src/lib/stale-bundle.ts) relies on two
// promises of the web router: an unknown path under /api answers with the
// relay-style 404 body rather than the SPA page, and every response, that 404
// included, carries the server's build id.
func newWebRouterUnderTest(t *testing.T, buildID string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	originalBuildID := common.BuildID
	t.Cleanup(func() { common.BuildID = originalBuildID })
	common.BuildID = buildID

	engine := gin.New()
	engine.Use(middleware.Version())
	SetWebRouter(engine, WebAssets{IndexPage: []byte("<html><body>spa</body></html>")})
	return engine
}

func TestWebRouterUnknownApiPathReturnsRelayNotFoundWithBuildID(t *testing.T) {
	engine := newWebRouterUnderTest(t, "build-under-test")

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/credit-contribution/self/", strings.NewReader("{}"))
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusNotFound, recorder.Code)
	assert.Equal(t, "build-under-test", recorder.Header().Get(common.BuildIDHeader))

	var payload struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	assert.Equal(t, "invalid_request_error", payload.Error.Type)
	assert.Equal(t, "Invalid URL (POST /api/credit-contribution/self/)", payload.Error.Message)
}

func TestWebRouterSpaFallbackCarriesBuildID(t *testing.T) {
	engine := newWebRouterUnderTest(t, "build-under-test")

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/billing/credit-supply", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "build-under-test", recorder.Header().Get(common.BuildIDHeader))
	assert.Equal(t, "no-cache", recorder.Header().Get("Cache-Control"))
	assert.Contains(t, recorder.Body.String(), "spa")
}

// A chunk from a previous build must fail fast (404), not come back as the
// SPA shell with 200: rspack only reports a ChunkLoadError immediately on a
// failed script load, and waits two minutes on a page that never registers it.
func TestWebRouterMissingStaticChunkIs404NotSpaShell(t *testing.T) {
	engine := newWebRouterUnderTest(t, "build-under-test")

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/static/js/async/1127.bee1faa10b.js", nil))

	require.Equal(t, http.StatusNotFound, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), "spa")
	assert.Equal(t, "build-under-test", recorder.Header().Get(common.BuildIDHeader))
}

// Reload must fetch a fresh shell whatever the query string: "/?aff=x" is
// still the shell, and the old Cache() keyed on RequestURI gave it a week.
func TestWebRouterShellWithQueryIsNotCached(t *testing.T) {
	engine := newWebRouterUnderTest(t, "build-under-test")

	for _, target := range []string{"/", "/?aff=partner", "/index.html", "/billing?tab=x"} {
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		assert.Equal(t, "no-cache", recorder.Header().Get("Cache-Control"), target)
	}
}

func TestWebRouterOmitsBuildIDHeaderWhenUnknown(t *testing.T) {
	engine := newWebRouterUnderTest(t, "")

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/does-not-exist", nil))

	require.Equal(t, http.StatusNotFound, recorder.Code)
	_, present := recorder.Header()[http.CanonicalHeaderKey(common.BuildIDHeader)]
	assert.False(t, present)
}
