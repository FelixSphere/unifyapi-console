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
	"fmt"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// Tenant lifecycle actions available to operators: suspend, resume, and extend
// the paid term.
//
// How suspension is enforced, and why: setting Tenant.Status alone would change
// nothing, because the relay authorises a request from the per-user cache
// (UserBase.Status) and never reads the tenant row. Rather than reach into the
// relay hot path -- which still bills users.quota and is the riskiest code in
// the repo to touch -- suspension disables the member user rows, which the relay
// already honours, and invalidates their cache so it takes effect immediately.
//
// Consequence worth knowing: resume re-enables every member. A member who was
// individually disabled before the tenant was suspended comes back enabled.
// Recording per-member prior state would fix that; it is not worth the extra
// table until someone actually relies on per-member disable.

// SuspendTenant cuts off a tenant's API access and records why.
func SuspendTenant(tenantId int, reason string) error {
	if tenantId == 0 {
		return errors.New("tenant id is empty")
	}
	if len(reason) > 255 {
		reason = reason[:255]
	}

	var memberIds []int
	err := DB.Transaction(func(tx *gorm.DB) error {
		var tenant Tenant
		if err := tx.First(&tenant, "id = ?", tenantId).Error; err != nil {
			return err
		}

		if err := tx.Model(&Tenant{}).Where("id = ?", tenantId).Updates(map[string]any{
			"status":         TenantStatusDisabled,
			"suspended_at":   common.GetTimestamp(),
			"suspend_reason": reason,
		}).Error; err != nil {
			return err
		}

		if err := tx.Model(&User{}).Where("tenant_id = ?", tenantId).
			Pluck("id", &memberIds).Error; err != nil {
			return err
		}

		// Never disable an operator account through a tenant action. Staff are
		// tenantless so this should not match, but a promoted user could.
		return tx.Model(&User{}).
			Where("tenant_id = ? AND role < ?", tenantId, common.RoleAdminUser).
			Update("status", common.UserStatusDisabled).Error
	})
	if err != nil {
		return err
	}

	invalidateMemberCaches(memberIds)
	return nil
}

// ResumeTenant restores API access.
func ResumeTenant(tenantId int) error {
	if tenantId == 0 {
		return errors.New("tenant id is empty")
	}

	var memberIds []int
	err := DB.Transaction(func(tx *gorm.DB) error {
		var tenant Tenant
		if err := tx.First(&tenant, "id = ?", tenantId).Error; err != nil {
			return err
		}

		if err := tx.Model(&Tenant{}).Where("id = ?", tenantId).Updates(map[string]any{
			"status":         TenantStatusEnabled,
			"suspended_at":   0,
			"suspend_reason": "",
		}).Error; err != nil {
			return err
		}

		if err := tx.Model(&User{}).Where("tenant_id = ?", tenantId).
			Pluck("id", &memberIds).Error; err != nil {
			return err
		}

		return tx.Model(&User{}).
			Where("tenant_id = ? AND role < ?", tenantId, common.RoleAdminUser).
			Update("status", common.UserStatusEnabled).Error
	})
	if err != nil {
		return err
	}

	invalidateMemberCaches(memberIds)
	return nil
}

// invalidateMemberCaches drops the per-user cache so a status change takes
// effect on the next request instead of after the cache TTL.
func invalidateMemberCaches(memberIds []int) {
	for _, id := range memberIds {
		if err := InvalidateUserCache(id); err != nil {
			common.SysLog(fmt.Sprintf("failed to invalidate user cache %d: %s", id, err.Error()))
		}
	}
}

// ExtendTenantTerm pushes ExpiresAt out by days. Extending an already-expired or
// unset term starts from now rather than from the stale date, so an operator
// renewing a lapsed account gets a full term instead of one that is already
// partly consumed. Pass a negative value to shorten.
func ExtendTenantTerm(tenantId int, days int) (int64, error) {
	if tenantId == 0 {
		return 0, errors.New("tenant id is empty")
	}
	if days == 0 {
		return 0, errors.New("days must not be zero")
	}

	var newExpiry int64
	err := DB.Transaction(func(tx *gorm.DB) error {
		var tenant Tenant
		if err := tx.First(&tenant, "id = ?", tenantId).Error; err != nil {
			return err
		}

		now := common.GetTimestamp()
		base := tenant.ExpiresAt
		if base < now {
			base = now
		}
		newExpiry = base + int64(days)*86400
		if newExpiry < 0 {
			newExpiry = 0
		}

		return tx.Model(&Tenant{}).Where("id = ?", tenantId).
			Update("expires_at", newExpiry).Error
	})
	if err != nil {
		return 0, err
	}
	return newExpiry, nil
}

// SetTenantExpiry sets the term end explicitly. 0 makes it open-ended.
func SetTenantExpiry(tenantId int, expiresAt int64) error {
	if tenantId == 0 {
		return errors.New("tenant id is empty")
	}
	if expiresAt < 0 {
		return errors.New("expiry must not be negative")
	}
	return DB.Model(&Tenant{}).Where("id = ?", tenantId).
		Update("expires_at", expiresAt).Error
}

// ---------------------------------------------------------------------------
// Payments and audit trail
// ---------------------------------------------------------------------------

// TenantPayment is one payment made by any member of a tenant.
type TenantPayment struct {
	Id              int     `json:"id"`
	UserId          int     `json:"user_id"`
	Username        string  `json:"username"`
	Amount          int     `json:"amount"`
	Money           float64 `json:"money"`
	TradeNo         string  `json:"trade_no"`
	PaymentMethod   string  `json:"payment_method"`
	PaymentProvider string  `json:"payment_provider"`
	Status          string  `json:"status"`
	CreateTime      int64   `json:"create_time"`
}

// GetTenantPayments returns the tenant's payment history, newest first, by
// joining top-ups through membership. Ordered on the topup id rather than a
// timestamp column so the query does not depend on which time fields the
// upstream TopUp model happens to populate.
func GetTenantPayments(tenantId int, limit int) ([]*TenantPayment, error) {
	if tenantId == 0 {
		return nil, errors.New("tenant id is empty")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	var payments []*TenantPayment
	err := DB.Table("top_ups").
		Select(`top_ups.id as id,
			top_ups.user_id as user_id,
			users.username as username,
			top_ups.amount as amount,
			top_ups.money as money,
			top_ups.trade_no as trade_no,
			top_ups.payment_method as payment_method,
			top_ups.payment_provider as payment_provider,
			top_ups.status as status,
			top_ups.create_time as create_time`).
		Joins("join users on users.id = top_ups.user_id").
		Where("top_ups.tenant_id = ? OR (top_ups.tenant_id = 0 AND users.tenant_id = ?)", tenantId, tenantId).
		Order("top_ups.id desc").
		Limit(limit).
		Find(&payments).Error
	if err != nil {
		return nil, err
	}
	return payments, nil
}

// TenantAuditEntry is one non-consumption event on a tenant's account.
type TenantAuditEntry struct {
	Id        int    `json:"id"`
	UserId    int    `json:"user_id"`
	Username  string `json:"username"`
	Type      int    `json:"type"`
	Content   string `json:"content"`
	Ip        string `json:"ip"`
	CreatedAt int64  `json:"created_at"`
	Other     string `json:"other"`
}

// tenantAuditLogTypes is everything that happened *to* the account, as opposed
// to what it spent. LogTypeConsume is deliberately excluded -- per-request
// billing rows belong in the usage view and would bury the audit trail.
var tenantAuditLogTypes = []int{
	LogTypeTopup,
	LogTypeManage,
	LogTypeSystem,
	LogTypeError,
	LogTypeRefund,
	LogTypeLogin,
}

// GetTenantAuditLog returns the tenant's audit trail, newest first.
func GetTenantAuditLog(tenantId int, limit int) ([]*TenantAuditEntry, error) {
	if tenantId == 0 {
		return nil, errors.New("tenant id is empty")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	var entries []*TenantAuditEntry
	err := DB.Table("logs").
		Select(`logs.id as id,
			logs.user_id as user_id,
			logs.username as username,
			logs.type as type,
			logs.content as content,
			logs.ip as ip,
			logs.created_at as created_at,
			logs.other as other`).
		Joins("join users on users.id = logs.user_id").
		Where("users.tenant_id = ?", tenantId).
		Where("logs.type in ?", tenantAuditLogTypes).
		Order("logs.id desc").
		Limit(limit).
		Find(&entries).Error
	if err != nil {
		return nil, err
	}
	return entries, nil
}
