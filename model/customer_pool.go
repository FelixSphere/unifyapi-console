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

	"gorm.io/gorm"
)

// ErrAlreadyInAnotherPool is returned when a user cannot be moved into a
// customer's wallet because they are already sharing a different one. Merging
// two populated wallets would merge two customers' money, which is never
// something to do as a side effect of somebody connecting.
var ErrAlreadyInAnotherPool = errors.New("account already shares another customer's wallet")

// JoinCustomerPoolTx moves a user into the wallet a customer's members spend
// from, carrying their balance with them so no credit is lost in the move.
//
// A customer is one wallet and every member draws on it. That holds wherever a
// user acquires a customer, not only over the Builder bridge, which is why
// this lives outside that path.
//
// Three shapes of balance have to survive the move:
//
//   - money in the user's own column, which folds into the pool;
//   - money in a solo tenant the user is the only member of, which is absorbed
//     and the empty tenant left behind;
//   - money in a tenant with other members, which is refused. That is another
//     customer's pool and is not this function's to spend.
func JoinCustomerPoolTx(tx *gorm.DB, userId, tenantId int) error {
	if userId <= 0 || tenantId <= 0 {
		return errors.New("user id and tenant id are required")
	}
	var user User
	if err := lockForUpdate(tx).First(&user, userId).Error; err != nil {
		return err
	}
	if user.TenantId == tenantId {
		return nil
	}
	// An operator joining a customer wallet would hand them that customer's
	// balance. EnsureTenantForUserTx refuses the same thing for the same reason.
	if IsStaffRole(user.Role) {
		return errors.New("cannot move an operator account into a customer wallet")
	}

	carried := user.Quota
	if user.TenantId != 0 {
		var members int64
		if err := tx.Model(&User{}).Where("tenant_id = ?", user.TenantId).
			Count(&members).Error; err != nil {
			return err
		}
		if members > 1 {
			return fmt.Errorf("%w: tenant %d has %d members", ErrAlreadyInAnotherPool, user.TenantId, members)
		}
		var solo Tenant
		if err := lockForUpdate(tx).First(&solo, user.TenantId).Error; err != nil {
			return err
		}
		carried += solo.Quota
		if solo.Quota != 0 {
			if err := tx.Model(&Tenant{}).Where("id = ?", solo.Id).
				Update("quota", 0).Error; err != nil {
				return err
			}
		}
	}

	if carried != 0 {
		if err := tx.Model(&Tenant{}).Where("id = ?", tenantId).
			Update("quota", gorm.Expr("quota + ?", carried)).Error; err != nil {
			return err
		}
	}
	return tx.Model(&User{}).Where("id = ?", user.Id).
		Updates(map[string]any{"tenant_id": tenantId, "quota": 0}).Error
}

// JoinCustomerPool is the non-transactional entry point, and refreshes the
// caches that would otherwise keep serving the balance the user had before
// the move.
func JoinCustomerPool(userId, tenantId int) error {
	if err := DB.Transaction(func(tx *gorm.DB) error {
		return JoinCustomerPoolTx(tx, userId, tenantId)
	}); err != nil {
		return err
	}
	_ = invalidateUserCache(userId)
	_ = invalidateBillingQuotaCache(BillingEntity{UserId: userId, TenantId: tenantId})
	return nil
}

// teamGrantHolder returns the customer that owns a program's grant for this
// offer, or nil when the grant is still a per-member one.
//
// The grant belongs to the team, so it is claimed once per team however many
// people join. Two cases keep the older per-member behaviour, and they are the
// same two that keep a per-member wallet: a program with no customer row, and
// the program's default customer, which is a catch-all of unrelated people
// rather than a team.
func teamGrantHolder(tx *gorm.DB, offer *PartnershipOffer) (*PartnershipCustomer, error) {
	if offer == nil || offer.CustomerId <= 0 {
		return nil, nil
	}
	var customer PartnershipCustomer
	if err := lockForUpdate(tx).First(&customer, offer.CustomerId).Error; err != nil {
		return nil, err
	}
	if customer.IsDefault {
		return nil, nil
	}
	return &customer, nil
}

// recordTeamGrantClaim marks the team's single claim.
func recordTeamGrantClaim(tx *gorm.DB, customerId, quota int) error {
	return tx.Model(&PartnershipCustomer{}).Where("id = ?", customerId).
		Updates(map[string]any{
			"grant_claimed_at": time.Now().Unix(),
			"granted_quota":    quota,
		}).Error
}
