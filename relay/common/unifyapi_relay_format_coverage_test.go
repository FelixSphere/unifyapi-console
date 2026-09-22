/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package common

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Adding a relay format means adding a case in several switches, and missing
// one fails late and confusingly. `system_one` shipped with the router, the
// validator and the handler all wired, but no case here -- so every
// authenticated request to the new endpoint died with "invalid relay format",
// and the unauthenticated probe that was supposed to prove the route worked
// returned 401 from the auth middleware long before it reached this code.
//
// This scans the router for the formats it actually dispatches and requires
// each to be handled, so the next one cannot be half-wired.
func TestEveryRoutedRelayFormatIsBuiltByGenRelayInfo(t *testing.T) {
	router, err := os.ReadFile("../../router/relay-router.go")
	require.NoError(t, err, "the router is the list of formats customers can reach")
	relayInfo, err := os.ReadFile("relay_info.go")
	require.NoError(t, err)

	routed := regexp.MustCompile(`controller\.Relay\(c, (types\.RelayFormat\w+)\)`).FindAllSubmatch(router, -1)
	require.NotEmpty(t, routed, "no dispatched formats found -- has the router moved?")

	handled := map[string]bool{}
	for _, match := range regexp.MustCompile(`case (types\.RelayFormat\w+):`).FindAllSubmatch(relayInfo, -1) {
		for _, name := range strings.Split(string(match[1]), ",") {
			handled[strings.TrimSpace(name)] = true
		}
	}

	var missing []string
	for _, match := range routed {
		name := string(match[1])
		if !handled[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	assert.Empty(t, missing,
		"these formats are routed but GenRelayInfo has no case for them, so every request to "+
			"their endpoint fails with \"invalid relay format\": %s", strings.Join(missing, ", "))
}

func TestGenRelayInfoBuildsASystemOneRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader("{}"))

	request := &dto.SystemOneRequest{
		Model:     "jev-1.13",
		State:     "ping",
		Questions: map[string]any{"reachable": map[string]any{"type": "noul"}},
	}

	info, err := GenRelayInfo(c, types.RelayFormatSystemOne, request, nil)
	require.NoError(t, err, "a routed format must build its info, not fall through to the default")
	require.NotNil(t, info)
	// The format constants after the first in their block are untyped string
	// constants, so compare by value rather than by dynamic type.
	assert.EqualValues(t, types.RelayFormatSystemOne, info.RelayFormat)
	assert.Equal(t, relayconstant.RelayModeSystemOne, info.RelayMode)
	assert.False(t, info.IsStream, "a decision model returns one typed answer; there is nothing to stream")
}

// The wrong DTO must say which type was expected rather than falling through
// to the catch-all, which is what made the original failure hard to place.
func TestGenRelayInfoRejectsAMismatchedSystemOneRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader("{}"))

	_, err := GenRelayInfo(c, types.RelayFormatSystemOne, &dto.RerankRequest{}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SystemOneRequest")
	assert.NotContains(t, err.Error(), "invalid relay format")
}
