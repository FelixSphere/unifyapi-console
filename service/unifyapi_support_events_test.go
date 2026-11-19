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
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// The support event notifier is best effort: whatever the agent does, the
// customer's or operator's request must not wait for it or fail because of it.

const eventsTestSecret = "0123456789abcdef0123456789abcdef-events"

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func captureEvents(t *testing.T) chan error {
	t.Helper()
	ch := make(chan error, 8)
	supportEventSent = func(_ SupportAgentEvent, err error) { ch <- err }
	t.Cleanup(func() { supportEventSent = nil })
	return ch
}

func captureLog(t *testing.T) *syncBuf {
	t.Helper()
	buf := &syncBuf{}
	common.LogWriterMu.Lock()
	prevOut, prevErr := gin.DefaultWriter, gin.DefaultErrorWriter
	gin.DefaultWriter, gin.DefaultErrorWriter = buf, buf
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter, gin.DefaultErrorWriter = prevOut, prevErr
		common.LogWriterMu.Unlock()
	})
	return buf
}

var eventTicket = &model.SupportTicket{Id: 42, UserId: 7}

func TestSupportEventsAreOffWithoutTheAgent(t *testing.T) {
	sent := captureEvents(t)
	t.Setenv("SUPPORT_AGENT_URL", "")
	t.Setenv("SUPPORT_AGENT_SECRET", eventsTestSecret)
	NotifySupportAgentEvent(eventTicket, SupportEventClosed, "")
	t.Setenv("SUPPORT_AGENT_URL", "http://127.0.0.1:1")
	t.Setenv("SUPPORT_AGENT_SECRET", "too-short")
	NotifySupportAgentEvent(eventTicket, SupportEventClosed, "")
	select {
	case <-sent:
		t.Fatal("an event was attempted with the agent unconfigured")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestASlowAgentNeverBlocksTheCaller(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	t.Setenv("SUPPORT_AGENT_URL", srv.URL)
	t.Setenv("SUPPORT_AGENT_SECRET", eventsTestSecret)
	prev := supportEventClient
	supportEventClient = &http.Client{Timeout: 300 * time.Millisecond}
	t.Cleanup(func() { supportEventClient = prev })
	sent := captureEvents(t)
	log := captureLog(t)

	start := time.Now()
	NotifySupportAgentEvent(eventTicket, SupportEventStaffReply, "secret reply text")
	require.Less(t, time.Since(start), 50*time.Millisecond, "the caller returns before delivery")

	select {
	case err := <-sent:
		require.Error(t, err, "the slow delivery times out")
	case <-time.After(3 * time.Second):
		t.Fatal("delivery never finished")
	}
	out := log.String()
	require.Contains(t, out, "[WARN]")
	require.Contains(t, out, "support event staff_reply for #42 not delivered")
	require.NotContains(t, out, "secret reply text", "the body is never logged")
	require.NotContains(t, out, "Bearer", "the token is never logged")
	require.NotContains(t, out, "eyJ", "the token is never logged")
}

func TestAnUnreachableOrRefusingAgentIsOnlyAWarning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.True(t, strings.HasSuffix(r.URL.Path, "/api/support-agent/events"))
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("SUPPORT_AGENT_URL", srv.URL+"/") // a trailing slash is tolerated
	t.Setenv("SUPPORT_AGENT_SECRET", eventsTestSecret)
	sent := captureEvents(t)
	log := captureLog(t)
	NotifySupportAgentEvents(eventTicket, []string{SupportEventReopened, SupportEventHandoff}, "")
	for i := 0; i < 2; i++ {
		select {
		case err := <-sent:
			require.ErrorContains(t, err, "HTTP 500")
		case <-time.After(3 * time.Second):
			t.Fatal("delivery never finished")
		}
	}
	require.Contains(t, log.String(), "support event handoff for #42 not delivered: HTTP 500")
}
