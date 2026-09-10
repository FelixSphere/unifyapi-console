/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// BuilderIdentity links a trusted Builder subject to one personal account.
// Email is used only when provisioning; it is never an authentication lookup.
type BuilderIdentity struct {
	Id         int    `gorm:"primaryKey"`
	Subject    string `gorm:"type:varchar(128);not null;uniqueIndex"`
	UserId     int    `gorm:"not null;uniqueIndex"`
	ProgramId  int    `gorm:"not null;index"`
	CustomerId int    `gorm:"not null"`
	TokenId    int    `gorm:"not null"`
	CreatedAt  int64  `gorm:"autoCreateTime"`
}

var ErrBuilderLinkRequired = errors.New("existing account ownership verification required")
var ErrBuilderUnavailable = errors.New("builder account unavailable")

func GetBuilderIdentity(subject string) (*BuilderIdentity, *User, error) {
	var link BuilderIdentity
	if err := DB.Where("subject = ?", subject).First(&link).Error; err != nil {
		return nil, nil, err
	}
	user, err := GetUserById(link.UserId, false)
	if err != nil {
		return nil, nil, err
	}
	if user.Status != common.UserStatusEnabled || IsStaffRole(user.Role) {
		return nil, nil, ErrBuilderUnavailable
	}
	return &link, user, nil
}

// ConnectBuilderIdentity requires the caller to have verified the Builder email.
// An existing UnifyAPI account additionally requires its management credential.
func ConnectBuilderIdentity(subject, email, code, managementToken string) (*BuilderIdentity, error) {
	if link, _, err := GetBuilderIdentity(subject); err == nil {
		return link, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	var existingID int
	if managementToken != "" {
		owner, err := ValidateAccessToken(managementToken)
		if err != nil || owner == nil || NormalizeEmail(owner.Email) != NormalizeEmail(email) || owner.Status != common.UserStatusEnabled || IsStaffRole(owner.Role) {
			return nil, ErrBuilderUnavailable
		}
		existingID = owner.Id
	}
	var link BuilderIdentity
	err := DB.Transaction(func(tx *gorm.DB) error {
		return withNormalizedEmailLock(tx, email, func(tx *gorm.DB) error {
			if err := tx.Where("subject = ?", subject).First(&link).Error; err == nil {
				return nil
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			offer, err := getPartnershipOfferByCode(tx, code, true)
			if err != nil {
				return err
			}
			var user User
			if existingID > 0 {
				if err := lockForUpdate(tx).First(&user, existingID).Error; err != nil {
					return err
				}
				if user.Status != common.UserStatusEnabled || IsStaffRole(user.Role) || NormalizeEmail(user.Email) != NormalizeEmail(email) {
					return ErrBuilderUnavailable
				}
			} else {
				var count int64
				if err := emailQuery(tx, email).Count(&count).Error; err != nil {
					return err
				}
				if count > 0 {
					return ErrBuilderLinkRequired
				}
				user = User{Username: "builder_" + common.GetRandomString(12), Email: NormalizeEmail(email), DisplayName: "Builder", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: offer.CustomerGroup, Quota: 0, AffCode: common.GetRandomString(4)}
				if err := user.prepareForInsert(tx); err != nil {
					return err
				}
				if err := tx.Create(&user).Error; err != nil {
					return err
				}
				if _, err := EnsureTenantForUserTx(tx, user.Id); err != nil {
					return err
				}
			}
			var enrollment PartnershipEnrollment
			if err := tx.Where("program_id = ? AND user_id = ?", offer.Program.Id, user.Id).First(&enrollment).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				if err := tx.Create(&PartnershipEnrollment{ProgramId: offer.Program.Id, CustomerId: offer.CustomerId, CustomerGroup: offer.CustomerGroup, UserId: user.Id}).Error; err != nil {
					return err
				}
			} else if err != nil {
				return err
			}
			key, err := common.GenerateKey()
			if err != nil {
				return err
			}
			token := Token{UserId: user.Id, Key: key, Status: common.TokenStatusEnabled, Name: "Builder API", CreatedTime: time.Now().Unix(), AccessedTime: time.Now().Unix(), ExpiredTime: -1, UnlimitedQuota: true}
			if err := tx.Create(&token).Error; err != nil {
				return err
			}
			link = BuilderIdentity{Subject: subject, UserId: user.Id, ProgramId: offer.Program.Id, CustomerId: offer.CustomerId, TokenId: token.Id}
			return tx.Create(&link).Error
		})
	})
	return &link, err
}

// ReadBuilderWorkspace returns a deliberately small projection; provider logs,
// management credentials and payment identifiers never cross this boundary.
func ReadBuilderWorkspace(link *BuilderIdentity, user *User, period string) (map[string]any, error) {
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -30).Unix()
	if period == "7d" {
		since = now.AddDate(0, 0, -7).Unix()
	}
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Unix()
	if period == "current_month" {
		since = month
	}
	quota, err := GetUserQuota(user.Id, true)
	if err != nil {
		return nil, err
	}
	token, err := GetTokenByIds(link.TokenId, user.Id)
	if err != nil {
		return nil, err
	}
	var rows []Log
	if err := LOG_DB.Select("id", "created_at", "type", "model_name", "quota", "prompt_tokens", "completion_tokens", "use_time").Where("user_id = ? AND type IN ? AND created_at >= ?", user.Id, []int{LogTypeConsume, LogTypeError}, since).Order("id desc").Limit(100).Find(&rows).Error; err != nil {
		return nil, err
	}
	var totals struct {
		Requests     int64 `json:"requests"`
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	}
	if err := LOG_DB.Model(&Log{}).Select("count(*) AS requests, coalesce(sum(prompt_tokens),0) AS input_tokens, coalesce(sum(completion_tokens),0) AS output_tokens").Where("user_id = ? AND type IN ? AND created_at >= ?", user.Id, []int{LogTypeConsume, LogTypeError}, since).Scan(&totals).Error; err != nil {
		return nil, err
	}
	var monthly int64
	if err := LOG_DB.Model(&Log{}).Select("coalesce(sum(quota),0)").Where("user_id = ? AND type = ? AND created_at >= ?", user.Id, LogTypeConsume, month).Scan(&monthly).Error; err != nil {
		return nil, err
	}
	activity := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		outcome := "success"
		if row.Type == LogTypeError {
			outcome = "error"
		}
		activity = append(activity, map[string]any{"id": row.Id, "timestamp": time.Unix(row.CreatedAt, 0).UTC().Format(time.RFC3339), "model": row.ModelName, "outcome": outcome, "duration_ms": row.UseTime * 1000, "cost": float64(row.Quota) / common.QuotaPerUnit})
	}
	var topups []TopUp
	if err := DB.Where("user_id = ? AND status = ? AND payment_method = ?", user.Id, common.TopUpStatusSuccess, PaymentMethodStripe).Order("id desc").Limit(100).Find(&topups).Error; err != nil {
		return nil, err
	}
	credits := make([]map[string]any, 0, len(topups))
	for _, topup := range topups {
		credits = append(credits, map[string]any{"id": topup.Id, "amount": topup.Money, "timestamp": time.Unix(topup.CompleteTime, 0).UTC().Format(time.RFC3339), "description": "Account top-up"})
	}
	models := []string{}
	if err := DB.Table("abilities").Where(commonGroupCol+" = ? AND enabled = ?", user.Group, true).Distinct("model").Order("model").Pluck("model", &models).Error; err != nil {
		return nil, err
	}
	if models == nil {
		models = []string{}
	}
	return map[string]any{
		"connected": true, "account": map[string]any{"id": user.Id, "email": user.Email, "name": user.DisplayName, "group": user.Group},
		"balance": float64(quota) / common.QuotaPerUnit, "monthly_usage": float64(monthly) / common.QuotaPerUnit,
		"key":    map[string]any{"id": token.Id, "masked": token.GetMaskedKey(), "active": token.Status == common.TokenStatusEnabled && (token.ExpiredTime == -1 || token.ExpiredTime > now.Unix()), "created_at": time.Unix(token.CreatedTime, 0).UTC().Format(time.RFC3339)},
		"models": models, "activity": activity, "totals": totals, "credits": credits,
	}, nil
}
