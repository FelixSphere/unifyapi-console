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

	"github.com/QuantumNous/new-api/common"
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
	// Members the partnership loop reached; the pricing-group loop below must
	// not count them a second time, or a dry run promises more than a real
	// run moves.
	handled := map[int]bool{}
	counted := map[string]bool{}
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
		counted[customer.Group] = true

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
				handled[user.Id] = true
				joined, err := result.admitTx(tx, &user, tenantId, dryRun)
				if err != nil {
					return err
				}
				if joined {
					moved = append(moved, movedMember{UserId: user.Id, TenantId: tenantId})
				}
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
	// UNIFYAPI-BRAND: a pricing group is a customer too. Logins the operator
	// created by hand each hold their money in a wallet of their own; move them
	// onto the group's. See model/customer_wallet.go for which groups count.
	var groups []string
	if err := DB.Model(&User{}).Where("role < ?", common.RoleAdminUser).
		Distinct().Pluck("group", &groups).Error; err != nil {
		return nil, err
	}
	for _, group := range groups {
		customer, err := isCustomerGroupTx(DB, group)
		if err != nil {
			return nil, err
		}
		if !customer {
			continue
		}
		var logins []User
		if err := DB.Where(map[string]any{"group": group}).Where("role < ?", common.RoleAdminUser).
			Order("id").Find(&logins).Error; err != nil {
			return nil, err
		}
		var members []User
		for _, login := range logins {
			if !handled[login.Id] {
				members = append(members, login)
			}
		}
		if len(members) == 0 {
			continue
		}
		if !counted[group] {
			result.Customers++
		}
		err = DB.Transaction(func(tx *gorm.DB) error {
			walletId, err := customerWalletTx(tx, group, group, slugFromName("customer-"+group))
			if err != nil {
				return err
			}
			for _, member := range members {
				var user User
				if err := tx.First(&user, member.Id).Error; err != nil {
					return err
				}
				joined, err := result.admitTx(tx, &user, walletId, dryRun)
				if err != nil {
					return err
				}
				if joined {
					moved = append(moved, movedMember{UserId: user.Id, TenantId: walletId})
				}
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

// admitTx moves one member into a wallet, or in a dry run only counts what
// would move. It reports whether a real move happened, so the caller can clear
// caches after the transaction commits: clearing them inside it lets a
// concurrent read repopulate the pre-commit balance, which nothing would then
// clear again.
func (result *CustomerPoolBackfillResult) admitTx(tx *gorm.DB, user *User, tenantId int, dryRun bool) (bool, error) {
	if user.TenantId == tenantId {
		result.AlreadyPooled++
		return false, nil
	}
	carried, err := quotaCarriedInto(tx, user)
	if err != nil {
		return false, err
	}
	if dryRun {
		result.MembersMoved++
		result.QuotaCarried += carried
		return false, nil
	}
	if err := JoinCustomerPoolTx(tx, user.Id, tenantId); err != nil {
		result.Skipped = append(result.Skipped,
			fmt.Sprintf("user %d (%s): %s", user.Id, user.Username, err.Error()))
		return false, nil
	}
	result.MembersMoved++
	result.QuotaCarried += carried
	return true, nil
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
