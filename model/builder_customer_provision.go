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
	"sync"
	"time"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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

// provisionedGroupRatio is the billing multiplier a newly provisioned
// pricing group starts at. Operator decision 2026-09-17: every new customer --
// a team joining through a partnership program included -- pays 90% of the
// published price until somebody deliberately reprices that group. (Until
// then a new team started at list, 1.0.) Existing groups are never repriced by
// reconnecting; the constant only applies to a group being created.
//
// provisionedTopupRatio is deliberately NOT discounted: the top-up ratio says
// how much credit a payment buys, and a discount there would give the same 10%
// away twice.
const (
	provisionedGroupRatio = 0.9
	provisionedTopupRatio = 1
)

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

// EnsurePartnershipGroupRatio adds one pricing group at the provisioned
// discount if it is absent, leaving every other entry untouched.
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
			groups[group] = provisionedGroupRatio
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
	// Deliberately NOT added to UserUsableGroups. That option is the list of
	// groups ANY user may select, so putting a customer's group in it published
	// the customer's name and commercial terms to every other user -- including
	// anonymous callers of /api/pricing -- and let them bill under it. The
	// operator's pricing screens do not need it: the Group Pricing editor
	// builds its list from GroupRatio union UserUsableGroups union
	// TopupGroupRatio, and the two ratios above already put the group there.
	// The customer's own members reach their group through the fallback in
	// service.GetUserUsableGroups.
	InvalidateCustomerOwnedGroupsCache()
	return nil
}

// ensureTopupGroupRatio gives the group the same top-up ratio a hand-created
// one gets, leaving an existing value alone.
func ensureTopupGroupRatio(group string) error {
	return ensureGroupSettingEntry("TopupGroupRatio", group, func(raw map[string]any) bool {
		if _, exists := raw[group]; exists {
			return false
		}
		raw[group] = provisionedTopupRatio
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

// builderProvisionMu keeps cache publication ordered within this process. The
// database integrity lock below serializes provisioning with other instances.
var builderProvisionMu sync.Mutex

type builderGroupChange struct {
	key, old, value string
}

// Merge the final group into all settings inside the customer transaction.
// A failed insert must leave neither durable settings nor cache changes behind.
func registerBuilderGroupTx(tx *gorm.DB, group string) ([]builderGroupChange, error) {
	var changes []builderGroupChange
	for _, key := range []string{"GroupRatio", "TopupGroupRatio", "UserUsableGroups"} {
		var option Option
		err := tx.Where("key = ?", key).First(&option).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		entries := map[string]any{}
		if option.Value != "" {
			if err := common.Unmarshal([]byte(option.Value), &entries); err != nil {
				return nil, fmt.Errorf("parse %s: %w", key, err)
			}
		}
		if entries == nil {
			return nil, fmt.Errorf("%s must be an object", key)
		}
		// Validate the types expected by cache publication before committing.
		// Syntactically valid JSON with wrong values must roll back too.
		if option.Value != "" {
			if key == "UserUsableGroups" {
				var typed map[string]string
				if err := common.Unmarshal([]byte(option.Value), &typed); err != nil {
					return nil, err
				}
			} else {
				var typed map[string]float64
				if err := common.Unmarshal([]byte(option.Value), &typed); err != nil {
					return nil, err
				}
			}
		}
		if _, exists := entries[group]; exists {
			continue
		}
		switch key {
		case "UserUsableGroups":
			entries[group] = group
		case "TopupGroupRatio":
			entries[group] = provisionedTopupRatio
		default:
			entries[group] = provisionedGroupRatio
		}
		raw, err := common.Marshal(entries)
		if err != nil {
			return nil, err
		}
		if err := saveOptionValue(tx, key, string(raw)); err != nil {
			return nil, err
		}
		changes = append(changes, builderGroupChange{key, option.Value, string(raw)})
	}
	return changes, nil
}

// Allocate a fresh settlement group whenever a previous customer owns the
// display-name group. Removed customers remain owners of their historical keys.
// Never change their group or reuse it for a new invoice counterparty.
func builderCustomerIdentifiers(tx *gorm.DB, program *PartnershipProgram, name string) (string, string, error) {
	base := partnershipCodeFromName(name)
	for attempt := 1; attempt <= 100; attempt++ {
		code := base
		display := name
		if attempt > 1 {
			code = fmt.Sprintf("%s-%d", base, attempt)
			display = fmt.Sprintf("%s-%d", name, attempt)
		}
		group := customerPricingGroupName(program, display, code)
		var count int64
		if err := tx.Model(&PartnershipCustomer{}).Where(clause.Or(clause.Eq{Column: "code", Value: code}, clause.Eq{Column: "group", Value: group})).Count(&count).Error; err != nil {
			return "", "", err
		}
		if count != 0 {
			continue
		}
		// Every candidate must also avoid a billing group an operator
		// configured independently, on the first attempt as much as any other.
		// This check used to be skipped when the group was the team's plain
		// name, which is the one case where an outside name can collide with
		// an existing group: a team called "UnifyAPI" would have adopted the
		// "UnifyAPI" group and its invoice, and nothing would have said so.
		occupied := false
		for _, key := range []string{"GroupRatio", "TopupGroupRatio", "UserUsableGroups"} {
			var option Option
			err := tx.Where("key = ?", key).First(&option).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return "", "", err
			}
			entries := map[string]any{}
			if option.Value != "" {
				if err := common.Unmarshal([]byte(option.Value), &entries); err != nil {
					return "", "", err
				}
			}
			if _, exists := entries[group]; exists {
				occupied = true
			}
		}
		if occupied {
			continue
		}
		return code, group, nil
	}
	return "", "", errors.New("no free Builder customer identifiers")
}

// groupNameSeparator joins the program to the team. An underscore matches the
// names already in use ("Builder_hub_2026_Sep_Batch") and stays safe in the
// places a group name travels: JSON option keys, config and URLs.
const groupNameSeparator = "_"

// customerPricingGroupName names the pricing group a team bills through.
//
// The team's own name is not enough on its own. A pricing group is the invoice
// counterparty, and these names arrive from another product's team list: a
// team called "UnifyAPI" or "Kingdee" can collide with a group that already
// exists here and already carries somebody else's money.
//
// So the program namespaces it. Two programs may each have a team of the same
// name without sharing an invoice, and an outside name can never select a
// group that was not created for it.
//
// The column is varchar(64) while a team name may be 120 characters, so this
// takes the most legible form that fits: the group is printed as the bill-to
// line, and an unlovely invoice beats a failed insert.
func customerPricingGroupName(program *PartnershipProgram, name, code string) string {
	if program == nil {
		return code
	}
	for _, candidate := range []string{
		program.Name + groupNameSeparator + name,
		program.Code + groupNameSeparator + name,
		program.Code + groupNameSeparator + code,
		code,
	} {
		if fitsPricingGroupColumn(candidate) {
			return candidate
		}
	}
	return code
}

func fitsPricingGroupColumn(group string) bool {
	return group != "" && len(group) <= 64 && len([]rune(group)) <= 64
}

// ProvisionBuilderCustomer atomically registers a new team and its settings.
// Existing disabled teams stay disabled, and removed rows and enrollments are
// retained. No account, grant, wallet or invoice is moved here.
func ProvisionBuilderCustomer(programName, customerName string) error {
	if !ValidBuilderProgramName(programName) || !ValidBuilderProgramName(customerName) {
		return ErrPartnershipCustomerUnavailable
	}
	builderProvisionMu.Lock()
	defer builderProvisionMu.Unlock()
	var changes []builderGroupChange
	err := DB.Transaction(func(tx *gorm.DB) error {
		return withPartnershipGroupIntegrityLock(tx, func(tx *gorm.DB) error {
			program, err := resolveProgramByName(tx, programName)
			if err != nil {
				return err
			}
			var candidates []PartnershipCustomer
			if err := tx.Where("program_id = ? AND removed_at = ?", program.Id, 0).Find(&candidates).Error; err != nil {
				return err
			}
			var existing *PartnershipCustomer
			for i := range candidates {
				if candidates[i].Name == customerName {
					if existing != nil || !candidates[i].Enabled {
						return ErrPartnershipCustomerUnavailable
					}
					existing = &candidates[i]
				}
			}
			if existing != nil {
				changes, err = registerBuilderGroupTx(tx, existing.Group)
				return err
			}
			code, group, err := builderCustomerIdentifiers(tx, program, customerName)
			if err != nil {
				return err
			}
			customer := PartnershipCustomer{ProgramId: program.Id, Name: customerName, Code: code, Group: group, Enabled: true}
			if err := ValidatePartnershipCustomer(&customer); err != nil {
				return err
			}
			changes, err = registerBuilderGroupTx(tx, group)
			if err != nil {
				return err
			}
			if err := validatePartnershipProgramGroup(tx, group); err != nil {
				return err
			}
			return tx.Create(&customer).Error
		})
	})
	if err != nil {
		return err
	}
	for _, change := range changes {
		RecordPricingConfigChange(change.key, change.old, change.value, "builder-bridge", "auto-provision-team")
		if err := updateOptionMap(change.key, change.value); err != nil {
			return err
		}
	}
	return nil
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
	// The team and its pricing group are one customer: a wallet the group
	// already bills through is this team's wallet too.
	tenantId, err := customerWalletTx(tx, customer.Group, customer.Name, slugFromName("team-"+customer.Code))
	if err != nil {
		return 0, err
	}
	if err := tx.Model(&PartnershipCustomer{}).Where("id = ?", customer.Id).
		Update("tenant_id", tenantId).Error; err != nil {
		return 0, err
	}
	return tenantId, nil
}

// claimTeamTenantOwner names the first member to connect as the tenant's
// owner. The tenant is created before any member exists, so it starts
// ownerless; leaving it that way would hide the team from views that key on
// the owner.
func claimTeamTenantOwner(tx *gorm.DB, tenantId, userId int) error {
	return tx.Model(&Tenant{}).Where("id = ? AND owner_id = ?", tenantId, 0).
		Update("owner_id", userId).Error
}
