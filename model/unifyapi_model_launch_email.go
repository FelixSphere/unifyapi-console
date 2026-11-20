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

// UNIFYAPI-FORK: the new-model announcement email (operator decision
// 2026-11-18: "我们对于新上架的模型，应该有营销类的宣传手段，比如有email").
// Persistence only; the message and the send loop live in
// service/unifyapi_model_launch_email.go.
//
// Two tables, ours, migrated at boot by MigrateModelLaunchEmail:
//   - launches: one row per announcement an operator approved, with the
//     models it named and how the send went;
//   - sends: one row per (launch, user), claimed BEFORE the send, so a
//     restart mid-run can lose at most one email but never send one twice.
//
// It shares the weekly update's opt-out list on purpose: a customer who
// unsubscribed from product mail must not start receiving it again because
// a second kind of product mail was invented.

import (
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ModelLaunchEmail struct {
	Id         int    `json:"id"`
	Models     string `json:"models" gorm:"type:text"` // newline-separated, sorted
	Note       string `json:"note" gorm:"type:text"`
	CreatedBy  int    `json:"created_by"`
	CreatedAt  int64  `json:"created_at" gorm:"bigint"`
	Recipients int    `json:"recipients"`
	Sent       int    `json:"sent"`
	Failed     int    `json:"failed"`
	FinishedAt int64  `json:"finished_at" gorm:"bigint"`
	// Queued marks a launch written straight into this table (the catalogue
	// automation inserts the row over SQL; operator decision 2026-11-19:
	// "上架邮件也改成自动发"). The queue worker sends it once it is
	// ModelLaunchQueueDelay old; deleting the row before then cancels it.
	Queued bool `json:"queued" gorm:"default:false"`
	// Error is why a queued launch was refused (model disabled, unpriced,
	// already announced). Empty for launches that were sent.
	Error string `json:"error" gorm:"type:varchar(255)"`
}

type ModelLaunchEmailSend struct {
	Id       int    `json:"id"`
	LaunchId int    `json:"launch_id" gorm:"uniqueIndex:idx_model_launch_send"`
	UserId   int    `json:"user_id" gorm:"uniqueIndex:idx_model_launch_send"`
	SentAt   int64  `json:"sent_at" gorm:"bigint"`
	Error    string `json:"error" gorm:"type:varchar(255)"`
}

func MigrateModelLaunchEmail() error {
	return DB.AutoMigrate(&ModelLaunchEmail{}, &ModelLaunchEmailSend{})
}

func (l *ModelLaunchEmail) ModelList() []string {
	if l.Models == "" {
		return nil
	}
	return strings.Split(l.Models, "\n")
}

// CreateModelLaunchEmail records an approved announcement. The model list is
// stored sorted so two launches of the same set compare equal.
func CreateModelLaunchEmail(models []string, note string, createdBy int) (*ModelLaunchEmail, error) {
	sorted := append([]string(nil), models...)
	sort.Strings(sorted)
	l := &ModelLaunchEmail{
		Models:    strings.Join(sorted, "\n"),
		Note:      note,
		CreatedBy: createdBy,
		CreatedAt: time.Now().Unix(),
	}
	return l, DB.Create(l).Error
}

func FinishModelLaunchEmail(id, recipients, sent, failed int) error {
	return DB.Model(&ModelLaunchEmail{}).Where("id = ?", id).Updates(map[string]any{
		"recipients": recipients, "sent": sent, "failed": failed, "finished_at": time.Now().Unix(),
	}).Error
}

// DueQueuedModelLaunches is every queued launch created at or before cutoff,
// oldest first.
func DueQueuedModelLaunches(cutoff int64) ([]ModelLaunchEmail, error) {
	var rows []ModelLaunchEmail
	err := DB.Where("queued = ? AND finished_at = ? AND created_at <= ?", true, 0, cutoff).Order("id").Find(&rows).Error
	return rows, err
}

// TakeQueuedModelLaunch clears the queued mark and reports whether this
// caller was the one that cleared it, so two workers cannot send one launch.
func TakeQueuedModelLaunch(id int) (bool, error) {
	res := DB.Model(&ModelLaunchEmail{}).Where("id = ? AND queued = ?", id, true).Update("queued", false)
	return res.RowsAffected == 1, res.Error
}

// RefuseModelLaunch closes a launch that will not be sent, with the reason.
func RefuseModelLaunch(id int, reason string) error {
	if len(reason) > 255 {
		reason = reason[:255]
	}
	return DB.Model(&ModelLaunchEmail{}).Where("id = ?", id).Updates(map[string]any{
		"queued": false, "error": reason, "finished_at": time.Now().Unix(),
	}).Error
}

func ListModelLaunchEmails(limit int) ([]ModelLaunchEmail, error) {
	var rows []ModelLaunchEmail
	err := DB.Order("id DESC").Limit(limit).Find(&rows).Error
	return rows, err
}

// AnnouncedModels is every model an earlier launch email actually told
// someone about: a launch counts only if at least one of its sends has no
// error. A launch whose every send failed (SMTP down when Send was pressed)
// or that had no recipients told nobody, so its models can be announced
// again. Claims are written before each send with an empty error, so a
// launch still in flight counts from its first claim.
func AnnouncedModels() (map[string]bool, error) {
	var rows []ModelLaunchEmail
	delivered := DB.Model(&ModelLaunchEmailSend{}).Select("launch_id").Where("error = ? OR error IS NULL", "")
	if err := DB.Select("models").Where("id IN (?)", delivered).Find(&rows).Error; err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for i := range rows {
		for _, m := range rows[i].ModelList() {
			seen[m] = true
		}
	}
	return seen, nil
}

// marketingRecipients is the same audience as the weekly update: enabled,
// non-staff logins with an email address that have not opted out. Disabled
// accounts -- which include the 2026-09-28 fake-signup wave -- are never
// mailed.
func marketingRecipients() *gorm.DB {
	return DB.Model(&User{}).
		Where("status = ? AND role < ? AND email <> ''", common.UserStatusEnabled, common.RoleAdminUser).
		Where("id NOT IN (?)", DB.Model(&RetentionEmailUnsubscribe{}).Select("user_id"))
}

// CountMarketingRecipients is how many people an announcement would reach.
func CountMarketingRecipients() (int64, error) {
	var n int64
	err := marketingRecipients().Count(&n).Error
	return n, err
}

// ModelLaunchRecipients is everyone launch id has not yet been claimed for.
func ModelLaunchRecipients(launchId int) ([]User, error) {
	var users []User
	err := marketingRecipients().
		Where("id NOT IN (?)", DB.Model(&ModelLaunchEmailSend{}).Select("user_id").Where("launch_id = ?", launchId)).
		Order("id").
		Find(&users).Error
	return users, err
}

// ClaimModelLaunchSend records that launch id's email to userId is about to
// go out. It returns false when another run already claimed it.
func ClaimModelLaunchSend(launchId, userId int) (bool, error) {
	res := DB.Clauses(clause.OnConflict{DoNothing: true}).
		Create(&ModelLaunchEmailSend{LaunchId: launchId, UserId: userId, SentAt: time.Now().Unix()})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

func RecordModelLaunchSendError(launchId, userId int, sendErr error) {
	msg := sendErr.Error()
	if len(msg) > 255 {
		msg = msg[:255]
	}
	DB.Model(&ModelLaunchEmailSend{}).Where("launch_id = ? AND user_id = ?", launchId, userId).Update("error", msg)
}

// LatestRetentionWeekModels is the catalogue as the most recent weekly
// update saw it, or nil when no week has run. A model enabled since then is
// news the weekly email has not told anyone yet.
func LatestRetentionWeekModels() ([]string, error) {
	var w RetentionEmailWeek
	res := DB.Order("week DESC").Limit(1).Find(&w)
	if res.Error != nil || res.RowsAffected == 0 {
		return nil, res.Error
	}
	if w.Models == "" {
		return []string{}, nil
	}
	return strings.Split(w.Models, "\n"), nil
}
