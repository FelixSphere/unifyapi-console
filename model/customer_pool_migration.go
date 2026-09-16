/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"fmt"

	"gorm.io/gorm"
)

// CustomerPoolBackfillResult reports what moving existing members into their
// customer's wallet did, so the operator can check the totals before and after
// rather than trusting that it worked.
type CustomerPoolBackfillResult struct {
	Customers     int      `json:"customers"`
	MembersMoved  int      `json:"members_moved"`
	QuotaCarried  int      `json:"quota_carried"`
	AlreadyPooled int      `json:"already_pooled"`
	Skipped       []string `json:"skipped,omitempty"`
}

// BackfillCustomerPools moves members enrolled in a customer onto that
// customer's wallet, carrying their balance. Accounts that already hold their
// credit in a private tenant keep every unit of it: the balance is added to
// the customer's wallet, never discarded.
//
// DryRun reports what would move without writing, because this changes where
// real money lives and is not something to run blind.
func BackfillCustomerPools(dryRun bool) (*CustomerPoolBackfillResult, error) {
	result := &CustomerPoolBackfillResult{}
	var moved []movedMember
	var customers []PartnershipCustomer
	if err := DB.Where("is_default = ? AND removed_at = ?", false, 0).
		Find(&customers).Error; err != nil {
		return nil, err
	}

	for _, customer := range customers {
		var enrollments []PartnershipEnrollment
		if err := DB.Where("customer_id = ?", customer.Id).Find(&enrollments).Error; err != nil {
			return nil, err
		}
		if len(enrollments) == 0 {
			continue
		}
		result.Customers++

		err := DB.Transaction(func(tx *gorm.DB) error {
			tenantId := customer.TenantId
			if tenantId == 0 {
				offer := &PartnershipOffer{
					Program:    PartnershipProgram{Id: customer.ProgramId},
					CustomerId: customer.Id,
				}
				created, err := ensureCustomerTenant(tx, offer)
				if err != nil {
					return err
				}
				tenantId = created
			}
			if tenantId == 0 {
				result.Skipped = append(result.Skipped,
					fmt.Sprintf("customer %q has no wallet and none could be created", customer.Name))
				return nil
			}
			for _, enrollment := range enrollments {
				var user User
				if err := tx.First(&user, enrollment.UserId).Error; err != nil {
					if err == gorm.ErrRecordNotFound {
						continue
					}
					return err
				}
				if user.TenantId == tenantId {
					result.AlreadyPooled++
					continue
				}
				carried, err := quotaCarriedInto(tx, &user)
				if err != nil {
					return err
				}
				if dryRun {
					result.MembersMoved++
					result.QuotaCarried += carried
					continue
				}
				if err := JoinCustomerPoolTx(tx, user.Id, tenantId); err != nil {
					result.Skipped = append(result.Skipped,
						fmt.Sprintf("user %d (%s): %s", user.Id, user.Username, err.Error()))
					continue
				}
				result.MembersMoved++
				result.QuotaCarried += carried
				// Caches are cleared after the transaction commits, not here.
				// Clearing them inside it lets a concurrent read repopulate the
				// pre-commit balance, which nothing would then clear again.
				moved = append(moved, movedMember{UserId: user.Id, TenantId: tenantId})
			}
			if dryRun {
				return errDryRun
			}
			return nil
		})
		if err != nil && err != errDryRun {
			return nil, err
		}
	}
	for _, member := range moved {
		_ = invalidateUserCache(member.UserId)
		_ = invalidateBillingQuotaCache(BillingEntity{UserId: member.UserId, TenantId: member.TenantId})
	}
	return result, nil
}

// movedMember is a member whose caches need clearing once their move has
// actually committed.
type movedMember struct {
	UserId   int
	TenantId int
}

// errDryRun rolls a dry run back without reporting a failure.
var errDryRun = fmt.Errorf("dry run")

// quotaCarriedInto reports how much a user would bring into a pool, which is
// their own column plus a solo tenant's balance.
func quotaCarriedInto(tx *gorm.DB, user *User) (int, error) {
	carried := user.Quota
	if user.TenantId == 0 {
		return carried, nil
	}
	var members int64
	if err := tx.Model(&User{}).Where("tenant_id = ?", user.TenantId).Count(&members).Error; err != nil {
		return 0, err
	}
	if members > 1 {
		return 0, nil
	}
	var solo Tenant
	if err := tx.First(&solo, user.TenantId).Error; err != nil {
		return 0, err
	}
	return carried + solo.Quota, nil
}
