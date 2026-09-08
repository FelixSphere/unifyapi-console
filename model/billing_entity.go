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
	"time"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

var ErrInsufficientBillingQuota = errors.New("billing quota is insufficient")

// BillingEntity is the row that owns spendable wallet quota for a user.
// Customer users share their tenant row; tenantless operators and legacy users
// retain upstream's per-user wallet semantics.
type BillingEntity struct {
	UserId   int
	TenantId int
}

func (e BillingEntity) IsTenant() bool { return e.TenantId > 0 }

func billingTenantCacheKey(tenantId int) string {
	return fmt.Sprintf("billing:tenant:%d", tenantId)
}

type billingQuotaCache struct {
	Quota int `json:"quota"`
}

func resolveBillingEntity(tx *gorm.DB, userId int) (BillingEntity, error) {
	if userId <= 0 {
		return BillingEntity{}, errors.New("invalid user id")
	}
	var user struct {
		Id       int
		TenantId int
	}
	if err := tx.Model(&User{}).Select("id", "tenant_id").Where("id = ?", userId).First(&user).Error; err != nil {
		return BillingEntity{}, err
	}
	return BillingEntity{UserId: user.Id, TenantId: user.TenantId}, nil
}

func ResolveBillingEntity(userId int) (BillingEntity, error) {
	if common.RedisEnabled {
		if cached, err := cacheGetUserBase(userId); err == nil {
			return BillingEntity{UserId: userId, TenantId: cached.TenantId}, nil
		}
	}
	return resolveBillingEntity(DB, userId)
}

func getBillingQuotaFromDB(tx *gorm.DB, entity BillingEntity) (int, error) {
	var quota int
	query := tx.Model(&User{}).Where("id = ?", entity.UserId)
	if entity.IsTenant() {
		query = tx.Model(&Tenant{}).Where("id = ?", entity.TenantId)
	}
	if err := query.Select("quota").Find(&quota).Error; err != nil {
		return 0, err
	}
	return quota, nil
}

func getTenantQuotaCached(tenantId int) (int, error) {
	if !common.RedisEnabled {
		return 0, errors.New("redis is not enabled")
	}
	var cached billingQuotaCache
	if err := common.RedisHGetObj(billingTenantCacheKey(tenantId), &cached); err != nil {
		return 0, err
	}
	return cached.Quota, nil
}

func cacheTenantQuota(tenantId int, quota int) error {
	if !common.RedisEnabled {
		return nil
	}
	ttl := time.Duration(common.RedisKeyCacheSeconds()) * time.Second
	if ttl <= 0 {
		ttl = time.Minute
	}
	return common.RedisHSetObj(billingTenantCacheKey(tenantId), &billingQuotaCache{Quota: quota}, ttl)
}

func invalidateBillingQuotaCache(entity BillingEntity) error {
	if !common.RedisEnabled {
		return nil
	}
	if entity.IsTenant() {
		return common.RedisDelKey(billingTenantCacheKey(entity.TenantId))
	}
	return invalidateUserCache(entity.UserId)
}

func InvalidateBillingQuotaCacheForUser(userId int) error {
	entity, err := ResolveBillingEntity(userId)
	if err != nil {
		return err
	}
	return invalidateBillingQuotaCache(entity)
}

func adjustBillingQuotaWithTx(tx *gorm.DB, userId int, delta int) (BillingEntity, error) {
	entity, err := resolveBillingEntity(tx, userId)
	if err != nil {
		return BillingEntity{}, err
	}
	query := tx.Model(&User{}).Where("id = ?", entity.UserId)
	if entity.IsTenant() {
		query = tx.Model(&Tenant{}).Where("id = ?", entity.TenantId)
	}
	if err := query.Update("quota", gorm.Expr("quota + ?", delta)).Error; err != nil {
		return BillingEntity{}, err
	}
	return entity, nil
}

func IncreaseUserQuotaWithTx(tx *gorm.DB, userId int, quota int) (BillingEntity, error) {
	if quota < 0 {
		return BillingEntity{}, errors.New("quota cannot be negative")
	}
	return adjustBillingQuotaWithTx(tx, userId, quota)
}

func setBillingQuotaWithTx(tx *gorm.DB, userId int, quota int) (BillingEntity, error) {
	entity, err := resolveBillingEntity(tx, userId)
	if err != nil {
		return BillingEntity{}, err
	}
	query := tx.Model(&User{}).Where("id = ?", entity.UserId)
	if entity.IsTenant() {
		query = tx.Model(&Tenant{}).Where("id = ?", entity.TenantId)
	}
	if err := query.Update("quota", quota).Error; err != nil {
		return BillingEntity{}, err
	}
	return entity, nil
}

func SetUserQuota(userId int, quota int) error {
	if quota < 0 {
		return errors.New("quota cannot be negative")
	}
	entity, err := setBillingQuotaWithTx(DB, userId, quota)
	if err != nil {
		return err
	}
	_ = invalidateBillingQuotaCache(entity)
	return nil
}

func tryDecreaseUserQuotaWithTx(tx *gorm.DB, userId int, quota int) (BillingEntity, error) {
	entity, err := resolveBillingEntity(tx, userId)
	if err != nil {
		return BillingEntity{}, err
	}
	query := tx.Model(&User{}).Where("id = ? AND quota >= ?", entity.UserId, quota)
	if entity.IsTenant() {
		query = tx.Model(&Tenant{}).Where("id = ? AND quota >= ?", entity.TenantId, quota)
	}
	result := query.Update("quota", gorm.Expr("quota - ?", quota))
	if result.Error != nil {
		return BillingEntity{}, result.Error
	}
	if result.RowsAffected != 1 {
		return BillingEntity{}, ErrInsufficientBillingQuota
	}
	return entity, nil
}

func TryDecreaseUserQuotaWithTx(tx *gorm.DB, userId int, quota int) (BillingEntity, error) {
	if quota < 0 {
		return BillingEntity{}, errors.New("quota cannot be negative")
	}
	return tryDecreaseUserQuotaWithTx(tx, userId, quota)
}

// TryDecreaseUserQuota atomically refuses a wallet reservation that would
// overdraw the shared billing entity. Settlement adjustments still use
// DecreaseUserQuota because an upstream response may exceed its reservation.
func TryDecreaseUserQuota(userId int, quota int) error {
	if quota < 0 {
		return errors.New("quota cannot be negative")
	}
	entity, err := tryDecreaseUserQuotaWithTx(DB, userId, quota)
	if err != nil {
		return err
	}
	_ = invalidateBillingQuotaCache(entity)
	return nil
}

// FillEffectiveQuotas rewrites each user's Quota to the balance actually spent
// from: the tenant wallet for a tenant-backed login, the user's own column
// otherwise.
//
// Admin-facing reads MUST use this. Every login created through the admin API
// gets its own tenant, and from then on its balance lives in `tenants.quota`
// while `users.quota` stays at whatever it was — normally 0. Returning the raw
// column therefore makes a funded account read as empty, and makes every quota
// the operator sets look like it was silently discarded, because the number on
// screen never moves. The money is never actually lost; only the read is wrong.
// GetSelf already resolves this (buildSelfUserData), which is why a customer
// sees the correct balance while an operator does not.
//
// One query per page, not one per row: a page of users is the hot path here.
func FillEffectiveQuotas(users []*User) error {
	tenantIds := make([]int, 0, len(users))
	seen := make(map[int]bool, len(users))
	for _, user := range users {
		if user == nil || user.TenantId <= 0 || seen[user.TenantId] {
			continue
		}
		seen[user.TenantId] = true
		tenantIds = append(tenantIds, user.TenantId)
	}
	if len(tenantIds) == 0 {
		return nil
	}
	var rows []struct {
		Id    int
		Quota int
	}
	if err := DB.Model(&Tenant{}).Select("id", "quota").Where("id IN ?", tenantIds).Find(&rows).Error; err != nil {
		return err
	}
	quotaByTenant := make(map[int]int, len(rows))
	for _, row := range rows {
		quotaByTenant[row.Id] = row.Quota
	}
	for _, user := range users {
		if user == nil || user.TenantId <= 0 {
			continue
		}
		// A tenant row that has gone missing leaves the user's own column
		// alone rather than zeroing the display.
		if quota, ok := quotaByTenant[user.TenantId]; ok {
			user.Quota = quota
		}
	}
	return nil
}
