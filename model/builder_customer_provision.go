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
	"time"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// Auto-provisioning a Builder team.
//
// A team named by the Builder side that has no customer yet is created rather
// than refused. Because a customer IS a pricing group and a pricing group is an
// invoice counterparty, creating one writes to the GroupRatio option -- the map
// that replaces rather than merges on save, and that once reached thousands of
// keys in production.
//
// So the merge happens inside the same integrity lock the admin path uses, the
// previous value is snapshotted before it is overwritten, and a group that is
// already present is left exactly as it is. A team is never repriced by
// reconnecting.

// builderProvisionedGroupRatio is list price. A new team pays the published
// rate until somebody deliberately discounts it; inheriting a cohort discount
// by merely existing would give away margin nobody agreed to.
const builderProvisionedGroupRatio = 1

// partnershipCodeFromName derives a registration code from a display name.
// Codes are pattern-constrained (lowercase, 3-64 chars) while names are free
// text, so this is a lossy projection and collisions are expected; the caller
// resolves them.
func partnershipCodeFromName(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case unicode.IsSpace(r), r == '-', r == '_', r == '.':
			if b.Len() > 0 && !lastDash {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}
	code := strings.Trim(b.String(), "-")
	if len(code) > 56 {
		code = strings.Trim(code[:56], "-")
	}
	// The pattern requires a leading alphanumeric and at least three
	// characters. A name made entirely of punctuation or non-Latin script
	// leaves nothing usable, so fall back to a stable prefix.
	if len(code) < 3 {
		code = "team-" + code
		code = strings.Trim(code, "-")
	}
	return code
}

// EnsurePartnershipGroupRatio adds one pricing group at list price if it is
// absent, leaving every other entry untouched.
//
// The read, the merge and the write all happen inside one locked transaction.
// Reading the map first and writing it back afterwards would let two teams
// connecting at once each persist a map missing the other.
func EnsurePartnershipGroupRatio(group string) error {
	group = strings.TrimSpace(group)
	if group == "" {
		return fmt.Errorf("pricing group is required")
	}
	var merged, previous string
	err := DB.Transaction(func(tx *gorm.DB) error {
		return withPartnershipGroupIntegrityLock(tx, func(tx *gorm.DB) error {
			var option Option
			err := tx.Where("key = ?", "GroupRatio").First(&option).Error
			if err != nil && err != gorm.ErrRecordNotFound {
				return err
			}
			groups := map[string]float64{}
			if option.Value != "" {
				if err := common.Unmarshal([]byte(option.Value), &groups); err != nil {
					return fmt.Errorf("parse Group Pricing: %w", err)
				}
			}
			if _, exists := groups[group]; exists {
				// Already priced. Reconnecting must never reprice a team.
				return nil
			}
			groups[group] = builderProvisionedGroupRatio
			encoded, err := common.Marshal(groups)
			if err != nil {
				return err
			}
			previous, merged = option.Value, string(encoded)
			return saveOptionValue(tx, "GroupRatio", merged)
		})
	})
	if err != nil || merged == "" {
		return err
	}
	// Every pricing map here is replace-not-merge, so the overwritten value is
	// kept for audit exactly as the admin path keeps it. Recorded after the
	// commit, on purpose: the history writes on the global handle, so doing it
	// inside the transaction both deadlocks a single-connection pool and can
	// record a change that then rolls back.
	RecordPricingConfigChange("GroupRatio", previous, merged, "builder-bridge", "auto-provision-team")
	// Publish to the in-memory setting only after the row is committed, so a
	// rolled-back transaction cannot leave a group priced in memory alone.
	if err := updateOptionMap("GroupRatio", merged); err != nil {
		return err
	}
	// A pricing group is defined by three settings, not one. Writing only the
	// billing ratio leaves a half-registered group: it bills correctly, but it
	// has no top-up ratio, is not user-selectable, and never appears in the
	// Customer model prices editor -- so nobody can ever price a model for that
	// team. Register it completely or not at all.
	if err := ensureTopupGroupRatio(group); err != nil {
		return err
	}
	return ensureUserUsableGroup(group)
}

// ensureTopupGroupRatio gives the group the same top-up ratio a hand-created
// one gets, leaving an existing value alone.
func ensureTopupGroupRatio(group string) error {
	return ensureGroupSettingEntry("TopupGroupRatio", group, func(raw map[string]any) bool {
		if _, exists := raw[group]; exists {
			return false
		}
		raw[group] = builderProvisionedGroupRatio
		return true
	})
}

// ensureUserUsableGroup makes the group selectable and visible, labelled with
// its own name, which is what an operator sees in the pricing screens.
func ensureUserUsableGroup(group string) error {
	return ensureGroupSettingEntry("UserUsableGroups", group, func(raw map[string]any) bool {
		if _, exists := raw[group]; exists {
			return false
		}
		raw[group] = group
		return true
	})
}

// ensureGroupSettingEntry merges one key into a settings map under the same
// integrity lock, snapshotting the previous value first. Every map here
// replaces rather than merges on save, so a read-modify-write outside the lock
// would let two teams each persist a map missing the other.
func ensureGroupSettingEntry(key, group string, add func(map[string]any) bool) error {
	var merged, previous string
	err := DB.Transaction(func(tx *gorm.DB) error {
		return withPartnershipGroupIntegrityLock(tx, func(tx *gorm.DB) error {
			var option Option
			err := tx.Where("key = ?", key).First(&option).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			raw := map[string]any{}
			if option.Value != "" {
				if err := common.Unmarshal([]byte(option.Value), &raw); err != nil {
					return fmt.Errorf("parse %s: %w", key, err)
				}
			}
			if !add(raw) {
				return nil
			}
			encoded, err := common.Marshal(raw)
			if err != nil {
				return err
			}
			previous, merged = option.Value, string(encoded)
			return saveOptionValue(tx, key, merged)
		})
	})
	if err != nil || merged == "" {
		return err
	}
	RecordPricingConfigChange(key, previous, merged, "builder-bridge", "auto-provision-team")
	return updateOptionMap(key, merged)
}

// ensureBuilderCustomerTx finds the named customer inside a program, creating
// it if absent. Returns the offer for that customer.
//
// Name is free text and is stored verbatim, because it is what the Builder side
// signs and matches on. Code is derived and must be unique across every
// program, so a collision is resolved by suffixing rather than by silently
// attaching the team to somebody else's customer.
func ensureBuilderCustomerTx(tx *gorm.DB, program *PartnershipProgram, name string) (*PartnershipCustomer, error) {
	var existing []PartnershipCustomer
	if err := tx.Where("program_id = ? AND removed_at = ?", program.Id, 0).Find(&existing).Error; err != nil {
		return nil, err
	}
	for i := range existing {
		if existing[i].Name == name {
			if !existing[i].Enabled {
				return nil, ErrPartnershipCustomerUnavailable
			}
			return &existing[i], nil
		}
	}

	base := partnershipCodeFromName(name)
	code := base
	for attempt := 2; ; attempt++ {
		var clash int64
		if err := tx.Model(&PartnershipCustomer{}).Where("code = ?", code).Count(&clash).Error; err != nil {
			return nil, err
		}
		if clash == 0 {
			break
		}
		code = fmt.Sprintf("%s-%d", base, attempt)
		if attempt > 50 {
			return nil, fmt.Errorf("could not derive a free registration code for %q", name)
		}
	}

	// The pricing group becomes the invoice counterparty and is printed as the
	// bill-to line, so the display name is used where it fits. The column is
	// varchar(64) while a team name may be up to 120 characters, in which case
	// the derived code stands in -- an unlovely invoice beats a failed insert.
	group := name
	if len([]rune(group)) > 64 || len(group) > 64 {
		group = code
	}
	customer := PartnershipCustomer{
		ProgramId: program.Id, Name: name, Code: code, Group: group,
		IsDefault: false, Enabled: true,
	}
	if err := ValidatePartnershipCustomer(&customer); err != nil {
		return nil, err
	}
	// A customer may only reference a group that exists in Group Pricing, so
	// the group is priced first. Both happen before the enrollment is written,
	// so a failure here leaves no half-provisioned team behind.
	if err := validatePartnershipProgramGroup(tx, customer.Group); err != nil {
		return nil, err
	}
	if err := tx.Create(&customer).Error; err != nil {
		return nil, err
	}
	return &customer, nil
}

// ProvisionBuilderCustomer creates the pricing group and the customer for a
// team the Builder side named but that does not exist here yet. Idempotent:
// two members of the same new team connecting at once leave one customer.
func ProvisionBuilderCustomer(programName, customerName string) error {
	if !ValidBuilderProgramName(programName) || !ValidBuilderProgramName(customerName) {
		return ErrPartnershipCustomerUnavailable
	}
	program, err := resolveProgramByName(DB, programName)
	if err != nil {
		return err
	}

	// Derive the group the same way the customer will, and price it before the
	// customer row can reference it.
	code := partnershipCodeFromName(customerName)
	group := customerName
	if len([]rune(group)) > 64 || len(group) > 64 {
		group = code
	}
	if err := EnsurePartnershipGroupRatio(group); err != nil {
		return err
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		return withPartnershipGroupIntegrityLock(tx, func(tx *gorm.DB) error {
			_, err := ensureBuilderCustomerTx(tx, program, customerName)
			return err
		})
	})
	if err == nil {
		return nil
	}
	// A concurrent connect for the same team may have won the race. That is a
	// success for this caller, not a failure, so long as the team now exists.
	if _, resolveErr := ResolveBuilderProgram(DB, BuilderProgramSelector{
		ProgramName: programName, CustomerName: customerName,
	}, false); resolveErr == nil {
		return nil
	}
	return err
}

// resolveProgramByName finds exactly one active program, comparing in Go
// because database collations may be case or accent insensitive.
func resolveProgramByName(tx *gorm.DB, name string) (*PartnershipProgram, error) {
	var candidates []PartnershipProgram
	if err := tx.Where("name = ? AND removed_at = ?", name, 0).Find(&candidates).Error; err != nil {
		return nil, err
	}
	var matches []PartnershipProgram
	for _, candidate := range candidates {
		if candidate.Name == name {
			matches = append(matches, candidate)
		}
	}
	switch {
	case len(matches) == 0:
		return nil, ErrPartnershipProgramNotFound
	case len(matches) > 1:
		return nil, ErrPartnershipProgramAmbiguous
	case !partnershipProgramActive(&matches[0], time.Now().Unix()):
		return nil, ErrPartnershipProgramInactive
	}
	return &matches[0], nil
}

// ensureCustomerTenant returns the tenant that owns a team's wallet, creating
// it the first time one of its members connects.
//
// This is what makes a team's credit one balance instead of one balance each.
// getBillingQuotaFromDB already reads and writes the Tenant row rather than
// the User row whenever a user has a tenant, so pointing every member of a
// team at the same tenant is the whole mechanism -- there is no splitting and
// no redistribution to do.
//
// Two cases deliberately keep the per-member wallet they have always had:
//
//   - A program with no customer row, which predates customers entirely.
//   - The program's default customer. That is a catch-all bucket, not a team:
//     it holds everyone who arrived without a team name, and those people have
//     nothing to do with each other. Pooling them would let strangers spend
//     each other's credit.
func ensureCustomerTenant(tx *gorm.DB, offer *PartnershipOffer) (int, error) {
	if offer == nil || offer.CustomerId <= 0 {
		return 0, nil
	}
	var customer PartnershipCustomer
	if err := lockForUpdate(tx).First(&customer, offer.CustomerId).Error; err != nil {
		return 0, err
	}
	if customer.IsDefault {
		return 0, nil
	}
	if customer.TenantId != 0 {
		return customer.TenantId, nil
	}
	tenant := &Tenant{
		Name:   customer.Name,
		Slug:   slugFromName("team-" + customer.Code),
		Status: TenantStatusEnabled,
		Group:  customer.Group,
	}
	if err := CreateTenantWithTx(tx, tenant); err != nil {
		return 0, err
	}
	if err := tx.Model(&PartnershipCustomer{}).Where("id = ?", customer.Id).
		Update("tenant_id", tenant.Id).Error; err != nil {
		return 0, err
	}
	return tenant.Id, nil
}

// claimTeamTenantOwner names the first member to connect as the tenant's
// owner. The tenant is created before any member exists, so it starts
// ownerless; leaving it that way would hide the team from views that key on
// the owner.
func claimTeamTenantOwner(tx *gorm.DB, tenantId, userId int) error {
	return tx.Model(&Tenant{}).Where("id = ? AND owner_id = ?", tenantId, 0).
		Update("owner_id", userId).Error
}
