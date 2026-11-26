/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

// UNIFYAPI-FORK: email an admin writes to one customer or to several, from
// the Users page (operator request 2026-11-23: "点击用户名，用 email 给他发
// 信息 ... 模板要能批量发送"). Persistence only; rendering, the queue and the
// throttle live in service/unifyapi_admin_email.go.
//
// Two tables, ours, migrated at boot by MigrateAdminEmail:
//   - admin_email_templates: reusable messages admins edit in the console.
//     Two built-ins are seeded once each; soft delete means a built-in an
//     admin deleted stays deleted across restarts instead of coming back.
//   - admin_email_sends: one row per recipient per send, written BEFORE the
//     message is queued, so every attempt -- skipped ones included -- has a
//     record of who sent it, when, from which template, and what happened.

import (
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	AdminEmailStatusQueued      = "queued"
	AdminEmailStatusSent        = "sent"
	AdminEmailStatusFailed      = "failed"
	AdminEmailStatusSkipped     = "skipped"
	AdminEmailStatusInterrupted = "interrupted"
)

type AdminEmailTemplate struct {
	Id         int            `json:"id"`
	BuiltinKey string         `json:"builtin_key" gorm:"type:varchar(32);index"`
	Name       string         `json:"name" gorm:"type:varchar(100)"`
	Subject    string         `json:"subject" gorm:"type:varchar(255)"`
	Headline   string         `json:"headline" gorm:"type:varchar(255)"`
	Body       string         `json:"body" gorm:"type:text"`
	CTALabel   string         `json:"cta_label" gorm:"column:cta_label;type:varchar(100)"`
	CTAURL     string         `json:"cta_url" gorm:"column:cta_url;type:varchar(512)"`
	CreatedBy  int            `json:"created_by"`
	UpdatedBy  int            `json:"updated_by"`
	CreatedAt  int64          `json:"created_at" gorm:"bigint"`
	UpdatedAt  int64          `json:"updated_at" gorm:"bigint"`
	DeletedAt  gorm.DeletedAt `json:"-" gorm:"index"`
}

type AdminEmailSend struct {
	Id         int    `json:"id"`
	BatchId    string `json:"batch_id" gorm:"type:varchar(32);index"`
	SenderId   int    `json:"sender_id" gorm:"index"`
	TemplateId int    `json:"template_id"`
	Subject    string `json:"subject" gorm:"type:varchar(255)"`
	UserId     int    `json:"user_id" gorm:"index"`
	Email      string `json:"email" gorm:"type:varchar(255)"`
	Status     string `json:"status" gorm:"type:varchar(16);index"`
	Error      string `json:"error" gorm:"type:varchar(255)"`
	CreatedAt  int64  `json:"created_at" gorm:"bigint;index"`
	SentAt     int64  `json:"sent_at" gorm:"bigint"`
}

// MigrateAdminEmail creates both tables and seeds the built-in templates.
// Safe to run on every boot.
func MigrateAdminEmail() error {
	if err := DB.AutoMigrate(&AdminEmailTemplate{}, &AdminEmailSend{}); err != nil {
		return err
	}
	return seedAdminEmailTemplates()
}

// seedAdminEmailTemplates inserts each built-in whose key has never existed,
// counting soft-deleted rows, so an admin's edit or delete is never undone.
func seedAdminEmailTemplates() error {
	now := time.Now().Unix()
	for _, builtin := range BuiltinAdminEmailTemplates() {
		var n int64
		if err := DB.Unscoped().Model(&AdminEmailTemplate{}).Where("builtin_key = ?", builtin.BuiltinKey).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		row := builtin
		row.CreatedAt, row.UpdatedAt = now, now
		if err := DB.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func ListAdminEmailTemplates() ([]AdminEmailTemplate, error) {
	var rows []AdminEmailTemplate
	err := DB.Order("id asc").Find(&rows).Error
	return rows, err
}

func GetAdminEmailTemplate(id int) (*AdminEmailTemplate, error) {
	var row AdminEmailTemplate
	if err := DB.First(&row, id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func CreateAdminEmailTemplate(row *AdminEmailTemplate) error {
	now := time.Now().Unix()
	row.Id, row.BuiltinKey = 0, ""
	row.CreatedAt, row.UpdatedAt = now, now
	row.UpdatedBy = row.CreatedBy
	return DB.Create(row).Error
}

// UpdateAdminEmailTemplate writes the editable fields only; who created the
// row and its built-in key are not the editor's to change.
func UpdateAdminEmailTemplate(row *AdminEmailTemplate) error {
	result := DB.Model(&AdminEmailTemplate{}).Where("id = ?", row.Id).Updates(map[string]any{
		"name":       row.Name,
		"subject":    row.Subject,
		"headline":   row.Headline,
		"body":       row.Body,
		"cta_label":  row.CTALabel,
		"cta_url":    row.CTAURL,
		"updated_by": row.UpdatedBy,
		"updated_at": time.Now().Unix(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func DeleteAdminEmailTemplate(id int) error {
	result := DB.Delete(&AdminEmailTemplate{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// AdminEmailRecipients loads the users an admin picked, with only the fields
// a message needs. Ids that match no (undeleted) user are simply absent.
func AdminEmailRecipients(ids []int) ([]User, error) {
	var users []User
	if len(ids) == 0 {
		return users, nil
	}
	err := DB.Where("id IN ?", ids).Find(&users).Error
	return users, err
}

// AdminEmailUnsubscribed is the subset of ids that opted out of product mail
// (the weekly update's list, shared by every bulk email we send).
func AdminEmailUnsubscribed(ids []int) (map[int]bool, error) {
	out := map[int]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []RetentionEmailUnsubscribe
	if err := DB.Where("user_id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.UserId] = true
	}
	return out, nil
}

// CreateAdminEmailSends records a whole send at once, so either every
// recipient has a row or none does and nothing is queued.
func CreateAdminEmailSends(rows []AdminEmailSend) error {
	if len(rows) == 0 {
		return nil
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		return tx.CreateInBatches(rows, 200).Error
	})
}

// FinishAdminEmailSend records what the mail server said about one queued
// message. Only a queued row moves, so a late worker cannot overwrite a row
// the boot sweep already marked interrupted.
func FinishAdminEmailSend(id int, sendErr error) error {
	updates := map[string]any{"status": AdminEmailStatusSent, "sent_at": time.Now().Unix(), "error": ""}
	if sendErr != nil {
		updates = map[string]any{"status": AdminEmailStatusFailed, "error": truncateAdminEmailError(sendErr.Error())}
	}
	return DB.Model(&AdminEmailSend{}).Where("id = ? AND status = ?", id, AdminEmailStatusQueued).Updates(updates).Error
}

// InterruptStaleAdminEmailSends marks queued rows older than before as
// interrupted. The queue is in memory, so a row still queued after a restart
// was never handed to the mail server -- or was, and the result was lost.
// Either way it is not re-sent automatically: a duplicate email to a
// customer is worse than a visible "interrupted" an admin can act on.
func InterruptStaleAdminEmailSends(before int64) (int64, error) {
	result := DB.Model(&AdminEmailSend{}).
		Where("status = ? AND created_at < ?", AdminEmailStatusQueued, before).
		Updates(map[string]any{"status": AdminEmailStatusInterrupted, "error": "the server restarted before this was sent; not retried"})
	return result.RowsAffected, result.Error
}

// ListAdminEmailSends is the send history, newest first, optionally for one
// recipient or one batch.
func ListAdminEmailSends(userId int, batchId string, limit int) ([]AdminEmailSend, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := DB.Model(&AdminEmailSend{})
	if userId > 0 {
		q = q.Where("user_id = ?", userId)
	}
	if batchId != "" {
		q = q.Where("batch_id = ?", batchId)
	}
	var rows []AdminEmailSend
	err := q.Order("id desc").Limit(limit).Find(&rows).Error
	return rows, err
}

func truncateAdminEmailError(s string) string {
	if len(s) > 255 {
		return s[:252] + "..."
	}
	return s
}

// NewAdminEmailBatchId names one send; every recipient row carries it.
func NewAdminEmailBatchId() string {
	return common.GetRandomString(16)
}
