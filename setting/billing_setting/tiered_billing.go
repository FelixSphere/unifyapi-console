package billing_setting

import (
	"fmt"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/samber/lo"
)

const (
	BillingModeRatio      = "ratio"
	BillingModeTieredExpr = "tiered_expr"
	BillingModeField      = "billing_mode"
	BillingExprField      = "billing_expr"
)

// BillingSetting is managed by config.GlobalConfig.Register.
// DB keys: billing_setting.billing_mode, billing_setting.billing_expr
type BillingSetting struct {
	BillingMode map[string]string `json:"billing_mode"`
	BillingExpr map[string]string `json:"billing_expr"`
}

var billingSetting = BillingSetting{
	BillingMode: make(map[string]string),
	BillingExpr: make(map[string]string),
}

func init() {
	config.GlobalConfig.Register("billing_setting", &billingSetting)
}

// ---------------------------------------------------------------------------
// Read accessors (hot path, must be fast)
// ---------------------------------------------------------------------------

func GetBillingMode(model string) string {
	if mode, ok := billingSetting.BillingMode[model]; ok {
		return mode
	}
	return BillingModeRatio
}

func GetBillingExpr(model string) (string, bool) {
	expr, ok := billingSetting.BillingExpr[model]
	return expr, ok
}

func GetBillingModeCopy() map[string]string {
	return lo.Assign(billingSetting.BillingMode)
}

func GetBillingExprCopy() map[string]string {
	return lo.Assign(billingSetting.BillingExpr)
}

func GetPricingSyncData(base map[string]any) map[string]any {
	extra := make(map[string]any, 2)
	if modes := GetBillingModeCopy(); len(modes) > 0 {
		extra[BillingModeField] = modes
	}
	if exprs := GetBillingExprCopy(); len(exprs) > 0 {
		extra[BillingExprField] = exprs
	}
	return lo.Assign(base, extra)
}

// ---------------------------------------------------------------------------
// Smoke test (called externally for validation before save)
// ---------------------------------------------------------------------------

func SmokeTestExpr(exprStr string) error {
	return smokeTestExpr(exprStr)
}

func smokeTestExpr(exprStr string) error {
	vectors := []billingexpr.TokenParams{
		{P: 0, C: 0, Len: 0},
		{P: 1000, C: 1000, Len: 1000},
		{P: 100000, C: 100000, Len: 100000},
		{P: 1000000, C: 1000000, Len: 1000000},
	}
	requests := []billingexpr.RequestInput{
		{},
		{
			Headers: map[string]string{
				"anthropic-beta": "fast-mode-2026-02-01",
			},
			Body: []byte(`{"service_tier":"fast","stream_options":{"include_usage":true},"messages":[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21]}`),
		},
	}

	for _, v := range vectors {
		for _, request := range requests {
			result, _, err := billingexpr.RunExprWithRequest(exprStr, v, request)
			if err != nil {
				return fmt.Errorf("vector {p=%g, c=%g}: run failed: %w", v.P, v.C, err)
			}
			if result < 0 {
				return fmt.Errorf("vector {p=%g, c=%g}: result %f < 0", v.P, v.C, result)
			}
		}
	}
	return nil
}

// ApplyCatalogBillingExprs installs expressions generated from the pricing
// catalog, replacing any previous catalog-generated set.
//
// UNIFYAPI-FORK. The catalog is authoritative for what a model costs, and some
// vendor prices -- audio input above the text rate, a context tier -- cannot be
// expressed by the flat ratio maps at all. Those models are put on the
// expression engine, with their prices generated from the same catalog row
// everything else derives from.
//
// It REPLACES rather than merges, for the same reason the ratio maps are rebuilt
// rather than mutated: when a model stops needing an expression -- a vendor
// drops a tier, or a discount changes -- the stale one has to disappear, not
// linger and keep billing the old shape. Anything an admin set by hand for a
// model the catalog does not manage is left alone.
func ApplyCatalogBillingExprs(exprs map[string]string) {
	managed := make(map[string]bool, len(exprs))
	for model := range exprs {
		managed[model] = true
	}

	modes := make(map[string]string, len(billingSetting.BillingMode))
	kept := make(map[string]string, len(billingSetting.BillingExpr))
	for model, expr := range billingSetting.BillingExpr {
		// A previously catalog-managed model that no longer needs an
		// expression drops out here rather than being carried forward.
		if _, isCatalogued := catalogManagedExprs[model]; isCatalogued && !managed[model] {
			continue
		}
		if managed[model] {
			continue
		}
		kept[model] = expr
		if mode, ok := billingSetting.BillingMode[model]; ok {
			modes[model] = mode
		}
	}

	for model, expr := range exprs {
		kept[model] = expr
		modes[model] = BillingModeTieredExpr
	}

	billingSetting.BillingExpr = kept
	billingSetting.BillingMode = modes
	catalogManagedExprs = managed
}

// catalogManagedExprs remembers which models the catalog installed an
// expression for, so a later rebuild can tell "the catalog stopped managing
// this" from "an admin set this by hand".
var catalogManagedExprs = map[string]bool{}

// SetBillingExprForTest installs an expression as if an admin had written it,
// so tests can prove a catalog rebuild leaves hand-written entries alone.
func SetBillingExprForTest(model, expr string) {
	billingSetting.BillingExpr[model] = expr
	billingSetting.BillingMode[model] = BillingModeTieredExpr
}
