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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

// The support chat widget: "Talk to a human" (POST .../handoff) and the
// events the console reports to the AI agent so its CRM Case stays in step.

type agentEvents struct {
	mu     sync.Mutex
	events []map[string]any
	claims []jwt.MapClaims
	asked  map[string]any // the last body of the AI call itself
}

func (a *agentEvents) list() []map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]map[string]any(nil), a.events...)
}

func (a *agentEvents) types() []string {
	var out []string
	for _, e := range a.list() {
		out = append(out, e["type"].(string))
	}
	return out
}

// startEventAgent is a fake UnifySelfAI that answers the AI call and records
// every event, with the claims of the token it came with.
func startEventAgent(t *testing.T) *agentEvents {
	t.Helper()
	a := &agentEvents{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, err := jwt.Parse(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "),
			func(*jwt.Token) (any, error) { return []byte(aiTestSecret), nil },
			jwt.WithAudience("unifyselfai:support-agent"), jwt.WithIssuer("unifyapi-console"),
			jwt.WithExpirationRequired(), jwt.WithValidMethods([]string{"HS256"}))
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/api/support-agent/events":
			a.mu.Lock()
			a.events = append(a.events, body)
			a.claims = append(a.claims, tok.Claims.(jwt.MapClaims))
			a.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case "/api/support-agent":
			a.mu.Lock()
			a.asked = body
			a.mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: answer\ndata: {\"text\":\"ok\"}\n\nevent: done\ndata: {}\n\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("SUPPORT_AGENT_URL", srv.URL)
	t.Setenv("SUPPORT_AGENT_SECRET", aiTestSecret)
	return a
}

func withWidgetRoutes(h *supportHarness) {
	h.r.POST("/api/support/tickets/:id/handoff", HandOffMySupportTicket)
	h.r.POST("/api/support/tickets/:id/ai", AskSupportAI)
}

func (h *supportHarness) handoff(t *testing.T, as *model.User, id int) (int, map[string]any) {
	return h.do(t, as, http.MethodPost, "/api/support/tickets/"+strconv.Itoa(id)+"/handoff", nil)
}

func lastMessage(out map[string]any) map[string]any {
	msgs := out["data"].(map[string]any)["messages"].([]any)
	return msgs[len(msgs)-1].(map[string]any)
}

func TestHandoffIsOwnerOnly(t *testing.T) {
	h := setupSupportTest(t)
	withWidgetRoutes(h)
	id := h.open(t, "Q", "help")
	code, out := h.handoff(t, h.other, id)
	require.Equal(t, http.StatusNotFound, code, out)
	require.Equal(t, "conversation not found", out["message"])
	code, _ = h.handoff(t, h.customer, 999999)
	require.Equal(t, http.StatusNotFound, code)
	_, conv := h.do(t, h.customer, http.MethodGet, "/api/support/tickets/"+strconv.Itoa(id), nil)
	require.Len(t, conv["data"].(map[string]any)["messages"].([]any), 1, "a refused handoff writes nothing")
}

func TestHandoffNeedsAPersonAndEmailsStaffOnce(t *testing.T) {
	h := setupSupportTest(t)
	withWidgetRoutes(h)
	id := h.open(t, "Q", "my key stopped working")
	require.Eventually(t, func() bool { return len(h.mails()) == 1 }, 3*time.Second, 20*time.Millisecond) // agent off: the question itself
	h.do(t, h.staff, http.MethodPost, "/api/support/admin/tickets/"+strconv.Itoa(id)+"/messages", map[string]string{"body": "try again"})
	require.Eventually(t, func() bool { return len(h.mails()) == 2 }, 3*time.Second, 20*time.Millisecond) // reply to customer

	code, out := h.handoff(t, h.customer, id)
	require.Equal(t, http.StatusOK, code, out)
	require.Equal(t, model.SupportStatusOpen, ticketStatus(out), "a handoff puts it under Needs reply")
	last := lastMessage(out)
	require.Equal(t, SupportHandoffBody, last["body"])
	require.Equal(t, false, last["from_staff"])
	require.Eventually(t, func() bool { return len(h.mails()) == 3 }, 3*time.Second, 20*time.Millisecond)
	require.Equal(t, "support-staff->ops@example.com", h.mails()[2])

	// Asking again is the same request: no second message, no second email.
	code, out = h.handoff(t, h.customer, id)
	require.Equal(t, http.StatusOK, code)
	require.Len(t, out["data"].(map[string]any)["messages"].([]any), 3)
	time.Sleep(150 * time.Millisecond)
	require.Len(t, h.mails(), 3)

	// Staff were told, so the safety sweep has nothing left.
	model.DB.Exec("UPDATE support_messages SET created_at = created_at - 600")
	rows, err := model.UnhandledSupportMessages(time.Now().Add(-service.SupportAgentSweepAfter).Unix(), 10)
	require.NoError(t, err)
	require.Empty(t, rows)
}

func TestHandoffIsNotAnsweredByTheAI(t *testing.T) {
	h := setupSupportTest(t)
	startEventAgent(t)
	withWidgetRoutes(h)
	id := h.open(t, "Q", "help")
	code, _ := h.handoff(t, h.customer, id)
	require.Equal(t, http.StatusOK, code)
	code, _ = h.askAI(t, id)
	require.Equal(t, http.StatusConflict, code, "the handoff is already handled; the agent must not answer it")
	// With the agent on, the handoff is the one email staff get.
	require.Eventually(t, func() bool { return len(h.mails()) == 1 }, 3*time.Second, 20*time.Millisecond)
}

func TestHandoffOnAClosedConversationReopensIt(t *testing.T) {
	h := setupSupportTest(t)
	agent := startEventAgent(t)
	withWidgetRoutes(h)
	id := h.open(t, "Q", "help")
	h.do(t, h.customer, http.MethodPost, "/api/support/tickets/"+strconv.Itoa(id)+"/close", nil)
	_, out := h.handoff(t, h.customer, id)
	require.Equal(t, model.SupportStatusOpen, ticketStatus(out))
	require.Eventually(t, func() bool { return len(agent.list()) == 3 }, 3*time.Second, 20*time.Millisecond)
	require.Equal(t, []string{"closed", "reopened", "handoff"}, agent.types())
}

func TestTheAgentHearsWhatHappensWithoutIt(t *testing.T) {
	h := setupSupportTest(t)
	agent := startEventAgent(t)
	withWidgetRoutes(h)
	id := h.open(t, "Q", "help")
	sid := strconv.Itoa(id)
	wait := func(n int) {
		t.Helper()
		require.Eventually(t, func() bool { return len(agent.list()) == n }, 3*time.Second, 20*time.Millisecond)
	}

	h.do(t, h.staff, http.MethodPost, "/api/support/admin/tickets/"+sid+"/messages", map[string]string{"body": "We raised your limit."})
	wait(1)
	h.do(t, h.customer, http.MethodPost, "/api/support/tickets/"+sid+"/close", nil)
	wait(2)
	h.do(t, h.customer, http.MethodPost, "/api/support/tickets/"+sid+"/close", nil) // already closed: no event
	h.do(t, h.customer, http.MethodPost, "/api/support/tickets/"+sid+"/messages", map[string]string{"body": "one more thing"})
	wait(3)
	h.do(t, h.staff, http.MethodPost, "/api/support/admin/tickets/"+sid+"/status", map[string]string{"status": "closed"})
	wait(4)
	h.do(t, h.staff, http.MethodPost, "/api/support/admin/tickets/"+sid+"/status", map[string]string{"status": "open"})
	wait(5)
	h.do(t, h.staff, http.MethodPost, "/api/support/admin/tickets/"+sid+"/status", map[string]string{"status": "answered"}) // not a close or reopen
	h.do(t, h.staff, http.MethodPost, "/api/support/admin/tickets/"+sid+"/status", map[string]string{"status": "closed"})
	wait(6)
	h.do(t, h.staff, http.MethodPost, "/api/support/admin/tickets/"+sid+"/messages", map[string]string{"body": "Reopening to add a note."})
	wait(8)
	time.Sleep(100 * time.Millisecond)

	require.Equal(t, []string{"staff_reply", "closed", "reopened", "closed", "reopened", "closed", "reopened", "staff_reply"}, agent.types())
	evs := agent.list()
	require.Equal(t, "We raised your limit.", evs[0]["body"])
	require.Equal(t, "Reopening to add a note.", evs[7]["body"])
	now := time.Now().Unix()
	jtis := map[string]bool{}
	for i, e := range evs {
		require.Equal(t, float64(id), e["ticket_id"])
		require.InDelta(t, float64(now), e["at"], 10)
		if e["type"] != "staff_reply" {
			_, has := e["body"]
			require.False(t, has, "only a staff reply carries a body")
		}
		cl := agent.claims[i]
		require.Equal(t, strconv.Itoa(h.customer.Id), cl["sub"], "the token names the customer")
		require.Equal(t, sid, cl["cid"], "and the conversation")
		require.LessOrEqual(t, cl["exp"].(float64)-cl["iat"].(float64), 120.0)
		jti := cl["jti"].(string)
		require.False(t, jtis[jti], "every event has its own jti")
		jtis[jti] = true
	}
}

func TestTheAICallNamesTheTicketAndTheCustomer(t *testing.T) {
	h := setupSupportTest(t)
	agent := startEventAgent(t)
	withWidgetRoutes(h)
	id := h.open(t, "Q", "help")
	code, _ := h.askAI(t, id)
	require.Equal(t, http.StatusOK, code)
	agent.mu.Lock()
	body := agent.asked
	agent.mu.Unlock()
	require.Equal(t, float64(id), body["ticket_id"])
	require.Equal(t, float64(h.customer.Id), body["customer_id"])
	require.Equal(t, "cust", body["username"])
	require.Equal(t, h.customer.Email, body["email"])
	require.NotEmpty(t, body["messages"], "the original field is still sent")
}

func TestWithTheAgentOffNoEventIsSent(t *testing.T) {
	h := setupSupportTest(t)
	hits := 0
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	// A URL alone is not enough: the agent is on only with a secret too.
	t.Setenv("SUPPORT_AGENT_URL", srv.URL)
	t.Setenv("SUPPORT_AGENT_SECRET", "")
	withWidgetRoutes(h)
	id := h.open(t, "Q", "help")
	h.do(t, h.staff, http.MethodPost, "/api/support/admin/tickets/"+strconv.Itoa(id)+"/messages", map[string]string{"body": "hi"})
	h.handoff(t, h.customer, id)
	h.do(t, h.customer, http.MethodPost, "/api/support/tickets/"+strconv.Itoa(id)+"/close", nil)
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	require.Zero(t, hits)
}
