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

// UNIFYAPI-FORK: customer questions and messages. See model/unifyapi_support.go.
//
// Two route groups: /api/support (UserAuth) where a customer sees only their
// own conversations -- someone else's id answers "not found", not
// "forbidden", so ids cannot be probed -- and /api/support/admin (AdminAuth),
// the operator inbox.

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

const (
	supportSubjectMax = 200
	supportBodyMax    = 5000
	// Abuse ceilings for one account: the 2026-09-28 signup wave made 165
	// accounts in two days, and every customer message emails every operator.
	supportMaxUnclosed      = 10
	supportMaxMessagesPerHr = 20
	supportInboxLimit       = 200
)

type supportCreateRequest struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

type supportMessageRequest struct {
	Body string `json:"body"`
}

type supportStatusRequest struct {
	Status string `json:"status"`
}

func supportFail(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{"success": false, "message": msg})
}

func supportOK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": data})
}

func cleanSupportText(s string, max int, field string) (string, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", field + " is required"
	}
	if utf8.RuneCountInString(s) > max {
		return "", field + " is too long (max " + strconv.Itoa(max) + " characters)"
	}
	return s, ""
}

// supportCustomerAllowed enforces the per-account ceilings before a write.
func supportCustomerAllowed(c *gin.Context, userId int, newTicket bool) bool {
	n, err := model.CountCustomerSupportMessagesSince(userId, time.Now().Add(-time.Hour).Unix())
	if err != nil {
		supportFail(c, http.StatusInternalServerError, "could not save your message, please try again")
		return false
	}
	if n >= supportMaxMessagesPerHr {
		supportFail(c, http.StatusTooManyRequests, "you have sent a lot of messages in the last hour; please wait a little")
		return false
	}
	if newTicket {
		open, err := model.CountUnclosedSupportTickets(userId)
		if err != nil {
			supportFail(c, http.StatusInternalServerError, "could not save your message, please try again")
			return false
		}
		if open >= supportMaxUnclosed {
			supportFail(c, http.StatusTooManyRequests, "you have "+strconv.FormatInt(open, 10)+" open conversations; add to one of them instead")
			return false
		}
	}
	return true
}

// supportOwnTicket loads a ticket the caller owns, or answers 404.
func supportOwnTicket(c *gin.Context) (*model.SupportTicket, bool) {
	id, _ := strconv.Atoi(c.Param("id"))
	ticket, err := model.GetSupportTicket(id)
	if err != nil || ticket.UserId != c.GetInt("id") {
		supportFail(c, http.StatusNotFound, model.ErrSupportTicketNotFound.Error())
		return nil, false
	}
	return ticket, true
}

func supportAnyTicket(c *gin.Context) (*model.SupportTicket, bool) {
	id, _ := strconv.Atoi(c.Param("id"))
	ticket, err := model.GetSupportTicket(id)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, model.ErrSupportTicketNotFound) {
			status = http.StatusNotFound
		}
		supportFail(c, status, err.Error())
		return nil, false
	}
	return ticket, true
}

// supportConversation answers with the ticket re-read from the database, so
// a response after a write shows the status that write produced.
func supportConversation(c *gin.Context, ticket *model.SupportTicket) {
	ticket, err := model.GetSupportTicket(ticket.Id)
	if err != nil {
		supportFail(c, http.StatusInternalServerError, err.Error())
		return
	}
	msgs, err := model.GetSupportMessages(ticket.Id)
	if err != nil {
		supportFail(c, http.StatusInternalServerError, err.Error())
		return
	}
	supportOK(c, gin.H{"ticket": ticket, "messages": msgs})
}

// --- customer ---

func ListMySupportTickets(c *gin.Context) {
	tickets, err := model.ListSupportTicketsForUser(c.GetInt("id"))
	if err != nil {
		supportFail(c, http.StatusInternalServerError, err.Error())
		return
	}
	supportOK(c, tickets)
}

func CreateSupportTicket(c *gin.Context) {
	var req supportCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		supportFail(c, http.StatusBadRequest, "invalid request")
		return
	}
	subject, problem := cleanSupportText(req.Subject, supportSubjectMax, "Subject")
	if problem != "" {
		supportFail(c, http.StatusBadRequest, problem)
		return
	}
	body, problem := cleanSupportText(req.Body, supportBodyMax, "Message")
	if problem != "" {
		supportFail(c, http.StatusBadRequest, problem)
		return
	}
	userId := c.GetInt("id")
	if !supportCustomerAllowed(c, userId, true) {
		return
	}
	ticket, err := model.CreateSupportTicket(userId, subject, body)
	if err != nil {
		supportFail(c, http.StatusInternalServerError, "could not save your message, please try again")
		return
	}
	if customer, err := model.GetUserById(userId, false); err == nil {
		service.NotifyStaffOfSupportMessage(ticket, customer, body, true)
	}
	supportOK(c, ticket)
}

func GetMySupportTicket(c *gin.Context) {
	if ticket, ok := supportOwnTicket(c); ok {
		supportConversation(c, ticket)
	}
}

func AddMySupportMessage(c *gin.Context) {
	ticket, ok := supportOwnTicket(c)
	if !ok {
		return
	}
	var req supportMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		supportFail(c, http.StatusBadRequest, "invalid request")
		return
	}
	body, problem := cleanSupportText(req.Body, supportBodyMax, "Message")
	if problem != "" {
		supportFail(c, http.StatusBadRequest, problem)
		return
	}
	if !supportCustomerAllowed(c, ticket.UserId, false) {
		return
	}
	if _, err := model.AddSupportMessage(ticket.Id, ticket.UserId, false, body); err != nil {
		supportFail(c, http.StatusInternalServerError, "could not save your message, please try again")
		return
	}
	if customer, err := model.GetUserById(ticket.UserId, false); err == nil {
		service.NotifyStaffOfSupportMessage(ticket, customer, body, false)
	}
	supportConversation(c, ticket)
}

func CloseMySupportTicket(c *gin.Context) {
	ticket, ok := supportOwnTicket(c)
	if !ok {
		return
	}
	if err := model.SetSupportTicketStatus(ticket.Id, model.SupportStatusClosed); err != nil {
		supportFail(c, http.StatusInternalServerError, err.Error())
		return
	}
	supportConversation(c, ticket)
}

// --- operator inbox ---

func ListSupportTickets(c *gin.Context) {
	status := c.Query("status")
	switch status {
	case "", model.SupportStatusOpen, model.SupportStatusAnswered, model.SupportStatusClosed:
	default:
		supportFail(c, http.StatusBadRequest, "unknown status")
		return
	}
	rows, err := model.ListSupportTickets(status, supportInboxLimit)
	if err != nil {
		supportFail(c, http.StatusInternalServerError, err.Error())
		return
	}
	supportOK(c, rows)
}

func GetSupportTicket(c *gin.Context) {
	ticket, ok := supportAnyTicket(c)
	if !ok {
		return
	}
	msgs, err := model.GetSupportMessages(ticket.Id)
	if err != nil {
		supportFail(c, http.StatusInternalServerError, err.Error())
		return
	}
	data := gin.H{"ticket": ticket, "messages": msgs}
	if customer, err := model.GetUserById(ticket.UserId, false); err == nil {
		data["customer"] = gin.H{"id": customer.Id, "username": customer.Username, "email": customer.Email}
	}
	supportOK(c, data)
}

func ReplySupportTicket(c *gin.Context) {
	ticket, ok := supportAnyTicket(c)
	if !ok {
		return
	}
	var req supportMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		supportFail(c, http.StatusBadRequest, "invalid request")
		return
	}
	body, problem := cleanSupportText(req.Body, supportBodyMax, "Reply")
	if problem != "" {
		supportFail(c, http.StatusBadRequest, problem)
		return
	}
	if _, err := model.AddSupportMessage(ticket.Id, c.GetInt("id"), true, body); err != nil {
		supportFail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if customer, err := model.GetUserById(ticket.UserId, false); err == nil {
		service.NotifyCustomerOfSupportReply(ticket, customer, body)
	}
	GetSupportTicket(c)
}

func SetSupportTicketStatus(c *gin.Context) {
	ticket, ok := supportAnyTicket(c)
	if !ok {
		return
	}
	var req supportStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		supportFail(c, http.StatusBadRequest, "invalid request")
		return
	}
	switch req.Status {
	case model.SupportStatusOpen, model.SupportStatusAnswered, model.SupportStatusClosed:
	default:
		supportFail(c, http.StatusBadRequest, "unknown status")
		return
	}
	if err := model.SetSupportTicketStatus(ticket.Id, req.Status); err != nil {
		supportFail(c, http.StatusInternalServerError, err.Error())
		return
	}
	GetSupportTicket(c)
}
