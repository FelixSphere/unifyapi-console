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
	"sort"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"gorm.io/gorm"
)

// A discount set on a Partnership Program is written into each of its
// customers' Customer model prices -- the per-model multipliers in
// GroupModelDiscount -- rather than being consulted at billing time.
//
// It is materialised on purpose. The operator prices customers in that editor,
// and a discount that existed only on the program would be invisible there
// while silently deciding the bill. Writing the rows means one screen shows
// what every customer actually pays, and a customer who needs their own number
// is edited in the same place afterwards.
//
// Which is the hazard this file exists to handle. Re-applying a program
// discount must not flatten a price somebody negotiated. So a row is only
// written when it is ABSENT or still carries the value this program last wrote
// -- recorded on the program as Discount. Anything else has been changed by
// hand since, and is left exactly as it is and reported back.

// ProgramDiscountResult reports what applying a program discount did, so the
// caller can tell an operator what was and was not touched instead of implying
// it all went through.
type ProgramDiscountResult struct {
	// Customers whose rows were written.
	CustomersChanged int `json:"customers_changed"`
	// Model rows written across all of them.
	ModelsWritten int `json:"models_written"`
	// Per-model prices left alone because somebody had set them by hand.
	// Rendered as "group / model" so the operator can go and look at them.
	Preserved []string `json:"preserved,omitempty"`
}

// ApplyProgramDiscount writes `next` into every enabled customer of the
// program, for every catalogued model, preserving hand-set prices.
//
// `previous` is the discount the program carried before this change; rows
// still equal to it are this program's own and may be moved. Pass 0 for a
// program that never had one -- then only absent rows are filled, which is the
// conservative reading of "apply a discount for the first time".
//
// A `next` of 0 means the operator cleared the discount: this program's rows
// are removed and hand-set ones stay.
func ApplyProgramDiscount(programId int, previous, next float64) (ProgramDiscountResult, error) {
	result := ProgramDiscountResult{}
	if programId <= 0 {
		return result, errors.New("invalid partnership program id")
	}
	if next < 0 || next > 1 {
		return result, fmt.Errorf("program discount must be between 0 and 1, got %g", next)
	}

	groups, err := programCustomerGroups(programId)
	if err != nil {
		return result, err
	}
	if len(groups) == 0 {
		return result, nil
	}

	models := ratio_setting.CatalogModels()
	if len(models) == 0 {
		return result, errors.New("the catalogue is empty, so there is nothing to price")
	}

	err = mutatePricingOption("GroupModelDiscount", "partnership-program", "apply-program-discount",
		func(raw map[string]any) bool {
			changed := false
			for _, group := range groups {
				perModel := nestedFloatMap(raw[group])
				customerChanged := false
				for _, modelName := range models {
					current, present := perModel[modelName]
					switch {
					case !present:
						if next == 0 {
							continue // nothing to clear
						}
					case sameDiscount(current, previous):
						// This program's own row. Safe to move or remove.
					default:
						// Set by hand since. Not ours to overwrite.
						result.Preserved = append(result.Preserved, group+" / "+modelName)
						continue
					}
					if next == 0 {
						delete(perModel, modelName)
					} else {
						perModel[modelName] = next
					}
					result.ModelsWritten++
					customerChanged = true
					changed = true
				}
				if customerChanged {
					result.CustomersChanged++
				}
				if len(perModel) == 0 {
					delete(raw, group)
				} else {
					raw[group] = perModel
				}
			}
			return changed
		})
	if err != nil {
		return ProgramDiscountResult{}, err
	}
	sort.Strings(result.Preserved)
	return result, nil
}

// sameDiscount compares two multipliers the way money should be compared:
// through the JSON round trip they survive, not by exact float identity.
func sameDiscount(a, b float64) bool {
	const epsilon = 1e-9
	diff := a - b
	return diff < epsilon && diff > -epsilon
}

func nestedFloatMap(value any) map[string]float64 {
	out := map[string]float64{}
	nested, ok := value.(map[string]any)
	if !ok {
		return out
	}
	for name, raw := range nested {
		switch v := raw.(type) {
		case float64:
			out[name] = v
		case int:
			out[name] = float64(v)
		}
	}
	return out
}

// programCustomerGroups lists the pricing groups of a program's live
// customers. A removed or disabled customer is skipped: repricing a team we
// have stopped serving would write a number nobody will ever bill under, and
// would resurrect it in the pricing editor.
func programCustomerGroups(programId int) ([]string, error) {
	if DB == nil {
		return nil, errors.New("no database")
	}
	groupCol := commonGroupCol
	if groupCol == "" {
		groupCol = "`group`"
	}
	var groups []string
	err := DB.Model(&PartnershipCustomer{}).
		Where("program_id = ? AND enabled = ? AND removed_at = 0", programId, true).
		Distinct(groupCol).Pluck(groupCol, &groups).Error
	if err != nil {
		return nil, err
	}
	live := groups[:0]
	for _, group := range groups {
		if group != "" {
			live = append(live, group)
		}
	}
	sort.Strings(live)
	return live, nil
}

// mutatePricingOption is ensureGroupSettingEntry with the audit actor spelled
// out. Every pricing map here replaces rather than merges on save, so the
// read-modify-write happens under the same integrity lock the admin path uses,
// the overwritten value is snapshotted for audit, and the in-memory setting is
// published only after the row commits.
func mutatePricingOption(key, actor, reason string, mutate func(map[string]any) bool) error {
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
			if !mutate(raw) {
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
	RecordPricingConfigChange(key, previous, merged, actor, reason)
	return updateOptionMap(key, merged)
}

// ReapplyProgramDiscounts fills in the rows a program's discount is missing,
// for every program that has one.
//
// It exists because the catalogue is compiled in: adding a model ships in a
// release, and on the release before it no customer had a row for that model.
// Without this, a new model would be sold at LIST to every customer in every
// discounted program until somebody noticed and re-applied by hand, per
// program. The operator's question that produced this was exactly right --
// "so I have to edit the new model at every customer by hand?" -- and the
// answer has to be no.
//
// Passing the program's own discount as BOTH previous and next is what makes
// this safe to run unattended: an absent row is filled, a row this program
// already owns is rewritten to the same value, and a price set by hand is
// preserved by the same rule that protects it during a deliberate change.
// So it is idempotent, and running it on every boot costs one option write
// only when something is genuinely missing.
func ReapplyProgramDiscounts() error {
	if DB == nil {
		return nil
	}
	if !DB.Migrator().HasTable(&PartnershipProgram{}) {
		return nil
	}
	var programs []PartnershipProgram
	if err := DB.Where("enabled = ? AND removed_at = 0 AND discount > 0", true).
		Find(&programs).Error; err != nil {
		return err
	}
	for _, program := range programs {
		result, err := ApplyProgramDiscount(program.Id, program.Discount, program.Discount)
		if err != nil {
			return fmt.Errorf("program %q: %w", program.Name, err)
		}
		if result.ModelsWritten > 0 {
			common.SysLog(fmt.Sprintf(
				"partnership program %q: filled %d customer model price(s) at %g",
				program.Name, result.ModelsWritten, program.Discount))
		}
	}
	return nil
}
