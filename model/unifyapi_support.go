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
package model

// UNIFYAPI-FORK: customer questions and messages (operator request
// 2026-10-13: "增加一个功能可以让客户提问题，留言"). A customer opens a
// conversation with a subject and a first message; staff and the customer
// then take turns. Upstream has nothing like it.
//
// Status says whose turn it is, which is the only thing the operator inbox
// needs to sort by:
//   open     -- the customer wrote last; waiting on us
//   answered -- staff wrote last; waiting on the customer
//   closed   -- done; a new customer message reopens it
//
// Migrated by MigrateSupport, called unconditionally at boot, so every
// environment has the tables whether or not anyone has used the feature.

import (
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	SupportStatusOpen     = "open"
	SupportStatusAnswered = "answered"
	SupportStatusClosed   = "closed"
)

var ErrSupportTicketNotFound = errors.New("conversation not found")

type SupportTicket struct {
	Id        int    `json:"id"`
	UserId    int    `json:"user_id" gorm:"index"`
	Subject   string `json:"subject" gorm:"type:varchar(200)"`
	Status    string `json:"status" gorm:"type:varchar(16);index"`
	CreatedAt int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt int64  `json:"updated_at" gorm:"bigint;index"`
}

type SupportMessage struct {
	Id        int    `json:"id"`
	TicketId  int    `json:"ticket_id" gorm:"index"`
	UserId    int    `json:"user_id"`
	FromStaff bool   `json:"from_staff"`
	Body      string `json:"body" gorm:"type:text"`
	CreatedAt int64  `json:"created_at" gorm:"bigint"`
}

// SupportTicketRow is a ticket with who asked, for the operator inbox.
type SupportTicketRow struct {
	SupportTicket
	Username string `json:"username"`
	Email    string `json:"email"`
}

func MigrateSupport() error {
	return DB.AutoMigrate(&SupportTicket{}, &SupportMessage{})
}

func CreateSupportTicket(userId int, subject, body string) (*SupportTicket, error) {
	now := time.Now().Unix()
	ticket := &SupportTicket{UserId: userId, Subject: subject, Status: SupportStatusOpen, CreatedAt: now, UpdatedAt: now}
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(ticket).Error; err != nil {
			return err
		}
		return tx.Create(&SupportMessage{TicketId: ticket.Id, UserId: userId, Body: body, CreatedAt: now}).Error
	})
	return ticket, err
}

// AddSupportMessage appends a message and hands the turn to the other side.
func AddSupportMessage(ticketId, userId int, fromStaff bool, body string) (*SupportMessage, error) {
	now := time.Now().Unix()
	msg := &SupportMessage{TicketId: ticketId, UserId: userId, FromStaff: fromStaff, Body: body, CreatedAt: now}
	status := SupportStatusOpen
	if fromStaff {
		status = SupportStatusAnswered
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(msg).Error; err != nil {
			return err
		}
		return tx.Model(&SupportTicket{}).Where("id = ?", ticketId).
			Updates(map[string]any{"status": status, "updated_at": now}).Error
	})
	return msg, err
}

func SetSupportTicketStatus(ticketId int, status string) error {
	return DB.Model(&SupportTicket{}).Where("id = ?", ticketId).
		Updates(map[string]any{"status": status, "updated_at": time.Now().Unix()}).Error
}

func GetSupportTicket(ticketId int) (*SupportTicket, error) {
	var t SupportTicket
	err := DB.First(&t, ticketId).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrSupportTicketNotFound
	}
	return &t, err
}

func GetSupportMessages(ticketId int) ([]SupportMessage, error) {
	var msgs []SupportMessage
	err := DB.Where("ticket_id = ?", ticketId).Order("id").Find(&msgs).Error
	return msgs, err
}

func ListSupportTicketsForUser(userId int) ([]SupportTicket, error) {
	var tickets []SupportTicket
	err := DB.Where("user_id = ?", userId).Order("updated_at DESC").Limit(200).Find(&tickets).Error
	return tickets, err
}

// ListSupportTickets is the operator inbox, newest activity first. An empty
// status lists everything.
func ListSupportTickets(status string, limit int) ([]SupportTicketRow, error) {
	var rows []SupportTicketRow
	q := DB.Table("support_tickets").
		Select("support_tickets.*, users.username, users.email").
		Joins("LEFT JOIN users ON users.id = support_tickets.user_id")
	if status != "" {
		q = q.Where("support_tickets.status = ?", status)
	}
	err := q.Order("support_tickets.updated_at DESC").Limit(limit).Scan(&rows).Error
	return rows, err
}

func CountUnclosedSupportTickets(userId int) (int64, error) {
	var n int64
	err := DB.Model(&SupportTicket{}).Where("user_id = ? AND status <> ?", userId, SupportStatusClosed).Count(&n).Error
	return n, err
}

// CountCustomerSupportMessagesSince counts what a customer has written, so a
// script cannot flood the inbox (or the operator's mailbox) from one account.
func CountCustomerSupportMessagesSince(userId int, since int64) (int64, error) {
	var n int64
	err := DB.Model(&SupportMessage{}).Where("user_id = ? AND from_staff = ? AND created_at >= ?", userId, false, since).Count(&n).Error
	return n, err
}

// SupportStaffEmails is who gets told about a new customer message: every
// enabled admin or root account with an email. No setting to configure.
func SupportStaffEmails() ([]string, error) {
	var emails []string
	err := DB.Model(&User{}).
		Where("role >= ? AND status = ? AND email <> ''", common.RoleAdminUser, common.UserStatusEnabled).
		Pluck("email", &emails).Error
	return emails, err
}
