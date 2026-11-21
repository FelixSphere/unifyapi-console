package router

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSub2APIGatewayKeepsNativeRoutesAndCredentialsSeparate(t *testing.T) {
	type receivedRequest struct {
		path, query, key, cookie, user, body string
	}
	incoming := make(chan receivedRequest, 10)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		incoming <- receivedRequest{r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"),
			r.Header.Get("Cookie"), r.Header.Get("New-Api-User"), string(body)}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Set-Cookie", "session=upstream")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("data: picture\n\n"))
	}))
	defer upstream.Close()

	t.Setenv("UNIFYAPI_SUB2API_GATEWAY_URL", upstream.URL)
	engine := gin.New()
	setSub2APIRouter(engine)
	engine.GET("/v1/models", func(c *gin.Context) { c.String(http.StatusOK, "UnifyAPI models") })

	for _, path := range []string{"/v1/models", "/sub2api/api/v1/admin/accounts",
		"/sub2api/v1/../api/v1/admin/accounts", "/sub2api/v1/%252e%252e/api/v1/admin/accounts",
		"/sub2api/v1/%2e%2e/api/v1/admin/accounts", "/sub2api/v1//models", "/sub2api/v1/%5c../models"} {
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if path == "/v1/models" {
			require.Equal(t, "UnifyAPI models", w.Body.String())
		} else {
			require.Equal(t, http.StatusNotFound, w.Code, path)
		}
	}
	require.Empty(t, incoming, "native and admin paths must never hit Sub2API")

	w := httptest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/sub2api/v1/images/generations/async?trace=yes", strings.NewReader(`{"model":"image"}`))
	req.Header.Set("Authorization", "Bearer sub2api-fixture-key")
	req.Header.Set("Cookie", "session=unify")
	req.Header.Set("New-Api-User", "123")
	engine.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)
	require.Equal(t, receivedRequest{
		path: "/v1/images/generations/async", query: "trace=yes", key: "Bearer sub2api-fixture-key", body: `{"model":"image"}`,
	}, <-incoming)
	require.Equal(t, "data: picture\n\n", w.Body.String())
	require.Empty(t, w.Header().Get("Set-Cookie"))
	require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
}

func TestSub2APIGatewayDisabledOrRejectsUpstreamConsole(t *testing.T) {
	for _, contentType := range []string{"text/html; charset=utf-8", "redirect"} {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if contentType == "redirect" {
				http.Redirect(w, r, "/api/v1/admin/accounts", http.StatusFound)
				return
			}
			w.Header().Set("Content-Type", contentType)
			_, _ = w.Write([]byte("<!doctype html>"))
		}))
		t.Setenv("UNIFYAPI_SUB2API_GATEWAY_URL", upstream.URL)
		engine := gin.New()
		setSub2APIRouter(engine)
		w := httptest.NewRecorder()
		ctx, cancel := context.WithCancel(context.Background())
		engine.ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodGet, "/sub2api/v1/models", nil))
		cancel()
		upstream.Close()
		require.Equal(t, http.StatusBadGateway, w.Code)
		require.NotContains(t, w.Body.String(), "<!doctype html>")
		require.Empty(t, w.Header().Get("Location"))
	}

	t.Setenv("UNIFYAPI_SUB2API_GATEWAY_URL", "")
	engine := gin.New()
	setSub2APIRouter(engine)
	require.Empty(t, engine.Routes())
}

func TestSub2APIImageTaskPollURLStaysInGateway(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Location", "/v1/images/tasks/imgtask_1")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"imgtask_1","poll_url":"/v1/images/tasks/imgtask_1"}`))
	}))
	defer upstream.Close()

	t.Setenv("UNIFYAPI_SUB2API_GATEWAY_URL", upstream.URL)
	engine := gin.New()
	setSub2APIRouter(engine)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodPost, "/sub2api/v1/images/generations/async", nil))
	require.Equal(t, http.StatusAccepted, w.Code)
	require.Equal(t, "/sub2api/v1/images/tasks/imgtask_1", w.Header().Get("Location"))
	require.JSONEq(t, `{"id":"imgtask_1","poll_url":"/sub2api/v1/images/tasks/imgtask_1"}`, w.Body.String())
}
