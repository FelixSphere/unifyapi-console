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
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

// The agent applies events in the order they arrive (UnifySelfAI
// lib/support-agent/cases.ts recordEvent; "at" only feeds its duplicate key),
// so a "closed" that lands after "reopened" leaves the CRM Case closed on an
// open conversation. Events about one ticket must arrive in the order they
// happened, even when one of them needs a retry.

func eventTypes(t *testing.T, bodies []string) []string {
	t.Helper()
	out := make([]string, 0, len(bodies))
	for _, b := range bodies {
		var ev SupportAgentEvent
		require.NoError(t, json.Unmarshal([]byte(b), &ev))
		out = append(out, ev.Type)
	}
	return out
}

func TestALaterEventNeverOvertakesARetry(t *testing.T) {
	a := startScriptedAgent(t, http.StatusBadGateway, http.StatusOK)
	sent := captureEvents(t)
	NotifySupportAgentEvent(eventTicket, SupportEventClosed, "")
	NotifySupportAgentEvent(eventTicket, SupportEventReopened, "")
	NotifySupportAgentEvents(eventTicket, []string{SupportEventHandoff}, "")
	for i := 0; i < 3; i++ {
		select {
		case err := <-sent:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("delivery never finished")
		}
	}
	a.mu.Lock()
	bodies := append([]string(nil), a.bodies...)
	a.mu.Unlock()
	require.Equal(t, []string{"closed", "closed", "reopened", "handoff"}, eventTypes(t, bodies),
		"the 502'd close is retried before the reopen is sent")
}

func TestOneTicketsRetriesDoNotHoldUpAnother(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	var got []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var ev SupportAgentEvent
		_ = json.Unmarshal(body, &ev)
		if ev.TicketId == 42 {
			<-release
		}
		mu.Lock()
		got = append(got, ev.TicketId)
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	t.Setenv("SUPPORT_AGENT_URL", srv.URL)
	t.Setenv("SUPPORT_AGENT_SECRET", eventsTestSecret)
	sent := captureEvents(t)

	NotifySupportAgentEvent(eventTicket, SupportEventClosed, "")
	NotifySupportAgentEvent(&model.SupportTicket{Id: 43, UserId: 8}, SupportEventClosed, "")
	select {
	case err := <-sent:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("ticket 43 waited for ticket 42")
	}
	mu.Lock()
	require.Equal(t, []int{43}, got)
	mu.Unlock()
	close(release)
	select {
	case err := <-sent:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("ticket 42 was never delivered")
	}
}
