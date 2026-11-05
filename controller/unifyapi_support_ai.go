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

// UNIFYAPI-FORK: the AI first responder on /support. See
// service/unifyapi_support_agent.go for the design; this file is the HTTP
// surface: the customer-facing stream, and the two read-only endpoints the
// agent calls back for the customer it is talking to.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// SupportAIStatus tells the SPA whether to ask the agent after a message.
func SupportAIStatus(c *gin.Context) {
	supportOK(c, gin.H{"enabled": service.SupportAgentEnabled()})
}

var (
	supportAIRunningMu sync.Mutex
	supportAIRunning   = map[int]bool{}
)

func supportAIClaim(ticketId int) bool {
	supportAIRunningMu.Lock()
	defer supportAIRunningMu.Unlock()
	if supportAIRunning[ticketId] {
		return false
	}
	supportAIRunning[ticketId] = true
	return true
}

func supportAIRelease(ticketId int) {
	supportAIRunningMu.Lock()
	delete(supportAIRunning, ticketId)
	supportAIRunningMu.Unlock()
}

type supportAIEvent struct {
	event string
	data  json.RawMessage
}

// AskSupportAI streams the agent's answer to the customer's latest message.
// The agent runs on a background context: if the customer closes the page,
// the answer is still stored, and they see it when they come back.
func AskSupportAI(c *gin.Context) {
	ticket, ok := supportOwnTicket(c)
	if !ok {
		return
	}
	if !service.SupportAgentEnabled() {
		supportFail(c, http.StatusNotFound, "the AI assistant is not enabled")
		return
	}
	msgs, err := model.GetSupportMessages(ticket.Id)
	if err != nil || len(msgs) == 0 {
		supportFail(c, http.StatusInternalServerError, "could not read the conversation")
		return
	}
	last := msgs[len(msgs)-1]
	if last.FromStaff || last.Id <= ticket.HandledMessageId {
		supportFail(c, http.StatusConflict, "nothing new to answer")
		return
	}
	if !supportAIClaim(ticket.Id) {
		supportFail(c, http.StatusConflict, "already answering")
		return
	}

	events := make(chan supportAIEvent, 16)
	go func() {
		defer supportAIRelease(ticket.Id)
		defer close(events)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		forward := func(event string, data json.RawMessage) {
			select {
			case events <- supportAIEvent{event, data}:
			default: // nobody is reading fast enough; the stored answer is what counts
			}
		}
		result, err := service.AskSupportAgent(ctx, ticket.UserId, ticket.Id, service.SupportAgentTurns(msgs), forward)
		if err != nil {
			common.SysError(fmt.Sprintf("support agent #%d: %s", ticket.Id, err.Error()))
			// The customer must not be left waiting on a dead agent.
			service.NotifyStaffOfUnhandledSupportMessage(ticket.Id, last.Id)
			forward("stored", json.RawMessage(`{"ok":false}`))
			return
		}
		if _, err := model.AddSupportAIMessage(ticket.Id, result.Answer, result.Handoff != "", last.Id); err != nil {
			common.SysError(fmt.Sprintf("support agent #%d: store: %s", ticket.Id, err.Error()))
			service.NotifyStaffOfUnhandledSupportMessage(ticket.Id, last.Id)
			forward("stored", json.RawMessage(`{"ok":false}`))
			return
		}
		if result.Handoff != "" {
			if customer, err := model.GetUserById(ticket.UserId, false); err == nil {
				service.NotifyStaffOfSupportMessage(ticket, customer, last.Body+"\n\n[AI handoff: "+result.Handoff+"]", false)
			}
		}
		forward("stored", json.RawMessage(`{"ok":true}`))
	}()

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-store")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	gone := c.Request.Context().Done()
	for {
		select {
		case ev, open := <-events:
			if !open {
				return
			}
			fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", ev.event, ev.data)
			c.Writer.Flush()
		case <-gone:
			return // the agent keeps going and stores its answer
		}
	}
}

func supportToolsCustomer(c *gin.Context) (int, bool) {
	auth := c.GetHeader("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		supportFail(c, http.StatusUnauthorized, "unauthorized")
		return 0, false
	}
	customerId, err := service.VerifySupportToolsToken(strings.TrimPrefix(auth, "Bearer "))
	if err != nil {
		supportFail(c, http.StatusUnauthorized, "unauthorized")
		return 0, false
	}
	return customerId, true
}

// SupportAgentAccount is the agent's view of the customer it is talking to.
func SupportAgentAccount(c *gin.Context) {
	customerId, ok := supportToolsCustomer(c)
	if !ok {
		return
	}
	sum, err := model.GetSupportAccountSummary(customerId)
	if err != nil {
		supportFail(c, http.StatusInternalServerError, "could not read the account")
		return
	}
	supportOK(c, sum)
}

var supportRequestIdRe = regexp.MustCompile(`^[0-9A-Za-z]{20,64}$`)

// SupportAgentRequest returns one of that customer's own requests; another
// customer's request id answers "not found", exactly like a missing one.
func SupportAgentRequest(c *gin.Context) {
	customerId, ok := supportToolsCustomer(c)
	if !ok {
		return
	}
	rid := c.Param("rid")
	if !supportRequestIdRe.MatchString(rid) {
		supportFail(c, http.StatusBadRequest, "not a request id")
		return
	}
	info, err := model.GetSupportRequestInfo(customerId, rid)
	if err != nil {
		supportFail(c, http.StatusInternalServerError, "could not look up the request")
		return
	}
	if info == nil {
		supportFail(c, http.StatusNotFound, "no such request on this account")
		return
	}
	supportOK(c, info)
}
