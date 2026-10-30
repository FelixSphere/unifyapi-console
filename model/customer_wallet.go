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

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// CustomerWallet is the one tenant a pricing group bills through.
//
// UNIFYAPI-BRAND: the operator's definition of a customer is a pricing group:
// one group, one customer, one wallet. Every login in the group draws on that
// wallet and every login's top-up lands in it. `default` is not a customer --
// its logins are strangers to each other, so each keeps a wallet of its own --
// and neither is a partnership program's catch-all group, for the same reason.
//
// tenants.group cannot serve as this registry: every solo tenant minted before
// pooling mirrors its owner's group, so "the tenant for group X" became
// ambiguous the moment a second login joined X. This table names the one that
// counts.
type CustomerWallet struct {
	Id       int    `json:"id"`
	Group    string `json:"group" gorm:"column:pricing_group;type:varchar(255);uniqueIndex"`
	TenantId int    `json:"tenant_id" gorm:"not null;index"`
}

// isCustomerGroupTx says whether logins in group share one wallet.
func isCustomerGroupTx(tx *gorm.DB, group string) (bool, error) {
	if group == "" || group == DefaultUserGroup {
		return false, nil
	}
	var catchAll int64
	if err := tx.Model(&PartnershipCustomer{}).
		Where(map[string]any{"group": group, "is_default": true}).
		Count(&catchAll).Error; err != nil {
		return false, err
	}
	return catchAll == 0, nil
}

// customerWalletTx returns the wallet for group, creating it on first use. A
// partnership team already holding a wallet for the group keeps it: the team
// and the pricing group are the same customer, not two with one name.
func customerWalletTx(tx *gorm.DB, group, name, slug string) (int, error) {
	var wallet CustomerWallet
	err := lockForUpdate(tx).Where("pricing_group = ?", group).First(&wallet).Error
	if err == nil {
		return wallet.TenantId, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}
	var team PartnershipCustomer
	err = tx.Where(map[string]any{"group": group, "is_default": false, "removed_at": 0}).
		Where("tenant_id <> 0").Order("id").First(&team).Error
	switch {
	case err == nil:
		wallet.TenantId = team.TenantId
	case errors.Is(err, gorm.ErrRecordNotFound):
		// A login already alone in a wallet of its own is the customer's first
		// member: its wallet becomes the customer's, keeping its id and its
		// history, and nothing has to move.
		wallet.TenantId, err = soloWalletInGroupTx(tx, group)
		if err != nil {
			return 0, err
		}
		if wallet.TenantId != 0 {
			if err := tx.Model(&Tenant{}).Where("id = ?", wallet.TenantId).Update("name", name).Error; err != nil {
				return 0, err
			}
			break
		}
		tenant := &Tenant{Name: name, Slug: slug, Status: TenantStatusEnabled, Group: group}
		if err := CreateTenantWithTx(tx, tenant); err != nil {
			return 0, err
		}
		wallet.TenantId = tenant.Id
	default:
		return 0, err
	}
	wallet.Group = group
	if err := tx.Create(&wallet).Error; err != nil {
		return 0, err
	}
	return wallet.TenantId, nil
}

// soloWalletInGroupTx finds a wallet held by exactly one login of the group,
// lowest id first, or 0 when there is none.
func soloWalletInGroupTx(tx *gorm.DB, group string) (int, error) {
	var held []int
	if err := tx.Model(&User{}).Where(map[string]any{"group": group}).
		Where("role < ? AND tenant_id <> 0", common.RoleAdminUser).
		Order("tenant_id").Distinct().Pluck("tenant_id", &held).Error; err != nil {
		return 0, err
	}
	for _, tenantId := range held {
		var members int64
		if err := tx.Model(&User{}).Where("tenant_id = ?", tenantId).Count(&members).Error; err != nil {
			return 0, err
		}
		if members == 1 {
			return tenantId, nil
		}
	}
	return 0, nil
}

// reattachWalletTx follows a login into its new pricing group. A login joining
// a customer draws on that customer's wallet, bringing a wallet it had to
// itself along; one leaving a shared wallet leaves the balance behind, because
// it was the customer's, not theirs. A login leaving for `default` gets an
// empty wallet of its own.
func reattachWalletTx(tx *gorm.DB, user User, group string) error {
	customer, err := isCustomerGroupTx(tx, group)
	if err != nil {
		return err
	}
	if customer {
		walletId, err := customerWalletTx(tx, group, group, slugFromName("customer-"+group))
		if err != nil {
			return err
		}
		if user.TenantId == walletId {
			return nil
		}
		err = JoinCustomerPoolTx(tx, user.Id, walletId)
		if errors.Is(err, ErrAlreadyInAnotherPool) {
			err = tx.Model(&User{}).Where("id = ?", user.Id).Update("tenant_id", walletId).Error
		}
		if err != nil {
			return err
		}
		return claimTeamTenantOwner(tx, walletId, user.Id)
	}
	if user.TenantId != 0 {
		var members int64
		if err := tx.Model(&User{}).Where("tenant_id = ?", user.TenantId).Count(&members).Error; err != nil {
			return err
		}
		if members <= 1 {
			return nil
		}
	}
	name := user.DisplayName
	if name == "" {
		name = user.Username
	}
	solo := &Tenant{Name: name, Slug: slugFromName(user.Username), Status: TenantStatusEnabled, OwnerId: user.Id, Group: group}
	if err := CreateTenantWithTx(tx, solo); err != nil {
		return err
	}
	return tx.Model(&User{}).Where("id = ?", user.Id).Update("tenant_id", solo.Id).Error
}
