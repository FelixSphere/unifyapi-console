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

// UNIFYAPI-FORK: "Talk to a human" from the support chat widget.
//
// A handoff is represented exactly the way the AI agent's own escalation
// already is -- no new flag, no new status:
//   - the conversation is "open" (Needs reply in the operator inbox);
//   - staff are emailed through NotifyStaffOfSupportMessage, with a marker
//     line saying why, as AskSupportAI does for "[AI handoff: ...]";
//   - the customer message is marked handled, so neither the AI agent nor the
//     two-minute safety sweep picks it up again.
// The request itself is stored as a customer message with a fixed body, so
// the inbox thread shows when and that the customer asked, and the widget
// can recognise it and show it in the customer's own language.

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// SupportHandoffBody is the stored text of a customer's request for a person.
// web/src/features/support/widget-logic.ts matches it word for word.
const SupportHandoffBody = "[handoff] The customer asked to talk to a person."

// HandOffMySupportTicket is POST /api/support/tickets/:id/handoff (owner only;
// another customer's ticket answers 404 like a missing one).
func HandOffMySupportTicket(c *gin.Context) {
	ticket, ok := supportOwnTicket(c)
	if !ok {
		return
	}
	msgs, err := model.GetSupportMessages(ticket.Id)
	if err != nil {
		supportFail(c, http.StatusInternalServerError, "could not save your message, please try again")
		return
	}
	// Asking twice in a row is one request: no second message, no second email.
	if n := len(msgs); n > 0 && !msgs[n-1].FromStaff && msgs[n-1].Body == SupportHandoffBody {
		supportConversation(c, ticket)
		return
	}
	if !supportCustomerAllowed(c, ticket.UserId, false) {
		return
	}
	msg, err := model.AddSupportMessage(ticket.Id, ticket.UserId, false, SupportHandoffBody)
	if err != nil {
		supportFail(c, http.StatusInternalServerError, "could not save your message, please try again")
		return
	}
	// Staff are told right here, so this message is dealt with.
	if err := model.MarkSupportMessageHandled(ticket.Id, msg.Id); err != nil {
		common.SysError("support handoff: mark handled: " + err.Error())
	}
	if customer, err := model.GetUserById(ticket.UserId, false); err == nil {
		service.NotifyStaffOfSupportMessage(ticket, customer, lastCustomerQuestion(msgs)+"\n\n[Customer asked to talk to a person]", false)
	}
	events := []string{service.SupportEventHandoff}
	if ticket.Status == model.SupportStatusClosed {
		events = []string{service.SupportEventReopened, service.SupportEventHandoff}
	}
	service.NotifySupportAgentEvents(ticket, events, "")
	supportConversation(c, ticket)
}

// lastCustomerQuestion is what staff should read first: the customer's newest
// real message, not an earlier handoff marker.
func lastCustomerQuestion(msgs []model.SupportMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if !msgs[i].FromStaff && msgs[i].Body != SupportHandoffBody {
			return msgs[i].Body
		}
	}
	return SupportHandoffBody
}
