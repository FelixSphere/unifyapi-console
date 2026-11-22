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
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"gorm.io/gorm"
)

// UNIFYAPI-FORK: access tier groups.
//
// A pricing group is a customer, and a customer has one wallet
// (customer_wallet.go). That is right for `Acme` and wrong for a group the
// operator uses to grant something: `Vip User` exists so that its members may
// reach a model the other groups are blocked from, and its members have
// nothing else to do with each other. On production, 2026-11-22, four
// unrelated logins moved into it were pooled into one wallet.
//
// An access tier is a pricing group whose logins are strangers, exactly as the
// logins of `default` are: each keeps a wallet of its own, and moving a login
// in or out moves no balance. Prices, channel access and everything else a
// group decides are unchanged.
//
// The list is one option row. It is read from the database inside the
// transaction that is about to place a login in a wallet, never from the
// in-memory option map, so the answer cannot be a minute old on the one path
// where a wrong answer merges two people's money.

// AccessTierGroupsOptionKey holds a JSON array of group names.
const AccessTierGroupsOptionKey = "AccessTierGroups"

// AccessTierChange reports what marking or un-marking a group did.
type AccessTierChange struct {
	Group      string `json:"group"`
	AccessTier bool   `json:"access_tier"`
	// Changed is false when the group already was what was asked for.
	Changed bool `json:"changed"`
	// ReleasedWalletTenant is the tenant the group's shared-wallet registration
	// pointed at, when marking removed that registration. The tenant, its
	// balance and its member, if it has one, are untouched.
	ReleasedWalletTenant int `json:"released_wallet_tenant,omitempty"`
	// Groups is the list after the change.
	Groups []string `json:"groups"`
}

// accessTierGroupsTx returns the stored list, in stored order.
func accessTierGroupsTx(tx *gorm.DB) ([]string, error) {
	if !tx.Migrator().HasTable(&Option{}) {
		return nil, nil
	}
	var rows []Option
	if err := tx.Where(map[string]any{"key": AccessTierGroupsOptionKey}).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 || strings.TrimSpace(rows[0].Value) == "" {
		return nil, nil
	}
	var groups []string
	if err := common.Unmarshal([]byte(rows[0].Value), &groups); err != nil {
		// Unreadable is not "no tiers": answering false here would pool the
		// next login moved into a tier group. The caller fails instead.
		return nil, fmt.Errorf("parse %s: %w", AccessTierGroupsOptionKey, err)
	}
	return groups, nil
}

// isAccessTierGroupTx says whether group is marked as an access tier.
func isAccessTierGroupTx(tx *gorm.DB, group string) (bool, error) {
	groups, err := accessTierGroupsTx(tx)
	if err != nil {
		return false, err
	}
	for _, tier := range groups {
		if tier == group {
			return true, nil
		}
	}
	return false, nil
}

// AccessTierGroups returns the groups marked as access tiers.
func AccessTierGroups() ([]string, error) {
	groups, err := accessTierGroupsTx(DB)
	if err != nil {
		return nil, err
	}
	if groups == nil {
		groups = []string{}
	}
	return groups, nil
}

// SetPricingGroupAccessTier marks group as an access tier, or removes the
// mark. Neither direction moves a login between wallets or changes a balance;
// each refuses when the only way to make the group consistent would be to do
// that, and says what is in the way.
func SetPricingGroupAccessTier(group string, tier bool, actor string) (*AccessTierChange, error) {
	group = strings.TrimSpace(group)
	if group == "" {
		return nil, errors.New("the group is required")
	}
	change := &AccessTierChange{Group: group, AccessTier: tier}
	var stored string
	err := DB.Transaction(func(tx *gorm.DB) error {
		// The lock a Group Pricing save, a rename and a partnership program
		// write hold while they decide what a group is.
		return withPartnershipGroupIntegrityLock(tx, func(tx *gorm.DB) error {
			groups, err := accessTierGroupsTx(tx)
			if err != nil {
				return err
			}
			marked := false
			kept := make([]string, 0, len(groups)+1)
			for _, existing := range groups {
				if existing == group {
					marked = true
					continue
				}
				kept = append(kept, existing)
			}
			if marked == tier {
				change.Groups = append([]string{}, groups...)
				return nil
			}
			if tier {
				if err := validateNewAccessTierTx(tx, group); err != nil {
					return err
				}
				if change.ReleasedWalletTenant, err = releaseTierGroupWalletTx(tx, group); err != nil {
					return err
				}
				if err := refuseTierWithPooledLoginsTx(tx, group); err != nil {
					return err
				}
				kept = append(kept, group)
			} else if err := refuseTierWithLoginsTx(tx, group); err != nil {
				return err
			}
			encoded, err := common.Marshal(kept)
			if err != nil {
				return err
			}
			stored = string(encoded)
			change.Changed = true
			change.Groups = kept
			return saveOptionValue(tx, AccessTierGroupsOptionKey, stored)
		})
	})
	if err != nil {
		return nil, err
	}
	if !change.Changed {
		return change, nil
	}
	if err := updateOptionMapLocked(AccessTierGroupsOptionKey, stored); err != nil {
		common.SysError("access tier: failed to publish the list: " + err.Error())
	}
	common.SysLog(fmt.Sprintf("pricing group %q access tier set to %t by %s; released wallet tenant %d; tiers now %s",
		group, tier, actor, change.ReleasedWalletTenant, stored))
	return change, nil
}

// validateNewAccessTierTx refuses the groups that cannot be an access tier.
func validateNewAccessTierTx(tx *gorm.DB, group string) error {
	switch {
	case group == DefaultUserGroup:
		return fmt.Errorf("group %q already gives every login its own wallet", group)
	case group == "auto":
		return fmt.Errorf("%q is not a pricing group", group)
	case IsBYOKGroup(group):
		return fmt.Errorf("group %q holds one customer's own provider key and cannot be an access tier", group)
	}
	var rows []Option
	if err := tx.Where(map[string]any{"key": "GroupRatio"}).Find(&rows).Error; err != nil {
		return err
	}
	stored := ratio_setting.GroupRatio2JSONString()
	if len(rows) > 0 {
		stored = rows[0].Value
	}
	priced := map[string]any{}
	if err := common.Unmarshal([]byte(stored), &priced); err != nil {
		return fmt.Errorf("parse Group Pricing: %w", err)
	}
	if _, ok := priced[group]; !ok {
		return fmt.Errorf("group %q does not exist in Group Pricing; save the group first", group)
	}
	// A partnership program's team is a customer by construction: its wallet
	// is created when the team is provisioned, before any login exists, and
	// the team's grant is paid into it.
	partners := []struct {
		what  string
		model any
	}{
		{"partnership program", &PartnershipProgram{}},
		{"partnership customer", &PartnershipCustomer{}},
	}
	for _, partner := range partners {
		if !tx.Migrator().HasTable(partner.model) {
			continue
		}
		var count int64
		if err := tx.Model(partner.model).Where(map[string]any{"group": group}).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return fmt.Errorf("group %q belongs to a %s, whose members share the team's wallet; it cannot be an access tier", group, partner.what)
		}
	}
	return nil
}

// releaseTierGroupWalletTx removes the shared-wallet registration of a group
// that is becoming an access tier, and returns the tenant it pointed at.
//
// It only ever deletes the registry row. That is safe exactly when nobody's
// money depends on the row: the wallet is empty and has no member, or it has
// one member, whose wallet it then simply is. A wallet two or more logins
// spend from is a pool that somebody has to divide, and a balance with no
// member is money somebody has to assign; both are refused, because either
// would otherwise be decided here by accident.
func releaseTierGroupWalletTx(tx *gorm.DB, group string) (int, error) {
	if !tx.Migrator().HasTable(&CustomerWallet{}) {
		return 0, nil
	}
	var wallets []CustomerWallet
	if err := lockForUpdate(tx).Where("pricing_group = ?", group).Find(&wallets).Error; err != nil {
		return 0, err
	}
	released := 0
	for _, wallet := range wallets {
		var members []string
		if err := tx.Model(&User{}).Where("tenant_id = ?", wallet.TenantId).
			Order("id").Pluck("username", &members).Error; err != nil {
			return 0, err
		}
		var tenants []Tenant
		if err := lockForUpdate(tx).Where("id = ?", wallet.TenantId).Find(&tenants).Error; err != nil {
			return 0, err
		}
		balance := 0
		if len(tenants) > 0 {
			balance = tenants[0].Quota
		}
		switch {
		case len(members) > 1:
			return 0, fmt.Errorf("group %q has a shared wallet (tenant %d, balance %d) that %d logins spend from: %s. "+
				"Marking it as an access tier would not divide that balance. Move each login that should not keep this wallet to another group first, or have the balance split, and try again",
				group, wallet.TenantId, balance, len(members), listNames(members))
		case len(members) == 0 && balance != 0:
			return 0, fmt.Errorf("group %q has a shared wallet (tenant %d) that holds a balance of %d and has no login. "+
				"Decide where that balance goes (credit it to a login or clear it) and try again",
				group, wallet.TenantId, balance)
		}
		if err := tx.Delete(&CustomerWallet{}, wallet.Id).Error; err != nil {
			return 0, err
		}
		released = wallet.TenantId
	}
	return released, nil
}

// refuseTierWithPooledLoginsTx refuses to mark a group in which two logins
// already spend from one wallet, whatever put them there. The mark would
// leave them where they are, and the operator would be told they are apart.
func refuseTierWithPooledLoginsTx(tx *gorm.DB, group string) error {
	if !tx.Migrator().HasTable(&User{}) {
		return nil
	}
	var logins []User
	if err := tx.Select("id", "username", "tenant_id").Where(map[string]any{"group": group}).
		Where("role < ? AND tenant_id <> 0", common.RoleAdminUser).
		Order("tenant_id, id").Find(&logins).Error; err != nil {
		return err
	}
	for start := 0; start < len(logins); {
		end := start
		var names []string
		for end < len(logins) && logins[end].TenantId == logins[start].TenantId {
			names = append(names, logins[end].Username)
			end++
		}
		if len(names) > 1 {
			return fmt.Errorf("%d logins of group %q spend from one wallet (tenant %d): %s. "+
				"Marking the group as an access tier would leave them sharing it. Move each login that should have its own wallet to another group first, then mark the group and move them back",
				len(names), group, logins[start].TenantId, listNames(names))
		}
		start = end
	}
	return nil
}

// refuseTierWithLoginsTx refuses to turn a tier that has logins back into a
// customer group. As a customer they would have to share one wallet, and they
// hold one each: making that true means merging their balances, which is the
// accident this file exists to prevent.
func refuseTierWithLoginsTx(tx *gorm.DB, group string) error {
	if !tx.Migrator().HasTable(&User{}) {
		return nil
	}
	var members []string
	if err := tx.Model(&User{}).Where(map[string]any{"group": group}).
		Where("role < ?", common.RoleAdminUser).
		Order("id").Pluck("username", &members).Error; err != nil {
		return err
	}
	if len(members) == 0 {
		return nil
	}
	return fmt.Errorf("group %q still has %d login(s): %s. As a customer group they would share one wallet. Move them to another group first, then remove the access tier mark",
		group, len(members), listNames(members))
}

// listNames joins the first few names and counts the rest.
func listNames(names []string) string {
	const shown = 5
	if len(names) <= shown {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:shown], ", "), len(names)-shown)
}
