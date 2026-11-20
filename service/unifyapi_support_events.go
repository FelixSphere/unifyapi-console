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

// UNIFYAPI-FORK: tell the AI support agent (UnifySelfAI) about conversation
// events the console handles without it, so the agent's side can keep its CRM
// Case in step with the console. The agent already hears every customer turn
// through POST /api/support-agent; these are the turns it would otherwise
// miss: a staff reply, a close, a reopen, and a customer asking for a person.
//
// Best effort by design. The console is the system of record for the
// conversation; a lost event costs the CRM a line, never the customer a reply.
// So delivery happens in the background with a short timeout, it never
// blocks or fails the request that triggered it, and a failure is one WARN
// line that names neither the token nor the message body.
//
// Events about one ticket are delivered one at a time, in the order they were
// raised. The agent applies them in arrival order ("at" only feeds its
// duplicate key), so a "closed" that overtook a "reopened" -- or that was
// retried after it -- would leave the CRM Case closed on an open conversation.
// Each ticket with events pending has one background goroutine draining its
// queue; different tickets do not wait for each other. The queue is per agent
// URL too, because order only matters at one receiver: events captured for an
// URL that has since changed must not hold up the new one.
//
// Contract (POST <SUPPORT_AGENT_URL>/api/support-agent/events):
//
//	Authorization: Bearer <HS256 JWT, iss "unifyapi-console",
//	               aud "unifyselfai:support-agent", sub = customer id,
//	               cid = ticket id, single-use jti, exp - iat = 60 s>
//	{"type":"staff_reply"|"closed"|"reopened"|"handoff",
//	 "ticket_id":N, "body":"..." (staff_reply only), "at":unix seconds}
//
// The token is minted exactly like the one on the agent call, so the agent
// verifies both with the same code.
//
// The agent answers 200 when applied, 202 when it has no CRM configured, 401
// or 403 on an auth problem, and 502 when its CRM write failed (its handling
// is idempotent, so a retry is safe). A network error, a timeout or any 5xx
// is retried -- up to supportEventAttempts in all, after 2 s and then 10 s --
// with a freshly minted token each time (the receiver burns every jti) and
// the original "at". Any 2xx, and any 4xx, ends it. Only the final failure
// is logged.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
)

const (
	SupportEventStaffReply = "staff_reply"
	SupportEventClosed     = "closed"
	SupportEventReopened   = "reopened"
	SupportEventHandoff    = "handoff"

	supportEventTimeout = 5 * time.Second
	// Three attempts of at most 5 s plus 2 s + 10 s of waiting: under 30 s.
	supportEventAttempts = 3
)

// supportEventBackoff is the wait before attempt 2 and attempt 3. A variable
// so tests need not wait 12 s.
var supportEventBackoff = []time.Duration{2 * time.Second, 10 * time.Second}

// SupportAgentEvent is the JSON body of one event.
type SupportAgentEvent struct {
	Type     string `json:"type"`
	TicketId int    `json:"ticket_id"`
	Body     string `json:"body,omitempty"`
	At       int64  `json:"at"`
}

// supportEventClient is a variable so a test can point it at a slow server.
var supportEventClient = &http.Client{Timeout: supportEventTimeout}

// supportEventSent, when set (tests only), is told each delivery's outcome.
var supportEventSent func(ev SupportAgentEvent, err error)

// NotifySupportAgentEvent posts one event to the agent in the background and
// returns at once. It does nothing unless the agent is configured. body is
// sent only for a staff reply.
func NotifySupportAgentEvent(ticket *model.SupportTicket, eventType, body string) {
	NotifySupportAgentEvents(ticket, []string{eventType}, body)
}

// NotifySupportAgentEvents posts several events about one moment (e.g.
// "reopened" then "staff_reply") in order, after any events about the same
// ticket that are still being delivered.
func NotifySupportAgentEvents(ticket *model.SupportTicket, eventTypes []string, body string) {
	if ticket == nil || len(eventTypes) == 0 || !SupportAgentEnabled() {
		return
	}
	now := time.Now().Unix()
	evs := make([]SupportAgentEvent, 0, len(eventTypes))
	for _, t := range eventTypes {
		ev := SupportAgentEvent{Type: t, TicketId: ticket.Id, At: now}
		if t == SupportEventStaffReply {
			ev.Body = body
		}
		evs = append(evs, ev)
	}
	customerId := ticket.UserId
	// Read the configuration now, on the caller's goroutine, so a delivery
	// cannot pick up a URL or secret changed after the event happened.
	url := supportAgentURL() + "/api/support-agent/events"
	jobs := make([]supportEventJob, 0, len(evs))
	for _, ev := range evs {
		jobs = append(jobs, supportEventJob{url: url, customerId: customerId, ev: ev})
	}
	enqueueSupportAgentEvents(supportEventQueueKey{url: url, ticketId: ticket.Id}, jobs)
}

type supportEventJob struct {
	url        string
	customerId int
	ev         SupportAgentEvent
}

type supportEventQueueKey struct {
	url      string
	ticketId int
}

// supportEventQueues holds each ticket's undelivered events. A key has an
// entry exactly while a goroutine is draining it, so the entry's presence is
// what says whether a new one must be started.
var supportEventQueues = struct {
	sync.Mutex
	pending map[supportEventQueueKey][]supportEventJob
}{pending: map[supportEventQueueKey][]supportEventJob{}}

func enqueueSupportAgentEvents(key supportEventQueueKey, jobs []supportEventJob) {
	q := &supportEventQueues
	q.Lock()
	_, draining := q.pending[key]
	q.pending[key] = append(q.pending[key], jobs...)
	q.Unlock()
	if !draining {
		go drainSupportAgentEvents(key)
	}
}

func drainSupportAgentEvents(key supportEventQueueKey) {
	q := &supportEventQueues
	for {
		q.Lock()
		jobs := q.pending[key]
		if len(jobs) == 0 {
			delete(q.pending, key)
			q.Unlock()
			return
		}
		job := jobs[0]
		q.pending[key] = jobs[1:]
		q.Unlock()

		attempts, err := deliverSupportAgentEventWithRetry(job.url, job.customerId, job.ev)
		if err != nil {
			logger.LogWarn(context.Background(), fmt.Sprintf("support event %s for #%d not delivered after %d attempt(s): %s", job.ev.Type, job.ev.TicketId, attempts, err.Error()))
		}
		if hook := supportEventSent; hook != nil {
			hook(job.ev, err)
		}
	}
}

// deliverSupportAgentEventWithRetry sends one event, retrying what the agent
// says is worth retrying. It returns how many attempts were made.
func deliverSupportAgentEventWithRetry(url string, customerId int, ev SupportAgentEvent) (int, error) {
	var err error
	for attempt := 1; attempt <= supportEventAttempts; attempt++ {
		if attempt > 1 {
			if i := attempt - 2; i < len(supportEventBackoff) {
				time.Sleep(supportEventBackoff[i])
			}
		}
		var retry bool
		retry, err = deliverSupportAgentEvent(url, customerId, ev)
		if err == nil || !retry {
			return attempt, err
		}
	}
	return supportEventAttempts, err
}

// deliverSupportAgentEvent makes one attempt with a freshly minted token. The
// bool says whether a failure is worth retrying: a network error, a timeout
// or a 5xx. A 4xx (auth, bad request) will not get better by asking again.
func deliverSupportAgentEvent(url string, customerId int, ev SupportAgentEvent) (bool, error) {
	token, err := mintSupportAgentToken(customerId, ev.TicketId, 0)
	if err != nil {
		return false, fmt.Errorf("mint token: %w", err)
	}
	payload, _ := json.Marshal(ev)
	ctx, cancel := context.WithTimeout(context.Background(), supportEventTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := supportEventClient.Do(req)
	if err != nil {
		// The error text of a failed request names the URL, never the
		// headers, so the token cannot leak into the log.
		return true, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return false, nil // 200 applied; 202 accepted without a CRM -- both final
	case resp.StatusCode >= 500:
		return true, fmt.Errorf("HTTP %d", resp.StatusCode)
	default:
		return false, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
}
