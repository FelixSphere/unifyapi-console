/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These routes read every customer's address and send mail in our name, so a
// customer must never reach them. The whole API router is mounted and called
// the way the console's own pages call it.
func TestOnlyStaffCanReachTheAdminEmailRoutes(t *testing.T) {
	engine := setupAccessTierRouteTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.RetentionEmailUnsubscribe{}))
	require.NoError(t, model.MigrateAdminEmail())
	prevSMTP := common.SMTPServer
	common.SMTPServer = "" // a send that got through auth must still not reach a mail server
	t.Cleanup(func() { common.SMTPServer = prevSMTP })

	admin := createChannelKeyRouteUser(t, "email-admin", common.RoleAdminUser, "")
	customer := createChannelKeyRouteUser(t, "email-customer", common.RoleCommonUser, "")
	adminSession := signInForChannelKeyRoute(t, admin)
	customerSession := signInForChannelKeyRoute(t, customer)

	routes := []struct{ method, path, body string }{
		{http.MethodGet, "/api/admin-email/templates", ""},
		{http.MethodPost, "/api/admin-email/templates", `{"name":"x","subject":"s","body":"b"}`},
		{http.MethodPut, "/api/admin-email/templates/1", `{"name":"x","subject":"s","body":"b"}`},
		{http.MethodDelete, "/api/admin-email/templates/1", ""},
		{http.MethodPost, "/api/admin-email/preview", `{"subject":"s","body":"b","user_ids":[1]}`},
		{http.MethodPost, "/api/admin-email/send", `{"subject":"s","body":"b","user_ids":[1]}`},
		{http.MethodGet, "/api/admin-email/sends", ""},
		{http.MethodPut, "/api/admin-email/settings", `{"reply_to":"x@example.com"}`},
	}
	call := func(method, path, body, credential string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if credential != "" {
			request.Header.Set("Authorization", "Bearer "+credential)
		}
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		return recorder
	}
	for _, r := range routes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			assert.Equal(t, http.StatusUnauthorized, call(r.method, r.path, r.body, "").Code, "nobody signed in")
			refused := call(r.method, r.path, r.body, customerSession)
			assert.Equal(t, http.StatusForbidden, refused.Code, "a customer: %s", refused.Body.String())
		})
	}

	templates, err := model.ListAdminEmailTemplates()
	require.NoError(t, err)
	assert.Len(t, templates, 2, "no refused call created or deleted a template")
	sends, err := model.ListAdminEmailSends(0, "", 100)
	require.NoError(t, err)
	assert.Empty(t, sends, "and none recorded a send")

	ok := call(http.MethodGet, "/api/admin-email/templates", "", adminSession)
	assert.Equal(t, http.StatusOK, ok.Code)
	assert.Contains(t, ok.Body.String(), `"success":true`)
	assert.Contains(t, ok.Body.String(), "Premium model access (Claude Mythos)")

	// The Reply-To option is an option write: root only.
	setting := call(http.MethodPut, "/api/admin-email/settings", `{"reply_to":"x@example.com"}`, adminSession)
	assert.Equal(t, http.StatusForbidden, setting.Code, setting.Body.String())
}
