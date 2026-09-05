/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func contributionRequestContext(t *testing.T, method, target string, body []byte, userId int) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, target, bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", userId)
	return ctx, recorder
}

func TestCreateMyCreditContributionRequiresTenantOwner(t *testing.T) {
	db := setupCreditPoolControllerDB(t)
	owner := model.User{Username: "supplier-owner", AffCode: "supplier-owner-aff", Status: 1}
	require.NoError(t, db.Create(&owner).Error)
	tenant := model.Tenant{Name: "Supplier", Slug: "supplier", OwnerId: owner.Id, Status: model.TenantStatusEnabled}
	require.NoError(t, db.Create(&tenant).Error)
	require.NoError(t, db.Model(&owner).Update("tenant_id", tenant.Id).Error)
	member := model.User{Username: "supplier-member", AffCode: "supplier-member-aff", TenantId: tenant.Id, Status: 1}
	require.NoError(t, db.Create(&member).Error)

	body := []byte(`{"provider":"anthropic","account_label":"Startup program","models":"claude-*","requested_quota":1000,"requested_acquisition_ratio":0.25,"attested":true}`)
	ctx, recorder := contributionRequestContext(t, http.MethodPost, "/api/credit-contribution/self/", body, owner.Id)
	CreateMyCreditContribution(ctx)
	var response struct {
		Success bool                     `json:"success"`
		Data    model.CreditContribution `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)
	assert.Equal(t, tenant.Id, response.Data.TenantId)
	assert.Equal(t, model.CreditContributionSubmitted, response.Data.Status)

	ctx, recorder = contributionRequestContext(t, http.MethodPost, "/api/credit-contribution/self/", body, member.Id)
	CreateMyCreditContribution(ctx)
	var denied struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &denied))
	assert.False(t, denied.Success)
	assert.Contains(t, denied.Message, "tenant owner")
}

func TestGetMyCreditContributionsDoesNotExposeAdminNotes(t *testing.T) {
	db := setupCreditPoolControllerDB(t)
	owner := model.User{Username: "private-notes-owner", AffCode: "private-notes-aff", Status: 1}
	require.NoError(t, db.Create(&owner).Error)
	tenant := model.Tenant{Name: "Private notes", Slug: "private-notes", OwnerId: owner.Id, Status: model.TenantStatusEnabled}
	require.NoError(t, db.Create(&tenant).Error)
	require.NoError(t, db.Model(&owner).Update("tenant_id", tenant.Id).Error)
	contribution := model.CreditContribution{
		TenantId: tenant.Id, SubmittedBy: owner.Id, Provider: "openai", RequestedQuota: 100,
		Status: model.CreditContributionSubmitted, AttestationVersion: model.CreditContributionAttestationVersion,
		AttestedAt: 1, AdminNotes: "internal risk note",
	}
	require.NoError(t, db.Create(&contribution).Error)

	ctx, recorder := contributionRequestContext(t, http.MethodGet, "/api/credit-contribution/self/", nil, owner.Id)
	GetMyCreditContributions(ctx)
	assert.NotContains(t, recorder.Body.String(), "internal risk note")
	assert.Contains(t, recorder.Body.String(), "openai")
}
