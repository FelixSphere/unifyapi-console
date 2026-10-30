/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package relay

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hostRouter lets a test use real, distinct hostnames while both of them are
// served by local test servers. Two httptest servers both answer on 127.0.0.1,
// so a redirect between them is same-host to net/http and keeps the key -- the
// opposite of what a real router.x -> console.x hop does. Without this the
// test would quietly prove nothing.
func hostRouter(t *testing.T, mapping map[string]string) *http.Client {
	t.Helper()
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if target, ok := mapping[host]; ok {
				addr = target
			}
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}}
}

// An aggregator that does not serve System One redirects the whole path space
// at its console host. Go follows that silently, drops the Authorization
// header on the way (a different domain), and the console answers 404 naming
// only the path -- which is exactly what an operator reported: "Invalid URL
// (POST /v1/systemone)" with no hint that another host had answered.
func TestSystemOneRedirectIsNamedWithTheHostThatActuallyAnswered(t *testing.T) {
	var consoleSawAuth string
	console := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		consoleSawAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid URL (POST /v1/systemone)"}}`))
	}))
	defer console.Close()

	consolePort := console.Listener.Addr().String()
	router := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://console.example.test:1/"+strings.TrimPrefix(r.URL.Path, "/"), http.StatusMovedPermanently)
	}))
	defer router.Close()

	client := hostRouter(t, map[string]string{
		"router.example.test":  router.Listener.Addr().String(),
		"console.example.test": consolePort,
	})

	requestedURL := "http://router.example.test:1/v1/systemone"
	req, err := http.NewRequest(http.MethodPost, requestedURL, strings.NewReader(`{"model":"jev-1.13"}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer sk-channel-key")

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// The premise: the hop really happened and really cost us the key.
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	require.Equal(t, "http://console.example.test:1/v1/systemone", resp.Request.URL.String())
	require.Empty(t, consoleSawAuth, "net/http drops Authorization off the original domain; that is why the upstream answers 404 rather than 401")

	note := describeUpstreamRedirect(requestedURL, resp)
	assert.Contains(t, note, requestedURL, "the operator must see what we asked for")
	assert.Contains(t, note, "http://console.example.test:1/v1/systemone", "and what answered instead")
	assert.Contains(t, note, "drops the channel's API key")
}

func TestSystemOneRedirectNoteIsSilentWhenTheUpstreamAnsweredDirectly(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer upstream.Close()

	requestedURL := upstream.URL + "/v1/systemone"
	resp, err := http.Post(requestedURL, "application/json", strings.NewReader("{}"))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Empty(t, describeUpstreamRedirect(requestedURL, resp),
		"an ordinary upstream error must not grow a redirect story")
}

// A redirect that stays on the domain (a trailing slash, say) still deserves
// naming, but it does not lose the key, so it must not claim it did.
func TestSystemOneSameDomainRedirectDoesNotClaimTheKeyWasDropped(t *testing.T) {
	var sawAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/systemone" {
			http.Redirect(w, r, "/v1/systemone/", http.StatusMovedPermanently)
			return
		}
		sawAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNotFound)
	}))
	defer upstream.Close()

	requestedURL := upstream.URL + "/v1/systemone"
	req, err := http.NewRequest(http.MethodPost, requestedURL, strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer sk-channel-key")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, "Bearer sk-channel-key", sawAuth, "same domain keeps the key")
	note := describeUpstreamRedirect(requestedURL, resp)
	assert.Contains(t, note, "redirected")
	assert.NotContains(t, note, "drops the channel's API key")
}

// net/http keeps the key on a hop to a subdomain and on a port change; the
// note must follow that rule rather than a simpler one that reads "same host".
func TestSystemOneCredentialRuleFollowsNetHTTP(t *testing.T) {
	assert.True(t, sameSiteForCredentials("flatkey.ai", "flatkey.ai"))
	assert.True(t, sameSiteForCredentials("flatkey.ai", "router.flatkey.ai"), "subdomain keeps the key")
	assert.True(t, sameSiteForCredentials("FlatKey.ai", "ROUTER.flatkey.ai"), "the comparison is case-insensitive")
	assert.False(t, sameSiteForCredentials("router.flatkey.ai", "console.flatkey.ai"), "a sibling host is not a subdomain")
	assert.False(t, sameSiteForCredentials("flatkey.ai", "evilflatkey.ai"), "a suffix is not a subdomain")
}

func TestSystemOneRedirectNoteToleratesAMissingResponse(t *testing.T) {
	assert.Empty(t, describeUpstreamRedirect("https://example.invalid/v1/systemone", nil))
	assert.Empty(t, describeUpstreamRedirect("", &http.Response{}))
	assert.Empty(t, describeUpstreamRedirect("https://example.invalid/v1/systemone", &http.Response{}))
}
