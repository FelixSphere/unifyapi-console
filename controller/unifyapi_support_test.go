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
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type supportHarness struct {
	r        *gin.Engine
	customer *model.User
	other    *model.User
	staff    *model.User
	mu       sync.Mutex
	mailed   []string // "purpose->receiver"
}

// The real routes sit behind UserAuth/AdminAuth; here a header names the
// caller, which is what those middlewares leave in the context.
func setupSupportTest(t *testing.T) *supportHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	prev := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = prev })
	require.NoError(t, db.AutoMigrate(&model.User{}))
	require.NoError(t, model.MigrateSupport())

	h := &supportHarness{}
	mk := func(name string, role int) *model.User {
		u := &model.User{Username: name, DisplayName: name, Email: name + "@example.com", Role: role,
			Status: common.UserStatusEnabled, AffCode: "aff-" + name, Password: "x"}
		require.NoError(t, db.Create(u).Error)
		return u
	}
	h.customer = mk("cust", common.RoleCommonUser)
	h.other = mk("other", common.RoleCommonUser)
	h.staff = mk("ops", common.RoleAdminUser)

	// Emails go out asynchronously; the recorder sees every attempt, sent or
	// refused, so the test needs no mail server.
	prevSMTP := common.SMTPServer
	common.SMTPServer = "127.0.0.1"
	prevPort := common.SMTPPort
	common.SMTPPort = 1 // nothing listens; the attempt is still recorded
	common.SetEmailDeliveryRecorder(func(receiver, purpose string, _ error) {
		h.mu.Lock()
		h.mailed = append(h.mailed, purpose+"->"+receiver)
		h.mu.Unlock()
	})
	t.Cleanup(func() {
		common.SetEmailDeliveryRecorder(nil)
		common.SMTPServer, common.SMTPPort = prevSMTP, prevPort
	})

	setCaller := func(c *gin.Context) {
		id, _ := strconv.Atoi(c.GetHeader("X-Test-User"))
		c.Set("id", id)
	}
	h.r = gin.New()
	h.r.Use(setCaller)
	h.r.GET("/api/support/tickets", ListMySupportTickets)
	h.r.POST("/api/support/tickets", CreateSupportTicket)
	h.r.GET("/api/support/tickets/:id", GetMySupportTicket)
	h.r.POST("/api/support/tickets/:id/messages", AddMySupportMessage)
	h.r.POST("/api/support/tickets/:id/close", CloseMySupportTicket)
	h.r.GET("/api/support/admin/tickets", ListSupportTickets)
	h.r.GET("/api/support/admin/tickets/:id", GetSupportTicket)
	h.r.POST("/api/support/admin/tickets/:id/messages", ReplySupportTicket)
	h.r.POST("/api/support/admin/tickets/:id/status", SetSupportTicketStatus)
	return h
}

func (h *supportHarness) do(t *testing.T, as *model.User, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-User", strconv.Itoa(as.Id))
	w := httptest.NewRecorder()
	h.r.ServeHTTP(w, req)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out), w.Body.String())
	return w.Code, out
}

func (h *supportHarness) open(t *testing.T, subject, body string) int {
	t.Helper()
	code, out := h.do(t, h.customer, http.MethodPost, "/api/support/tickets", map[string]string{"subject": subject, "body": body})
	require.Equal(t, http.StatusOK, code, out)
	return int(out["data"].(map[string]any)["id"].(float64))
}

func ticketStatus(out map[string]any) string {
	return out["data"].(map[string]any)["ticket"].(map[string]any)["status"].(string)
}

func (h *supportHarness) mails() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.mailed...)
}

func TestACustomerAsksAndSeesOnlyTheirOwnConversations(t *testing.T) {
	h := setupSupportTest(t)
	id := h.open(t, "How do I set a budget?", "I want to cap spend per key.")
	path := "/api/support/tickets/" + strconv.Itoa(id)

	code, out := h.do(t, h.customer, http.MethodGet, path, nil)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, model.SupportStatusOpen, ticketStatus(out))
	msgs := out["data"].(map[string]any)["messages"].([]any)
	require.Len(t, msgs, 1)
	require.Equal(t, "I want to cap spend per key.", msgs[0].(map[string]any)["body"])

	// Someone else's id is "not found", for reads and writes alike.
	code, _ = h.do(t, h.other, http.MethodGet, path, nil)
	require.Equal(t, http.StatusNotFound, code)
	code, _ = h.do(t, h.other, http.MethodPost, path+"/messages", map[string]string{"body": "hi"})
	require.Equal(t, http.StatusNotFound, code)
	code, _ = h.do(t, h.other, http.MethodPost, path+"/close", nil)
	require.Equal(t, http.StatusNotFound, code)

	_, out = h.do(t, h.other, http.MethodGet, "/api/support/tickets", nil)
	require.Empty(t, out["data"])
	_, out = h.do(t, h.customer, http.MethodGet, "/api/support/tickets", nil)
	require.Len(t, out["data"], 1)
}

func TestStatusSaysWhoseTurnItIs(t *testing.T) {
	h := setupSupportTest(t)
	id := h.open(t, "Invoice", "Can I get a VAT invoice?")
	mine := "/api/support/tickets/" + strconv.Itoa(id)
	inbox := "/api/support/admin/tickets/" + strconv.Itoa(id)

	_, out := h.do(t, h.staff, http.MethodPost, inbox+"/messages", map[string]string{"body": "Yes, from the wallet page."})
	require.Equal(t, model.SupportStatusAnswered, ticketStatus(out))

	_, out = h.do(t, h.customer, http.MethodPost, mine+"/messages", map[string]string{"body": "Thanks, found it."})
	require.Equal(t, model.SupportStatusOpen, ticketStatus(out))

	_, out = h.do(t, h.customer, http.MethodPost, mine+"/close", nil)
	require.Equal(t, model.SupportStatusClosed, ticketStatus(out))

	_, out = h.do(t, h.customer, http.MethodPost, mine+"/messages", map[string]string{"body": "One more thing."})
	require.Equal(t, model.SupportStatusOpen, ticketStatus(out), "writing to a closed conversation reopens it")

	_, out = h.do(t, h.customer, http.MethodGet, mine, nil)
	msgs := out["data"].(map[string]any)["messages"].([]any)
	require.Len(t, msgs, 4)
	require.Equal(t, true, msgs[1].(map[string]any)["from_staff"])
	require.Equal(t, false, msgs[2].(map[string]any)["from_staff"])
}

func TestTheInboxFiltersByStatusAndNamesTheCustomer(t *testing.T) {
	h := setupSupportTest(t)
	a := h.open(t, "A", "first")
	h.open(t, "B", "second")
	h.do(t, h.staff, http.MethodPost, "/api/support/admin/tickets/"+strconv.Itoa(a)+"/messages", map[string]string{"body": "done"})

	_, out := h.do(t, h.staff, http.MethodGet, "/api/support/admin/tickets?status=open", nil)
	rows := out["data"].([]any)
	require.Len(t, rows, 1)
	require.Equal(t, "B", rows[0].(map[string]any)["subject"])
	require.Equal(t, "cust", rows[0].(map[string]any)["username"])
	require.Equal(t, "cust@example.com", rows[0].(map[string]any)["email"])

	_, out = h.do(t, h.staff, http.MethodGet, "/api/support/admin/tickets", nil)
	require.Len(t, out["data"], 2)

	code, _ := h.do(t, h.staff, http.MethodGet, "/api/support/admin/tickets?status=bogus", nil)
	require.Equal(t, http.StatusBadRequest, code)
	code, _ = h.do(t, h.staff, http.MethodPost, "/api/support/admin/tickets/"+strconv.Itoa(a)+"/status", map[string]string{"status": "bogus"})
	require.Equal(t, http.StatusBadRequest, code)
	code, out = h.do(t, h.staff, http.MethodPost, "/api/support/admin/tickets/"+strconv.Itoa(a)+"/status", map[string]string{"status": "closed"})
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, model.SupportStatusClosed, ticketStatus(out))
	code, _ = h.do(t, h.staff, http.MethodGet, "/api/support/admin/tickets/99999", nil)
	require.Equal(t, http.StatusNotFound, code)
}

func TestEmptyAndOversizedTextIsRefused(t *testing.T) {
	h := setupSupportTest(t)
	cases := []map[string]string{
		{"subject": "  ", "body": "x"},
		{"subject": "x", "body": ""},
		{"subject": strings.Repeat("s", supportSubjectMax+1), "body": "x"},
		{"subject": "x", "body": strings.Repeat("b", supportBodyMax+1)},
	}
	for _, c := range cases {
		code, _ := h.do(t, h.customer, http.MethodPost, "/api/support/tickets", c)
		require.Equal(t, http.StatusBadRequest, code)
	}
	// The limit counts characters, not bytes: 5000 CJK characters fit.
	code, _ := h.do(t, h.customer, http.MethodPost, "/api/support/tickets",
		map[string]string{"subject": "中文", "body": strings.Repeat("字", supportBodyMax)})
	require.Equal(t, http.StatusOK, code)
}

func TestOneAccountCannotFloodTheInbox(t *testing.T) {
	h := setupSupportTest(t)
	for i := 0; i < supportMaxUnclosed; i++ {
		h.open(t, "q"+strconv.Itoa(i), "body")
	}
	code, _ := h.do(t, h.customer, http.MethodPost, "/api/support/tickets", map[string]string{"subject": "one more", "body": "x"})
	require.Equal(t, http.StatusTooManyRequests, code, "too many unclosed conversations")

	path := "/api/support/tickets/1/messages"
	for i := supportMaxUnclosed; i < supportMaxMessagesPerHr; i++ {
		code, _ = h.do(t, h.customer, http.MethodPost, path, map[string]string{"body": "again"})
		require.Equal(t, http.StatusOK, code)
	}
	code, _ = h.do(t, h.customer, http.MethodPost, path, map[string]string{"body": "again"})
	require.Equal(t, http.StatusTooManyRequests, code, "too many messages in an hour")

	// The other customer is unaffected.
	code, _ = h.do(t, h.other, http.MethodPost, "/api/support/tickets", map[string]string{"subject": "mine", "body": "x"})
	require.Equal(t, http.StatusOK, code)
}

func TestStaffHearAboutCustomerMessagesAndCustomersHearAboutReplies(t *testing.T) {
	h := setupSupportTest(t)
	id := h.open(t, "Hello", "first")
	require.Eventually(t, func() bool {
		return len(h.mails()) == 1
	}, 3*time.Second, 20*time.Millisecond)
	require.Equal(t, []string{"support-staff->ops@example.com"}, h.mails(), "only staff, never other customers")

	h.do(t, h.staff, http.MethodPost, "/api/support/admin/tickets/"+strconv.Itoa(id)+"/messages", map[string]string{"body": "hi"})
	require.Eventually(t, func() bool {
		return len(h.mails()) == 2
	}, 3*time.Second, 20*time.Millisecond)
	require.Equal(t, "support-reply->cust@example.com", h.mails()[1])
}

func TestASubjectIsOneLine(t *testing.T) {
	h := setupSupportTest(t)
	id := h.open(t, "Invoice\r\nBcc: someone@example.com\n\tplease", "body")
	_, out := h.do(t, h.customer, http.MethodGet, "/api/support/tickets/"+strconv.Itoa(id), nil)
	subject := out["data"].(map[string]any)["ticket"].(map[string]any)["subject"].(string)
	require.Equal(t, "Invoice Bcc: someone@example.com please", subject)
}
