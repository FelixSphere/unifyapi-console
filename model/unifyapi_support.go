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
	"regexp"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	SupportStatusOpen     = "open"
	SupportStatusAnswered = "answered"
	SupportStatusClosed   = "closed"
)

var (
	ErrSupportTicketNotFound    = errors.New("conversation not found")
	ErrSupportConversationStale = errors.New("conversation changed while reply was pending")
)

type SupportTicket struct {
	Id        int    `json:"id"`
	UserId    int    `json:"user_id" gorm:"index"`
	Subject   string `json:"subject" gorm:"type:varchar(200)"`
	Status    string `json:"status" gorm:"type:varchar(16);index"`
	CreatedAt int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt int64  `json:"updated_at" gorm:"bigint;index"`
	// HandledMessageId is the newest customer message that has been dealt
	// with -- answered by the AI agent or announced to staff. With the agent
	// on, staff are emailed only for messages the agent did not handle, and
	// this is how the safety sweep tells the two apart.
	HandledMessageId int `json:"-" gorm:"default:0"`
}

type SupportMessage struct {
	Id        int  `json:"id"`
	TicketId  int  `json:"ticket_id" gorm:"index"`
	UserId    int  `json:"user_id"`
	FromStaff bool `json:"from_staff"`
	// FromAI marks a reply written by the AI support agent. It is also
	// FromStaff, because for whose-turn-it-is and unread counting the agent
	// speaks for the team.
	FromAI    bool   `json:"from_ai" gorm:"default:false"`
	Body      string `json:"body" gorm:"type:text"`
	CreatedAt int64  `json:"created_at" gorm:"bigint"`
}

// SupportTicketRow is a ticket with who asked, for the operator inbox.
type SupportTicketRow struct {
	SupportTicket
	Username string `json:"username"`
	Email    string `json:"email"`
}

// SupportTicketRead is how far one person has read one conversation. Unread
// is per person, not per ticket: two operators share the inbox, and one of
// them opening a conversation must not clear it for the other.
type SupportTicketRead struct {
	UserId            int `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	TicketId          int `json:"ticket_id" gorm:"primaryKey;autoIncrement:false"`
	LastReadMessageId int `json:"last_read_message_id"`
}

func MigrateSupport() error {
	return DB.AutoMigrate(&SupportTicket{}, &SupportMessage{}, &SupportTicketRead{})
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
		var ticket SupportTicket
		if err := lockForUpdate(tx).First(&ticket, ticketId).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrSupportTicketNotFound
			}
			return err
		}
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

// MarkSupportTicketRead records that userId has seen every message in the
// ticket up to now. It only ever moves forward.
func MarkSupportTicketRead(userId, ticketId int) error {
	var last int
	if err := DB.Model(&SupportMessage{}).Where("ticket_id = ?", ticketId).
		Select("COALESCE(MAX(id), 0)").Scan(&last).Error; err != nil {
		return err
	}
	if last == 0 {
		return nil
	}
	read := SupportTicketRead{UserId: userId, TicketId: ticketId}
	if err := DB.Where(read).Attrs(SupportTicketRead{LastReadMessageId: 0}).FirstOrCreate(&read).Error; err != nil {
		return err
	}
	return DB.Model(&SupportTicketRead{}).
		Where("user_id = ? AND ticket_id = ? AND last_read_message_id < ?", userId, ticketId, last).
		Update("last_read_message_id", last).Error
}

// UnreadSupportTicketIds lists the conversations with a message from the
// other side that userId has not opened since it arrived. Staff count
// customer messages in every conversation; a customer counts staff replies in
// their own.
func UnreadSupportTicketIds(userId int, staff bool) ([]int, error) {
	q := DB.Table("support_messages AS m").
		Select("DISTINCT m.ticket_id").
		Joins("JOIN support_tickets AS t ON t.id = m.ticket_id").
		Joins("LEFT JOIN support_ticket_reads AS r ON r.ticket_id = m.ticket_id AND r.user_id = ?", userId).
		Where("m.from_staff = ? AND m.id > COALESCE(r.last_read_message_id, 0)", !staff)
	if !staff {
		q = q.Where("t.user_id = ?", userId)
	}
	var ids []int
	err := q.Pluck("m.ticket_id", &ids).Error
	return ids, err
}

// AddSupportAIMessage records the AI agent's reply. The conversation becomes
// "answered" (the customer's turn) unless the agent handed it to a person, in
// which case it stays "open" so it shows under Needs reply. Either way the
// customer message it replied to is marked handled.
func AddSupportAIMessage(ticketId int, body string, handoff bool, handledMessageId int) (*SupportMessage, error) {
	now := time.Now().Unix()
	msg := &SupportMessage{TicketId: ticketId, FromStaff: true, FromAI: true, Body: body, CreatedAt: now}
	status := SupportStatusAnswered
	if handoff {
		status = SupportStatusOpen
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		var ticket SupportTicket
		if err := lockForUpdate(tx).First(&ticket, ticketId).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrSupportTicketNotFound
			}
			return err
		}
		if ticket.Status != SupportStatusOpen {
			return ErrSupportConversationStale
		}

		var latestMessageId int
		if err := tx.Model(&SupportMessage{}).Where("ticket_id = ?", ticketId).
			Select("COALESCE(MAX(id), 0)").Scan(&latestMessageId).Error; err != nil {
			return err
		}
		if latestMessageId != handledMessageId {
			return ErrSupportConversationStale
		}

		if err := tx.Create(msg).Error; err != nil {
			return err
		}
		return tx.Model(&SupportTicket{}).Where("id = ?", ticketId).
			Updates(map[string]any{"status": status, "updated_at": now, "handled_message_id": handledMessageId}).Error
	})
	return msg, err
}

// MarkSupportMessageHandled records that a customer message was dealt with
// (staff were told about it), without changing whose turn it is.
func MarkSupportMessageHandled(ticketId, messageId int) error {
	return DB.Model(&SupportTicket{}).Where("id = ? AND handled_message_id < ?", ticketId, messageId).
		Update("handled_message_id", messageId).Error
}

// UnhandledSupportMessage is a conversation whose newest message is the
// customer's and has been neither answered by the agent nor announced to staff.
type UnhandledSupportMessage struct {
	TicketId  int
	MessageId int
}

// UnhandledSupportMessages finds customer messages older than the cutoff that
// nobody has dealt with -- the safety net for when the AI agent never ran
// (the customer closed the tab before asking) or failed.
func UnhandledSupportMessages(olderThan int64, limit int) ([]UnhandledSupportMessage, error) {
	var rows []UnhandledSupportMessage
	err := DB.Raw(`
		SELECT t.id AS ticket_id, m.id AS message_id
		FROM support_tickets t
		JOIN support_messages m ON m.id = (SELECT MAX(id) FROM support_messages WHERE ticket_id = t.id)
		WHERE m.from_staff = ? AND m.created_at < ? AND m.id > t.handled_message_id AND t.status <> ?
		ORDER BY m.id LIMIT ?`, false, olderThan, SupportStatusClosed, limit).Scan(&rows).Error
	return rows, err
}

// SupportAccountSummary is what the AI agent may read about the customer it
// is talking to: their balance and their own recent failures. Error text is
// reduced to a status code, because upstream error strings can name suppliers
// and internal routing groups.
type SupportAccountSummary struct {
	BalanceUSD         float64              `json:"balance_usd"`
	FailedRequests24h  int                  `json:"failed_requests_24h"`
	FailuresByStatus   map[string]int       `json:"failures_by_status_24h"`
	RecentFailures     []SupportRequestInfo `json:"recent_failures"`
	SuccessfulCalls24h int64                `json:"successful_requests_24h"`
}

// SupportRequestInfo is one of the customer's own requests, as the agent sees it.
type SupportRequestInfo struct {
	RequestId  string  `json:"request_id"`
	At         int64   `json:"at"`
	Model      string  `json:"model"`
	Outcome    string  `json:"outcome"` // "success" | "error"
	StatusCode string  `json:"status_code,omitempty"`
	Seconds    int     `json:"seconds"`
	Stream     bool    `json:"stream"`
	CostUSD    float64 `json:"cost_usd,omitempty"`
}

var statusCodeRe = regexp.MustCompile(`status_code=(\d{3})`)

func supportRequestInfo(l Log) SupportRequestInfo {
	info := SupportRequestInfo{RequestId: l.RequestId, At: l.CreatedAt, Model: l.ModelName, Seconds: l.UseTime, Stream: l.IsStream}
	if l.Type == LogTypeError {
		info.Outcome = "error"
		if m := statusCodeRe.FindStringSubmatch(l.Content); m != nil {
			info.StatusCode = m[1]
		}
	} else {
		info.Outcome = "success"
		info.CostUSD = float64(l.Quota) / common.QuotaPerUnit
	}
	return info
}

func GetSupportAccountSummary(userId int) (*SupportAccountSummary, error) {
	balance, err := GetUserQuota(userId, true)
	if err != nil {
		return nil, err
	}
	since := time.Now().Add(-24 * time.Hour).Unix()
	var failures []Log
	if err := LOG_DB.Select("request_id", "created_at", "type", "model_name", "content", "use_time", "is_stream").
		Where("user_id = ? AND type = ? AND created_at >= ?", userId, LogTypeError, since).
		Order("id desc").Limit(200).Find(&failures).Error; err != nil {
		return nil, err
	}
	var ok int64
	if err := LOG_DB.Model(&Log{}).Where("user_id = ? AND type = ? AND created_at >= ?", userId, LogTypeConsume, since).
		Count(&ok).Error; err != nil {
		return nil, err
	}
	sum := &SupportAccountSummary{
		BalanceUSD:         float64(balance) / common.QuotaPerUnit,
		FailedRequests24h:  len(failures),
		FailuresByStatus:   map[string]int{},
		SuccessfulCalls24h: ok,
		RecentFailures:     []SupportRequestInfo{},
	}
	for i, l := range failures {
		info := supportRequestInfo(l)
		code := info.StatusCode
		if code == "" {
			code = "unknown"
		}
		sum.FailuresByStatus[code]++
		if i < 5 {
			sum.RecentFailures = append(sum.RecentFailures, info)
		}
	}
	return sum, nil
}

// GetSupportRequestInfo returns one request only if it belongs to userId.
func GetSupportRequestInfo(userId int, requestId string) (*SupportRequestInfo, error) {
	var l Log
	err := LOG_DB.Select("request_id", "created_at", "type", "model_name", "content", "use_time", "is_stream", "quota").
		Where("request_id = ? AND user_id = ? AND type IN ?", requestId, userId, []int{LogTypeConsume, LogTypeError}).
		Order("id desc").First(&l).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	info := supportRequestInfo(l)
	return &info, nil
}
