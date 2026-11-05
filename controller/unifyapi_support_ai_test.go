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
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// The AI first responder on /support (operator request 2026-11-05). The agent
// runs in UnifySelfAI; here a fake stands in for it, so these tests pin the
// console's half: what is stored, who is emailed when, and that the agent's
// callbacks only ever read the customer its token names.

const aiTestSecret = "0123456789abcdef0123456789abcdef-test"

type fakeAgent struct {
	srv      *httptest.Server
	mode     string // answer | handoff | fail
	lastAuth jwt.MapClaims
	lastBody map[string]any
}

func startFakeAgent(t *testing.T, mode string) *fakeAgent {
	t.Helper()
	fa := &fakeAgent{mode: mode}
	fa.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, err := jwt.Parse(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "),
			func(*jwt.Token) (any, error) { return []byte(aiTestSecret), nil },
			jwt.WithAudience("unifyselfai:support-agent"), jwt.WithIssuer("unifyapi-console"))
		require.NoError(t, err)
		fa.lastAuth = tok.Claims.(jwt.MapClaims)
		_ = json.NewDecoder(r.Body).Decode(&fa.lastBody)
		if fa.mode == "fail" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(e, d string) { fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e, d); w.(http.Flusher).Flush() }
		send("status", `{"stage":"thinking"}`)
		send("tool", `{"name":"get_my_account","ok":true}`)
		send("answer", `{"text":"Your balance is fine."}`)
		if fa.mode == "handoff" {
			send("handoff", `{"reason":"refund request"}`)
		}
		send("done", `{"tools":["get_my_account"]}`)
	}))
	t.Cleanup(fa.srv.Close)
	t.Setenv("SUPPORT_AGENT_URL", fa.srv.URL)
	t.Setenv("SUPPORT_AGENT_SECRET", aiTestSecret)
	return fa
}

func (h *supportHarness) askAI(t *testing.T, ticketId int) (int, []string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/support/tickets/"+strconv.Itoa(ticketId)+"/ai", nil)
	req.Header.Set("X-Test-User", strconv.Itoa(h.customer.Id))
	w := httptest.NewRecorder()
	h.r.ServeHTTP(w, req)
	var events []string
	sc := bufio.NewScanner(strings.NewReader(w.Body.String()))
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "event: ") {
			events = append(events, strings.TrimPrefix(sc.Text(), "event: "))
		}
	}
	return w.Code, events
}

func withAIRoutes(h *supportHarness) {
	h.r.POST("/api/support/tickets/:id/ai", AskSupportAI)
	h.r.GET("/api/support/agent/account", SupportAgentAccount)
	h.r.GET("/api/support/agent/request/:rid", SupportAgentRequest)
}

func TestWithTheAgentOnTheCustomerGetsAnAnswerAndStaffAreNotEmailed(t *testing.T) {
	h := setupSupportTest(t)
	fa := startFakeAgent(t, "answer")
	withAIRoutes(h)

	id := h.open(t, "Balance", "what is my balance?")
	time.Sleep(100 * time.Millisecond)
	require.Empty(t, h.mails(), "the agent answers first; staff are not emailed for every message")

	code, events := h.askAI(t, id)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, []string{"status", "tool", "answer", "done", "stored"}, events)
	require.Equal(t, strconv.Itoa(h.customer.Id), fa.lastAuth["sub"], "the agent is told which customer, by token")
	require.Equal(t, strconv.Itoa(id), fa.lastAuth["cid"])
	turns := fa.lastBody["messages"].([]any)
	require.Equal(t, "customer", turns[0].(map[string]any)["role"])

	_, out := h.do(t, h.customer, http.MethodGet, "/api/support/tickets/"+strconv.Itoa(id), nil)
	data := out["data"].(map[string]any)
	require.Equal(t, model.SupportStatusAnswered, data["ticket"].(map[string]any)["status"])
	msgs := data["messages"].([]any)
	ai := msgs[len(msgs)-1].(map[string]any)
	require.Equal(t, "Your balance is fine.", ai["body"])
	require.Equal(t, true, ai["from_ai"])
	require.Equal(t, true, ai["from_staff"])

	// The same message is answered once.
	code, _ = h.askAI(t, id)
	require.Equal(t, http.StatusConflict, code)

	// Nothing is left for the safety sweep, so it emails nobody.
	model.DB.Exec("UPDATE support_messages SET created_at = created_at - 600")
	rows, err := model.UnhandledSupportMessages(time.Now().Add(-service.SupportAgentSweepAfter).Unix(), 10)
	require.NoError(t, err)
	require.Empty(t, rows)
	require.Empty(t, h.mails())
}

func TestAHandoffGoesToNeedsReplyAndEmailsStaff(t *testing.T) {
	h := setupSupportTest(t)
	startFakeAgent(t, "handoff")
	withAIRoutes(h)
	id := h.open(t, "Refund", "I want a refund")
	_, events := h.askAI(t, id)
	require.Contains(t, events, "handoff")
	_, out := h.do(t, h.customer, http.MethodGet, "/api/support/tickets/"+strconv.Itoa(id), nil)
	require.Equal(t, model.SupportStatusOpen, out["data"].(map[string]any)["ticket"].(map[string]any)["status"])
	require.Eventually(t, func() bool { return len(h.mails()) == 1 }, 3*time.Second, 20*time.Millisecond)
	require.Equal(t, "support-staff->ops@example.com", h.mails()[0])
}

func TestWhenTheAgentFailsStaffAreEmailedAtOnce(t *testing.T) {
	h := setupSupportTest(t)
	startFakeAgent(t, "fail")
	withAIRoutes(h)
	id := h.open(t, "Q", "hello?")
	_, events := h.askAI(t, id)
	require.Equal(t, []string{"stored"}, events)
	require.Eventually(t, func() bool { return len(h.mails()) == 1 }, 3*time.Second, 20*time.Millisecond)
	// Told once: the sweep does not email again.
	model.DB.Exec("UPDATE support_messages SET created_at = created_at - 600")
	rows, _ := model.UnhandledSupportMessages(time.Now().Add(-service.SupportAgentSweepAfter).Unix(), 10)
	require.Empty(t, rows)
}

func TestTheSweepCatchesMessagesTheAgentNeverSaw(t *testing.T) {
	h := setupSupportTest(t)
	startFakeAgent(t, "answer")
	withAIRoutes(h)
	id := h.open(t, "Q", "nobody asked the agent")
	rows, _ := model.UnhandledSupportMessages(time.Now().Add(-service.SupportAgentSweepAfter).Unix(), 10)
	require.Empty(t, rows, "too recent to sweep")
	model.DB.Exec("UPDATE support_messages SET created_at = created_at - 600")
	rows, _ = model.UnhandledSupportMessages(time.Now().Add(-service.SupportAgentSweepAfter).Unix(), 10)
	require.Len(t, rows, 1)
	require.Equal(t, id, rows[0].TicketId)
	service.NotifyStaffOfUnhandledSupportMessage(rows[0].TicketId, rows[0].MessageId)
	require.Eventually(t, func() bool { return len(h.mails()) == 1 }, 3*time.Second, 20*time.Millisecond)
	rows, _ = model.UnhandledSupportMessages(time.Now().Add(-service.SupportAgentSweepAfter).Unix(), 10)
	require.Empty(t, rows, "emailed once only")
}

func TestWithTheAgentOffNothingChanges(t *testing.T) {
	h := setupSupportTest(t)
	withAIRoutes(h)
	id := h.open(t, "Q", "body")
	require.Eventually(t, func() bool { return len(h.mails()) == 1 }, 3*time.Second, 20*time.Millisecond)
	code, _ := h.askAI(t, id)
	require.Equal(t, http.StatusNotFound, code)
}

func toolsToken(t *testing.T, iss, aud string, customerId int) string {
	now := time.Now()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": iss, "aud": aud, "sub": strconv.Itoa(customerId), "cid": "1",
		"jti": uuid.NewString(), "iat": now.Unix(), "exp": now.Add(60 * time.Second).Unix(),
	}).SignedString([]byte(aiTestSecret))
	require.NoError(t, err)
	return s
}

func (h *supportHarness) agentGet(t *testing.T, path, token string) (int, map[string]any) {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.r.ServeHTTP(w, req)
	var out map[string]any
	body, _ := io.ReadAll(w.Body)
	_ = json.Unmarshal(body, &out)
	return w.Code, out
}

func TestTheAgentCanReadOnlyTheCustomerItsTokenNames(t *testing.T) {
	h := setupSupportTest(t)
	startFakeAgent(t, "answer")
	withAIRoutes(h)
	require.NoError(t, model.DB.AutoMigrate(&model.Log{}))
	prevLog := model.LOG_DB
	model.LOG_DB = model.DB
	t.Cleanup(func() { model.LOG_DB = prevLog })
	prevRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = prevRedis })

	model.DB.Model(&model.User{}).Where("id = ?", h.customer.Id).Update("quota", int(common.QuotaPerUnit*12.5))
	now := time.Now().Unix()
	mine := "202611051241013575764408268d9d6QHyGD390"
	theirs := "202611051241013575764408268d9d6OTHER0001"
	require.NoError(t, model.DB.Create(&[]model.Log{
		{UserId: h.customer.Id, Type: model.LogTypeError, CreatedAt: now - 60, ModelName: "gpt-4.1-mini", RequestId: mine,
			Content: "status_code=503, No available channel for model gpt-4.1-mini-fk under group GPT Official (distributor)"},
		{UserId: h.other.Id, Type: model.LogTypeConsume, CreatedAt: now - 30, ModelName: "gpt-4o", RequestId: theirs, Quota: 1000},
	}).Error)

	tok := toolsToken(t, "unifyselfai", "unifyapi-console:support-tools", h.customer.Id)
	code, out := h.agentGet(t, "/api/support/agent/account", tok)
	require.Equal(t, http.StatusOK, code)
	acct := out["data"].(map[string]any)
	require.Equal(t, 12.5, acct["balance_usd"])
	require.EqualValues(t, 1, acct["failed_requests_24h"])
	raw, _ := json.Marshal(acct)
	require.NotContains(t, string(raw), "GPT Official", "supplier group names never reach the agent")
	require.NotContains(t, string(raw), "-fk")
	require.Contains(t, string(raw), `"status_code":"503"`)

	code, _ = h.agentGet(t, "/api/support/agent/account", tok)
	require.Equal(t, http.StatusUnauthorized, code, "a tools token is single-use")

	code, out = h.agentGet(t, "/api/support/agent/request/"+mine, toolsToken(t, "unifyselfai", "unifyapi-console:support-tools", h.customer.Id))
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "error", out["data"].(map[string]any)["outcome"])
	code, _ = h.agentGet(t, "/api/support/agent/request/"+theirs, toolsToken(t, "unifyselfai", "unifyapi-console:support-tools", h.customer.Id))
	require.Equal(t, http.StatusNotFound, code, "another customer's request is 'not found'")

	// A token minted for the other direction, or never minted by the agent, is refused.
	code, _ = h.agentGet(t, "/api/support/agent/account", toolsToken(t, "unifyapi-console", "unifyselfai:support-agent", h.customer.Id))
	require.Equal(t, http.StatusUnauthorized, code)
	code, _ = h.agentGet(t, "/api/support/agent/account", "garbage")
	require.Equal(t, http.StatusUnauthorized, code)
}
