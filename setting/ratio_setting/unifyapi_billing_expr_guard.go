/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package ratio_setting

// UNIFYAPI-FORK: a catalogued model bills only what the catalog says.
//
// A billing expression replaces "official price x discount" for its model
// outright, and /api/pricing keeps showing the catalog ratios beside it. A
// hand-written expression is therefore a second price list that nothing
// checks: gpt-5.6-sol billed gpt-5.5's $5/$30 for weeks while the console
// displayed $4/$20. So for every model in the catalog, the only expression
// allowed is the one the catalog generates, and a model the catalog prices
// flat has none.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
)

// EnforceCatalogBillingExprs installs the catalog's expressions and removes
// every other expression or tiered mode on a catalogued model. It returns the
// models whose hand-written pricing it removed.
func EnforceCatalogBillingExprs() []string {
	exprs := BillingExprs()
	billing_setting.ApplyCatalogBillingExprs(exprs)

	handWritten := map[string]bool{}
	for model := range billing_setting.GetBillingExprCopy() {
		if isCatalogued(model) && exprs[model] == "" {
			handWritten[model] = true
		}
	}
	for model, mode := range billing_setting.GetBillingModeCopy() {
		if mode == billing_setting.BillingModeTieredExpr && isCatalogued(model) && exprs[model] == "" {
			handWritten[model] = true
		}
	}

	dropped := sortedKeys(handWritten)
	billing_setting.DropBillingExprs(dropped)
	if len(dropped) > 0 {
		common.SysLog(fmt.Sprintf("billing: ignored hand-written billing expressions for catalogued models %v; they bill the catalog price", dropped))
	}
	return dropped
}

// ValidateBillingSettingOption refuses a billing_setting save that would give a
// catalogued model any pricing other than the catalog's.
func ValidateBillingSettingOption(key, value string) error {
	switch key {
	case "billing_setting." + billing_setting.BillingExprField:
		return validateBillingExprs(value)
	case "billing_setting." + billing_setting.BillingModeField:
		return validateBillingModes(value)
	}
	return nil
}

func validateBillingExprs(value string) error {
	submitted := map[string]string{}
	if err := common.UnmarshalJsonStr(value, &submitted); err != nil {
		return fmt.Errorf("billing expressions must be a JSON object of model to expression: %w", err)
	}
	catalog := BillingExprs()
	for _, model := range sortedKeys(submitted) {
		if !isCatalogued(model) {
			continue
		}
		want, managed := catalog[model]
		if !managed {
			return fmt.Errorf("%s is priced by the catalog at a flat rate and cannot have a billing expression; change the catalog instead", model)
		}
		if normalizeExpr(submitted[model]) != normalizeExpr(want) {
			return fmt.Errorf("%s: this billing expression differs from the one the catalog generates; change the catalog instead", model)
		}
	}
	return nil
}

func validateBillingModes(value string) error {
	submitted := map[string]string{}
	if err := common.UnmarshalJsonStr(value, &submitted); err != nil {
		return fmt.Errorf("billing modes must be a JSON object of model to mode: %w", err)
	}
	catalog := BillingExprs()
	for _, model := range sortedKeys(submitted) {
		if !isCatalogued(model) {
			continue
		}
		_, managed := catalog[model]
		tiered := submitted[model] == billing_setting.BillingModeTieredExpr
		if tiered != managed {
			return fmt.Errorf("%s: its billing mode is set by the catalog and cannot be changed here", model)
		}
	}
	return nil
}

func isCatalogued(model string) bool {
	_, ok := CatalogEntryFor(model)
	return ok
}

// normalizeExpr ignores whitespace, which the console's editor may reflow.
func normalizeExpr(expr string) string {
	return strings.Join(strings.Fields(expr), "")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
