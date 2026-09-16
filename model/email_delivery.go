/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Delivery outcomes recorded at send time.
//
// These describe whether the mail server ACCEPTED the message, which is not
// the same as the recipient receiving it. A message accepted here can still
// bounce minutes later or be filed as spam and never seen. Recording only what
// send time can actually observe keeps the field honest; treating "accepted"
// as "reachable" would make every dead address look healthy.
const (
	EmailDeliveryAccepted = "accepted"
	EmailDeliveryRejected = "rejected"
)

// EmailDeliveryState is the most recent send attempt per address.
//
// Keyed on the address rather than a user id: verification mail is sent before
// an account exists, the same address can outlive the account, and the
// question being answered is about the address itself.
type EmailDeliveryState struct {
	Id            int    `json:"id" gorm:"primaryKey"`
	Email         string `json:"email" gorm:"type:varchar(191);uniqueIndex;not null"`
	LastStatus    string `json:"last_status" gorm:"type:varchar(16);not null;index"`
	LastAttemptAt int64  `json:"last_attempt_at" gorm:"not null;default:0;index"`
	LastPurpose   string `json:"last_purpose" gorm:"type:varchar(64);not null;default:''"`

	// LastError is the rejection as the mail server phrased it. Kept verbatim
	// so an operator can tell "no such mailbox" from "relay denied", which need
	// completely different responses.
	LastError string `json:"last_error" gorm:"type:text"`

	Attempts   int `json:"attempts" gorm:"not null;default:0"`
	Rejections int `json:"rejections" gorm:"not null;default:0"`

	CreatedAt int64 `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt int64 `json:"updated_at" gorm:"autoUpdateTime"`
}

// Reachable reports whether the last attempt was accepted for delivery. An
// address never written to is reported as reachable: nothing has contradicted
// it, and marking untried addresses as unreachable would be a guess.
func (s *EmailDeliveryState) Reachable() bool {
	return s == nil || s.LastStatus != EmailDeliveryRejected
}

const emailDeliveryErrorLimit = 500

// RecordEmailDelivery stores the outcome of one send attempt. It never returns
// an error to its caller: recording is observability, and failing to record
// must not turn a delivered message into a failed one.
func RecordEmailDelivery(email, purpose string, sendErr error) {
	if DB == nil {
		return
	}
	email = NormalizeEmail(email)
	if email == "" {
		return
	}
	status := EmailDeliveryAccepted
	message := ""
	rejection := 0
	if sendErr != nil {
		status = EmailDeliveryRejected
		message = sendErr.Error()
		if len(message) > emailDeliveryErrorLimit {
			message = message[:emailDeliveryErrorLimit]
		}
		rejection = 1
	}
	now := time.Now().Unix()
	state := EmailDeliveryState{
		Email: email, LastStatus: status, LastAttemptAt: now,
		LastPurpose: strings.TrimSpace(purpose), LastError: message,
		Attempts: 1, Rejections: rejection,
	}
	// One upsert, so concurrent sends to the same address cannot lose a count
	// through a read-modify-write.
	_ = DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "email"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"last_status":     status,
			"last_attempt_at": now,
			"last_purpose":    state.LastPurpose,
			"last_error":      message,
			"attempts":        gorm.Expr("email_delivery_states.attempts + 1"),
			"rejections":      gorm.Expr("email_delivery_states.rejections + ?", rejection),
			"updated_at":      now,
		}),
	}).Create(&state).Error
}

// GetEmailDeliveryStates looks up many addresses at once, so an admin list can
// show reachability without a query per row.
func GetEmailDeliveryStates(emails []string) (map[string]*EmailDeliveryState, error) {
	states := map[string]*EmailDeliveryState{}
	if DB == nil || len(emails) == 0 {
		return states, nil
	}
	unique := make([]string, 0, len(emails))
	seen := map[string]bool{}
	for _, email := range emails {
		normalized := NormalizeEmail(email)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		unique = append(unique, normalized)
	}
	if len(unique) == 0 {
		return states, nil
	}
	var rows []*EmailDeliveryState
	if err := DB.Where("email IN ?", unique).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		states[row.Email] = row
	}
	return states, nil
}

// GetUnreachableEmails lists addresses whose last attempt was rejected, most
// recent first, so an operator can work through what actually needs fixing.
func GetUnreachableEmails(limit int) ([]*EmailDeliveryState, error) {
	if DB == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var rows []*EmailDeliveryState
	err := DB.Where("last_status = ?", EmailDeliveryRejected).
		Order("last_attempt_at desc").Limit(limit).Find(&rows).Error
	return rows, err
}
