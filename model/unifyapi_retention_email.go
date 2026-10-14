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

// UNIFYAPI-FORK: the weekly retention email (operator decision 2026-10-09:
// keep registered customers aware of the platform). Persistence only; the
// schedule and the message live in service/unifyapi_retention_email.go.
//
// Three tables, all ours, migrated by MigrateRetentionEmail rather than the
// shared AutoMigrate list in main.go so the upstream file stays untouched:
//   - unsubscribes: one row per user who opted out, never sent to again;
//   - sends: one row per (week, user), claimed BEFORE the send, so a restart
//     mid-run can lose at most one email but can never send one twice;
//   - weeks: the model catalogue as it stood when the week ran, so "new this
//     week" is a diff of two observed lists and not a claim anyone wrote.

import (
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type RetentionEmailUnsubscribe struct {
	UserId    int   `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	CreatedAt int64 `json:"created_at" gorm:"bigint"`
}

type RetentionEmailSend struct {
	Id     int    `json:"id"`
	Week   string `json:"week" gorm:"type:varchar(10);uniqueIndex:idx_retention_email_week_user"`
	UserId int    `json:"user_id" gorm:"uniqueIndex:idx_retention_email_week_user"`
	SentAt int64  `json:"sent_at" gorm:"bigint"`
	Error  string `json:"error" gorm:"type:varchar(255)"`
}

type RetentionEmailWeek struct {
	Week      string `json:"week" gorm:"primaryKey;type:varchar(10)"`
	Models    string `json:"models" gorm:"type:text"` // newline-separated, sorted
	CreatedAt int64  `json:"created_at" gorm:"bigint"`
}

func MigrateRetentionEmail() error {
	return DB.AutoMigrate(&RetentionEmailUnsubscribe{}, &RetentionEmailSend{}, &RetentionEmailWeek{})
}

// RetentionRecipients is every customer who should get this week's email:
// an enabled, non-deleted, non-staff login with an email address, that has
// not opted out and has not already been claimed for this week. Staff
// (role >= admin) are excluded the same way the tenant list excludes them.
// Disabled accounts -- which include the 2026-09-28 fake-signup wave -- are
// never mailed.
func RetentionRecipients(week string) ([]User, error) {
	var users []User
	err := DB.Model(&User{}).
		Where("status = ? AND role < ? AND email <> ''", common.UserStatusEnabled, common.RoleAdminUser).
		Where("id NOT IN (?)", DB.Model(&RetentionEmailUnsubscribe{}).Select("user_id")).
		Where("id NOT IN (?)", DB.Model(&RetentionEmailSend{}).Select("user_id").Where("week = ?", week)).
		Order("id").
		Find(&users).Error
	return users, err
}

// ClaimRetentionSend records that this week's email to userId is about to go
// out. It returns false when another run already claimed it.
func ClaimRetentionSend(week string, userId int) (bool, error) {
	res := DB.Clauses(clause.OnConflict{DoNothing: true}).
		Create(&RetentionEmailSend{Week: week, UserId: userId, SentAt: time.Now().Unix()})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

func RecordRetentionSendError(week string, userId int, sendErr error) {
	msg := sendErr.Error()
	if len(msg) > 255 {
		msg = msg[:255]
	}
	DB.Model(&RetentionEmailSend{}).Where("week = ? AND user_id = ?", week, userId).Update("error", msg)
}

// UnsubscribeRetentionEmail is idempotent: a second click is not an error.
func UnsubscribeRetentionEmail(userId int) error {
	return DB.Clauses(clause.OnConflict{DoNothing: true}).
		Create(&RetentionEmailUnsubscribe{UserId: userId, CreatedAt: time.Now().Unix()}).Error
}

func IsRetentionEmailUnsubscribed(userId int) bool {
	var n int64
	DB.Model(&RetentionEmailUnsubscribe{}).Where("user_id = ?", userId).Count(&n)
	return n > 0
}

// RetentionActivity is what a user consumed in [since, now): successful
// (type=2, consume) calls and the quota they were charged.
func RetentionActivity(userId int, since int64) (requests int64, quota int64, err error) {
	var row struct {
		Requests int64
		Quota    int64
	}
	err = LOG_DB.Model(&Log{}).
		Select("COUNT(*) AS requests, COALESCE(SUM(quota), 0) AS quota").
		Where("user_id = ? AND type = ? AND created_at >= ?", userId, LogTypeConsume, since).
		Scan(&row).Error
	return row.Requests, row.Quota, err
}

// EnabledModelNames is the catalogue a customer can actually call: every model
// with at least one enabled ability. Sorted and de-duplicated.
func EnabledModelNames() ([]string, error) {
	var names []string
	err := DB.Model(&Ability{}).Where("enabled = ?", true).Distinct().Pluck("model", &names).Error
	sort.Strings(names)
	return names, err
}

// SaveRetentionWeekModels stores the catalogue for week (first write wins, so
// a rerun later in the week does not move the baseline) and returns the
// catalogue of the most recent earlier week, or nil when there is none.
func SaveRetentionWeekModels(week string, models []string) (previous []string, err error) {
	err = DB.Clauses(clause.OnConflict{DoNothing: true}).
		Create(&RetentionEmailWeek{Week: week, Models: strings.Join(models, "\n"), CreatedAt: time.Now().Unix()}).Error
	if err != nil {
		return nil, err
	}
	var prev RetentionEmailWeek
	err = DB.Where("week < ?", week).Order("week DESC").First(&prev).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if prev.Models == "" {
		return []string{}, nil
	}
	return strings.Split(prev.Models, "\n"), nil
}
