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
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The agent stamps nothing of its own on an event and "at" is whole seconds,
// so a close, a reopen and a second close in one second would look to it like
// one close and its retry. Each event therefore carries an id: the same on
// every retry of that event, different for every other event. The agent keys
// its duplicate check on it.

var agentEventId = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func eventIdsOf(t *testing.T, bodies []string) []string {
	t.Helper()
	ids := make([]string, 0, len(bodies))
	for _, b := range bodies {
		var ev SupportAgentEvent
		require.NoError(t, json.Unmarshal([]byte(b), &ev))
		require.Regexp(t, agentEventId, ev.Id, "the agent accepts only this shape")
		ids = append(ids, ev.Id)
	}
	return ids
}

func TestARetryCarriesTheSameEventId(t *testing.T) {
	a := startScriptedAgent(t, http.StatusBadGateway, http.StatusBadGateway, http.StatusOK)
	require.NoError(t, notifyAndWait(t))
	ids := eventIdsOf(t, a.bodies)
	require.Len(t, ids, 3)
	require.Equal(t, ids[0], ids[1])
	require.Equal(t, ids[0], ids[2])
}

func TestEventsOfOneTypeInOneSecondHaveDifferentIds(t *testing.T) {
	a := startScriptedAgent(t, http.StatusOK)
	sent := captureEvents(t)
	NotifySupportAgentEvent(eventTicket, SupportEventClosed, "")
	NotifySupportAgentEvents(eventTicket, []string{SupportEventReopened, SupportEventStaffReply}, "back again")
	NotifySupportAgentEvent(eventTicket, SupportEventClosed, "")
	for i := 0; i < 4; i++ {
		select {
		case err := <-sent:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("delivery never finished")
		}
	}
	ids := eventIdsOf(t, a.bodies)
	require.Len(t, ids, 4)
	seen := map[string]bool{}
	for _, id := range ids {
		require.False(t, seen[id], "every event has its own id, even two closes in one second")
		seen[id] = true
	}
}
