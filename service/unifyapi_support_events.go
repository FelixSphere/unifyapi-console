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
// So it is fire-and-forget on its own goroutine with a short timeout, it never
// blocks or fails the request that triggered it, and a failure is one WARN
// line that names neither the token nor the message body.
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

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
)

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
// "reopened" then "staff_reply") in order, on one background goroutine.
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
	go func() {
		for _, ev := range evs {
			err := deliverSupportAgentEvent(url, customerId, ev)
			if err != nil {
				logger.LogWarn(context.Background(), fmt.Sprintf("support event %s for #%d not delivered: %s", ev.Type, ev.TicketId, err.Error()))
			}
			if hook := supportEventSent; hook != nil {
				hook(ev, err)
			}
		}
	}()
}

func deliverSupportAgentEvent(url string, customerId int, ev SupportAgentEvent) error {
	token, err := mintSupportAgentToken(customerId, ev.TicketId)
	if err != nil {
		return fmt.Errorf("mint token: %w", err)
	}
	payload, _ := json.Marshal(ev)
	ctx, cancel := context.WithTimeout(context.Background(), supportEventTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := supportEventClient.Do(req)
	if err != nil {
		// The error text of a failed request names the URL, never the
		// headers, so the token cannot leak into the log.
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
