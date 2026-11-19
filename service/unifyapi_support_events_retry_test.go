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
package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

// The agent's events endpoint (UnifySelfAI#184) says 502 when its CRM write
// failed and a retry is safe, 202 when it has no CRM, 401/403 on auth. So the
// console retries network errors and 5xx -- three attempts in all, each with
// a fresh token, the original "at" -- and nothing else.

func fastSupportRetries(t *testing.T) {
	t.Helper()
	prev := supportEventBackoff
	supportEventBackoff = []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}
	t.Cleanup(func() { supportEventBackoff = prev })
}

type scriptedAgent struct {
	mu     sync.Mutex
	codes  []int // answered in order; the last one repeats
	jtis   []string
	bodies []string
}

func (a *scriptedAgent) attempts() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.jtis)
}

func startScriptedAgent(t *testing.T, codes ...int) *scriptedAgent {
	t.Helper()
	a := &scriptedAgent{codes: codes}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, err := jwt.Parse(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "),
			func(*jwt.Token) (any, error) { return []byte(eventsTestSecret), nil },
			jwt.WithAudience("unifyselfai:support-agent"), jwt.WithIssuer("unifyapi-console"))
		body, _ := io.ReadAll(r.Body)
		a.mu.Lock()
		n := len(a.jtis)
		jti := ""
		if err == nil {
			jti, _ = tok.Claims.(jwt.MapClaims)["jti"].(string)
		}
		a.jtis = append(a.jtis, jti)
		a.bodies = append(a.bodies, string(body))
		code := a.codes[len(a.codes)-1]
		if n < len(a.codes) {
			code = a.codes[n]
		}
		a.mu.Unlock()
		w.WriteHeader(code)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("SUPPORT_AGENT_URL", srv.URL)
	t.Setenv("SUPPORT_AGENT_SECRET", eventsTestSecret)
	fastSupportRetries(t)
	return a
}

func notifyAndWait(t *testing.T) error {
	t.Helper()
	sent := captureEvents(t)
	NotifySupportAgentEvent(eventTicket, SupportEventStaffReply, "reply text")
	select {
	case err := <-sent:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("delivery never finished")
		return nil
	}
}

func TestA502IsRetriedAndThenSucceeds(t *testing.T) {
	a := startScriptedAgent(t, http.StatusBadGateway, http.StatusOK)
	require.NoError(t, notifyAndWait(t))
	require.Equal(t, 2, a.attempts())
	require.Equal(t, a.bodies[0], a.bodies[1], "a retry resends the same event, original at included")
}

func TestA403IsNotRetried(t *testing.T) {
	a := startScriptedAgent(t, http.StatusForbidden)
	require.ErrorContains(t, notifyAndWait(t), "HTTP 403")
	require.Equal(t, 1, a.attempts())
}

func TestA401IsNotRetried(t *testing.T) {
	a := startScriptedAgent(t, http.StatusUnauthorized)
	require.Error(t, notifyAndWait(t))
	require.Equal(t, 1, a.attempts())
}

func TestA202IsFinalAndNotRetried(t *testing.T) {
	a := startScriptedAgent(t, http.StatusAccepted)
	require.NoError(t, notifyAndWait(t))
	require.Equal(t, 1, a.attempts())
}

func TestThreeServerErrorsGiveUpAfterThreeAttempts(t *testing.T) {
	a := startScriptedAgent(t, http.StatusBadGateway, http.StatusInternalServerError, http.StatusServiceUnavailable, http.StatusOK)
	log := captureLog(t)
	require.ErrorContains(t, notifyAndWait(t), "HTTP 503")
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, 3, a.attempts(), "never a fourth attempt")
	out := log.String()
	require.Equal(t, 1, strings.Count(out, "[WARN]"), "only the final failure is logged")
	require.Contains(t, out, "not delivered after 3 attempt(s): HTTP 503")
	require.NotContains(t, out, "reply text")
	require.NotContains(t, out, "Bearer")
}

func TestEveryAttemptCarriesItsOwnJti(t *testing.T) {
	a := startScriptedAgent(t, http.StatusBadGateway)
	require.Error(t, notifyAndWait(t))
	require.Equal(t, 3, a.attempts())
	seen := map[string]bool{}
	for _, jti := range a.jtis {
		require.NotEmpty(t, jti, "every attempt carries a valid token")
		require.False(t, seen[jti], "a jti is never reused")
		seen[jti] = true
	}
	require.Equal(t, a.bodies[0], a.bodies[2])
}

func TestANetworkErrorIsRetried(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing listens any more
	t.Setenv("SUPPORT_AGENT_URL", url)
	t.Setenv("SUPPORT_AGENT_SECRET", eventsTestSecret)
	fastSupportRetries(t)
	attempts, err := deliverSupportAgentEventWithRetry(url+"/api/support-agent/events", 7, SupportAgentEvent{Type: SupportEventClosed, TicketId: 42, At: 1})
	require.Error(t, err)
	require.Equal(t, 3, attempts)
}

func TestTheDefaultBackoffStaysUnderThirtySeconds(t *testing.T) {
	var total time.Duration
	for _, d := range supportEventBackoff {
		total += d
	}
	total += supportEventAttempts * supportEventTimeout
	require.Less(t, total, 30*time.Second)
	require.Equal(t, []time.Duration{2 * time.Second, 10 * time.Second}, supportEventBackoff)
}
