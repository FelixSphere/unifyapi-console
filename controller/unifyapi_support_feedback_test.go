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
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
)

func TestSupportFeedbackPagesCustomerMessagesWithoutMarkingInboxRead(t *testing.T) {
	h := setupSupportTest(t)
	h.r = unreadRouter(h)
	h.r.GET("/api/support/admin/feedback", SupportFeedback)
	ticket, err := model.CreateSupportTicket(h.customer.Id, "Issue", "First feedback")
	require.NoError(t, err)
	_, err = model.AddSupportAIMessage(ticket.Id, "AI reply", false, 1)
	require.NoError(t, err)
	_, err = model.AddSupportMessage(ticket.Id, h.staff.Id, true, "Human reply")
	require.NoError(t, err)
	_, err = model.AddSupportMessage(ticket.Id, h.customer.Id, false, "Follow-up feedback")
	require.NoError(t, err)
	before := unreadCount(t, h.r, h.staff)
	code, out := h.do(t, h.staff, http.MethodGet, "/api/support/admin/feedback?after=0&limit=1", nil)
	require.Equal(t, http.StatusOK, code)
	page := out["data"].(map[string]any)
	messages := page["messages"].([]any)
	require.Len(t, messages, 1)
	first := messages[0].(map[string]any)
	assert.Equal(t, "First feedback", first["body"])
	assert.NotContains(t, first, "email")
	assert.NotContains(t, first, "user_id")
	assert.Equal(t, float64(1), page["next_cursor"])
	code, out = h.do(t, h.staff, http.MethodGet, "/api/support/admin/feedback?after=1", nil)
	require.Equal(t, http.StatusOK, code)
	messages = out["data"].(map[string]any)["messages"].([]any)
	require.Len(t, messages, 1)
	assert.Equal(t, "Follow-up feedback", messages[0].(map[string]any)["body"])
	assert.Equal(t, before, unreadCount(t, h.r, h.staff))
	code, _ = h.do(t, h.customer, http.MethodGet, "/api/support/admin/feedback", nil)
	assert.Equal(t, http.StatusForbidden, code)
	code, _ = h.do(t, h.staff, http.MethodGet, "/api/support/admin/feedback?after=-1", nil)
	assert.Equal(t, http.StatusBadRequest, code)
	code, _ = h.do(t, h.staff, http.MethodGet, "/api/support/admin/feedback?limit=101", nil)
	assert.Equal(t, http.StatusBadRequest, code)
}
