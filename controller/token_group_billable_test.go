/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
 * A token's own group decides what the request is PRICED at -- auth stores it
 * as ContextKeyUsingGroup through effectiveUsingGroup, which returns it
 * verbatim. Nothing validated it: AddToken and UpdateToken copied it out of the
 * request body and Token.Insert/Update wrote it unchecked.
 *
 * So a login could POST a token naming any group and be billed at that group's
 * rate. On production on 2026-09-22 `default` was 0.9 while every customer
 * group was 1.0, so a customer could pay 10% less than they had agreed by
 * editing one field; naming another customer's group took their contract.
 *
 * These go through the HTTP handlers, because that is the layer the gap was in.
 */
package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTokenGroupBillableTest(t *testing.T) {
	t.Helper()
	originalUsable := setting.UserUsableGroups2JSONString()
	originalRatios := ratio_setting.GroupRatio2JSONString()
	originalSpecial := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.ReadAll()

	// `default` stays in the allowlist so the public catalogue renders, and it
	// is the cheap one -- production's exact shape.
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":0.9,"Kingdee":1,"Chinhin":1}`))
	ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.Clear()

	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsable))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalRatios))
		special := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup
		special.Clear()
		special.AddAll(originalSpecial)
	})
}

// kingdeeContext is a login that belongs to the Kingdee contract.
func kingdeeContext(t *testing.T, method, target string, body any) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	ctx, recorder := newAuthenticatedContext(t, method, target, body, 1)
	ctx.Set("group", "Kingdee")
	return ctx, recorder
}

func TestATokenCannotBeCreatedUnderTheCheaperDefaultGroup(t *testing.T) {
	setupTokenControllerTestDB(t)
	setupTokenGroupBillableTest(t)

	ctx, recorder := kingdeeContext(t, http.MethodPost, "/api/token/", map[string]any{
		"name": "cheap", "expired_time": -1, "remain_quota": 0,
		"unlimited_quota": true, "group": "default",
	})
	AddToken(ctx)

	response := decodeAPIResponse(t, recorder)
	assert.False(t, response.Success,
		"a Kingdee login must not hold a token priced at the default discount")
}

func TestATokenCannotBeCreatedUnderAnotherCustomersGroup(t *testing.T) {
	setupTokenControllerTestDB(t)
	setupTokenGroupBillableTest(t)

	ctx, recorder := kingdeeContext(t, http.MethodPost, "/api/token/", map[string]any{
		"name": "theirs", "expired_time": -1, "remain_quota": 0,
		"unlimited_quota": true, "group": "Chinhin",
	})
	AddToken(ctx)

	assert.False(t, decodeAPIResponse(t, recorder).Success,
		"one customer must not bill under another customer's contract")
}

func TestATokenInTheOwnersOwnGroupIsStillAccepted(t *testing.T) {
	setupTokenControllerTestDB(t)
	setupTokenGroupBillableTest(t)

	ctx, recorder := kingdeeContext(t, http.MethodPost, "/api/token/", map[string]any{
		"name": "mine", "expired_time": -1, "remain_quota": 0,
		"unlimited_quota": true, "group": "Kingdee",
	})
	AddToken(ctx)

	response := decodeAPIResponse(t, recorder)
	assert.True(t, response.Success,
		"the guard must not lock a customer out of their own group: %s", response.Message)
}

func TestAnEmptyTokenGroupIsStillAccepted(t *testing.T) {
	setupTokenControllerTestDB(t)
	setupTokenGroupBillableTest(t)

	// Empty means "use my own", which effectiveUsingGroup resolves; refusing it
	// would break every token created without naming a group.
	ctx, recorder := kingdeeContext(t, http.MethodPost, "/api/token/", map[string]any{
		"name": "unset", "expired_time": -1, "remain_quota": 0,
		"unlimited_quota": true, "group": "",
	})
	AddToken(ctx)

	response := decodeAPIResponse(t, recorder)
	assert.True(t, response.Success, response.Message)
}
