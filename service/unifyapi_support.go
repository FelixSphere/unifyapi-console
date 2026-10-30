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

// UNIFYAPI-FORK: the two emails that make customer support work without
// anyone watching the inbox page -- staff hear about every customer message,
// and the customer hears about every reply. Both are transactional (the
// customer started the conversation), so neither carries an unsubscribe.
// Sending never blocks or fails the request that triggered it.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/bytedance/gopkg/util/gopool"
)

const supportEmailExcerpt = 2000 // characters of the message quoted in the email

func SupportConversationURL(ticketId int) string {
	return strings.TrimRight(system_setting.ServerAddress, "/") + "/support?ticket=" + strconv.Itoa(ticketId)
}

func excerpt(s string) string {
	r := []rune(s)
	if len(r) <= supportEmailExcerpt {
		return s
	}
	return string(r[:supportEmailExcerpt]) + "…"
}

// SupportStaffEmail is what an operator gets when a customer writes.
func SupportStaffEmail(ticket *model.SupportTicket, customer *model.User, body string, isNew bool) common.EmailMessage {
	headline := "New message from " + customer.Username
	if isNew {
		headline = "New question from " + customer.Username
	}
	return common.EmailMessage{
		Subject:      fmt.Sprintf("[Support #%d] %s: %s", ticket.Id, customer.Username, ticket.Subject),
		Preheader:    ticket.Subject,
		Eyebrow:      "Support",
		Headline:     headline,
		Intro:        excerpt(body),
		CTALabel:     "Reply in the console",
		CTAURL:       SupportConversationURL(ticket.Id),
		Details:      []common.EmailDetail{{Label: "Subject", Value: ticket.Subject}, {Label: "Customer", Value: customer.Username + " · " + customer.Email}},
		FooterReason: "You get this email because you are staff on Unify API.",
	}
}

// SupportReplyEmail is what the customer gets when staff reply.
func SupportReplyEmail(ticket *model.SupportTicket, body string) common.EmailMessage {
	return common.EmailMessage{
		Subject:      "Re: " + ticket.Subject,
		Preheader:    "We replied to your question.",
		Eyebrow:      "Support",
		Headline:     "We replied to your question",
		Intro:        excerpt(body),
		CTALabel:     "View the conversation",
		CTAURL:       SupportConversationURL(ticket.Id),
		Details:      []common.EmailDetail{{Label: "Your question", Value: ticket.Subject}},
		FooterReason: "You get this email because you asked Unify API support a question. Reply in the console, not to this email.",
	}
}

func NotifyStaffOfSupportMessage(ticket *model.SupportTicket, customer *model.User, body string, isNew bool) {
	if common.SMTPServer == "" {
		return
	}
	msg := SupportStaffEmail(ticket, customer, body, isNew)
	// Read recipients now, on the request's goroutine; only the slow SMTP
	// conversations go to the background.
	emails, err := model.SupportStaffEmails()
	if err != nil {
		common.SysError("support: staff emails: " + err.Error())
		return
	}
	gopool.Go(func() {
		for _, to := range emails {
			if err := SendBrandedEmail("support-staff", to, msg); err != nil {
				common.SysError(fmt.Sprintf("support: notify staff of #%d: %s", ticket.Id, err.Error()))
			}
		}
	})
}

func NotifyCustomerOfSupportReply(ticket *model.SupportTicket, customer *model.User, body string) {
	if common.SMTPServer == "" || customer.Email == "" {
		return
	}
	msg := SupportReplyEmail(ticket, body)
	gopool.Go(func() {
		if err := SendBrandedEmail("support-reply", customer.Email, msg); err != nil {
			common.SysError(fmt.Sprintf("support: notify customer of #%d: %s", ticket.Id, err.Error()))
		}
	})
}
