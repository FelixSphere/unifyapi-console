/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api (Copyright (C) 2023-2026
QuantumNous), distributed under the GNU Affero General Public License v3.
See BRANDING.md for the relationship between this fork and its upstream.

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
package controller

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupUnsubscribeTest(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	prev := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = prev })
	require.NoError(t, model.MigrateRetentionEmail())

	prevSecret := common.CryptoSecret
	common.CryptoSecret = "unsubscribe-test-secret"
	t.Cleanup(func() { common.CryptoSecret = prevSecret })

	r := gin.New()
	r.GET("/api/email/unsubscribe", RetentionUnsubscribePage)
	r.POST("/api/email/unsubscribe", RetentionUnsubscribe)
	return r
}

func unsubscribeRequest(r *gin.Engine, method string, userId int, token string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, "/api/email/unsubscribe?u="+strconv.Itoa(userId)+"&t="+token, nil)
	r.ServeHTTP(w, req)
	return w
}

// A mail scanner GETs every link; that must not opt anyone out.
func TestOpeningTheUnsubscribeLinkOnlyAsksForConfirmation(t *testing.T) {
	r := setupUnsubscribeTest(t)
	w := unsubscribeRequest(r, http.MethodGet, 5, service.RetentionUnsubscribeToken(5))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `<form method="post">`)
	require.False(t, model.IsRetentionEmailUnsubscribed(5))
}

func TestPostingTheSignedLinkUnsubscribesAndIsIdempotent(t *testing.T) {
	r := setupUnsubscribeTest(t)
	tok := service.RetentionUnsubscribeToken(5)
	for i := 0; i < 2; i++ { // the second click is not an error
		w := unsubscribeRequest(r, http.MethodPost, 5, tok)
		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Body.String(), "You are unsubscribed")
	}
	require.True(t, model.IsRetentionEmailUnsubscribed(5))

	w := unsubscribeRequest(r, http.MethodGet, 5, tok)
	require.NotContains(t, w.Body.String(), "<form", "an unsubscribed user is told so, not asked again")
}

func TestATamperedLinkUnsubscribesNobody(t *testing.T) {
	r := setupUnsubscribeTest(t)
	w := unsubscribeRequest(r, http.MethodPost, 6, service.RetentionUnsubscribeToken(5))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.False(t, model.IsRetentionEmailUnsubscribed(6))
	require.False(t, model.IsRetentionEmailUnsubscribed(5))
}
