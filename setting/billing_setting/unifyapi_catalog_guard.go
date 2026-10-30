/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package billing_setting

// UNIFYAPI-FORK: removes billing expressions the catalog did not generate.

// DropBillingExprs removes the expression and the tiered billing mode of each
// model, so it bills from the flat ratio maps instead.
func DropBillingExprs(models []string) {
	if len(models) == 0 {
		return
	}
	drop := make(map[string]bool, len(models))
	for _, model := range models {
		drop[model] = true
	}

	exprs := make(map[string]string, len(billingSetting.BillingExpr))
	for model, expr := range billingSetting.BillingExpr {
		if !drop[model] {
			exprs[model] = expr
		}
	}
	modes := make(map[string]string, len(billingSetting.BillingMode))
	for model, mode := range billingSetting.BillingMode {
		if !drop[model] {
			modes[model] = mode
		}
	}

	billingSetting.BillingExpr = exprs
	billingSetting.BillingMode = modes
}
