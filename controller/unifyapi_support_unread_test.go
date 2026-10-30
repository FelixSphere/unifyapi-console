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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// The sidebar badge (operator request 2026-10-15: "管理员账户如果有没有读的消息，
// 应该在导航栏有提示"). Unread is per person: opening a conversation clears it
// for whoever opened it, and a new message from the other side brings it back.

// unreadRouter is the support harness plus the unread route, with the caller's
// role in the context the way UserAuth leaves it.
func unreadRouter(h *supportHarness) *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		id, _ := strconv.Atoi(c.GetHeader("X-Test-User"))
		c.Set("id", id)
		var u model.User
		model.DB.First(&u, id)
		c.Set("role", u.Role)
	})
	r.GET("/api/support/unread", SupportUnread)
	r.GET("/api/support/tickets/:id", GetMySupportTicket)
	r.GET("/api/support/admin/tickets/:id", GetSupportTicket)
	return r
}

func unreadCount(t *testing.T, r *gin.Engine, as *model.User) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/support/unread", nil)
	req.Header.Set("X-Test-User", strconv.Itoa(as.Id))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var out struct {
		Data struct {
			Count int `json:"count"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	return out.Data.Count
}

func openAs(t *testing.T, r *gin.Engine, as *model.User, path string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-Test-User", strconv.Itoa(as.Id))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

func TestStaffSeeUnreadCustomerMessagesUntilTheyOpenThem(t *testing.T) {
	h := setupSupportTest(t)
	r := unreadRouter(h)
	require.Equal(t, 0, unreadCount(t, r, h.staff))

	a := h.open(t, "A", "first")
	h.open(t, "B", "second")
	require.Equal(t, 2, unreadCount(t, r, h.staff))
	require.Equal(t, 0, unreadCount(t, r, h.customer), "a customer's own messages are not unread for them")

	openAs(t, r, h.staff, "/api/support/admin/tickets/"+strconv.Itoa(a))
	require.Equal(t, 1, unreadCount(t, r, h.staff))

	// A new customer message brings the opened conversation back.
	h.do(t, h.customer, http.MethodPost, "/api/support/tickets/"+strconv.Itoa(a)+"/messages", map[string]string{"body": "more"})
	require.Equal(t, 2, unreadCount(t, r, h.staff))
}

func TestOneOperatorOpeningAConversationDoesNotClearItForAnother(t *testing.T) {
	h := setupSupportTest(t)
	r := unreadRouter(h)
	second := &model.User{Username: "ops2", DisplayName: "ops2", Email: "ops2@example.com", Role: h.staff.Role,
		Status: h.staff.Status, AffCode: "aff-ops2", Password: "x"}
	require.NoError(t, model.DB.Create(second).Error)

	id := h.open(t, "Q", "body")
	openAs(t, r, h.staff, "/api/support/admin/tickets/"+strconv.Itoa(id))
	require.Equal(t, 0, unreadCount(t, r, h.staff))
	require.Equal(t, 1, unreadCount(t, r, second))
}

func TestCustomersSeeUnreadRepliesToTheirOwnConversationsOnly(t *testing.T) {
	h := setupSupportTest(t)
	r := unreadRouter(h)
	id := h.open(t, "Q", "body")
	path := "/api/support/admin/tickets/" + strconv.Itoa(id)
	h.do(t, h.staff, http.MethodPost, path+"/messages", map[string]string{"body": "answer"})

	require.Equal(t, 0, unreadCount(t, r, h.staff), "replying counts as having read it")
	require.Equal(t, 1, unreadCount(t, r, h.customer))
	require.Equal(t, 0, unreadCount(t, r, h.other), "another customer never sees it")

	openAs(t, r, h.customer, "/api/support/tickets/"+strconv.Itoa(id))
	require.Equal(t, 0, unreadCount(t, r, h.customer))
}
