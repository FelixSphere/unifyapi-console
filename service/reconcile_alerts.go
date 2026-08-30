package service

// UNIFYAPI-FORK: turns a reconciliation report into the short list of things
// somebody has to act on.
//
// The report itself is a table, and a table nobody opens is not a control. What
// makes reconciliation automatic is this file: a pure evaluation of "what in
// this period is wrong", so a scheduled run can persist findings and shout,
// rather than producing a CSV that sits unread until a quarter closes.
//
// Deliberately pure -- no database, no clock, no config lookups -- so every
// threshold is exercised in tests instead of only in production.

import (
	"fmt"
	"sort"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// AlertSeverity ranks a finding. Only two levels on purpose: something either
// needs acting on this week or it does not. A middle tier becomes a bucket
// everything gets filed into and nothing leaves.
type AlertSeverity string

const (
	// AlertCritical means money is being lost right now, or a number in the
	// report cannot be trusted.
	AlertCritical AlertSeverity = "critical"
	// AlertWarning means the position is thin or the data has a known gap.
	AlertWarning AlertSeverity = "warning"
)

// ReconcileAlert is one actionable finding.
type ReconcileAlert struct {
	Severity AlertSeverity `json:"severity"`
	Kind     string        `json:"kind"`
	Subject  string        `json:"subject"`
	Detail   string        `json:"detail"`
	Action   string        `json:"action"`

	RevenueUSD float64 `json:"revenue_usd"`
	CostUSD    float64 `json:"cost_usd"`
	MarginUSD  float64 `json:"margin_usd"`
	MarginPct  float64 `json:"margin_pct"`
}

// AlertThresholds are the rules. Exposed as a struct so an operator can tune
// them without editing code, and so tests can state each rule's boundary
// explicitly rather than inferring it.
type AlertThresholds struct {
	// ThinMarginPct flags a line that is profitable but not comfortably so.
	ThinMarginPct float64
	// MinRevenueUSD suppresses findings on lines too small to matter. Without
	// it, one request against an oddly priced model produces the same alert as
	// a month of real traffic, and the list stops being a priority order.
	MinRevenueUSD float64
	// VendorVariancePct is how far a vendor invoice may sit from modelled cost.
	VendorVariancePct float64
}

// DefaultAlertThresholds are deliberately conservative.
//
// ThinMarginPct is 10: at official-list pricing bought at official list the
// margin is exactly zero, so all of it comes from the purchasing discount --
// a line under 10% means the customer discount has eaten nearly all of it and
// one vendor price rise puts it underwater.
//
// MinRevenueUSD is 1: below a dollar the percentages are dominated by rounding
// and a single request.
func DefaultAlertThresholds() AlertThresholds {
	return AlertThresholds{
		ThinMarginPct:     10,
		MinRevenueUSD:     1,
		VendorVariancePct: varianceTolerancePct,
	}
}

// EvaluateReconcileAlerts finds everything worth acting on in one report.
//
// Ordered by severity then by absolute money at stake, so the top of the list is
// the thing to fix first rather than the alphabetically first model.
func EvaluateReconcileAlerts(report ReconcileReport, thresholds AlertThresholds) []ReconcileAlert {
	// With no purchasing cost configured anywhere, every model is costed at the
	// vendor's list price -- and since we also SELL at list price times a
	// discount, margin is then zero by construction on an undiscounted model and
	// negative on every discounted one. Reporting that as hundreds of
	// loss-making findings is not a signal, it is the alert list destroying its
	// own credibility on day one. The margin is not zero here, it is unmeasured;
	// say so once and stop.
	if len(ratio_setting.GetChannelCostRatioCopy()) == 0 {
		return []ReconcileAlert{{
			Severity: AlertWarning,
			Kind:     "no-cost-basis",
			Subject:  "(all channels)",
			Detail: "no channel has a purchasing cost configured, so every line is costed at the vendor's " +
				"list price and its margin is unmeasured rather than zero. Margin findings are suppressed " +
				"until at least one channel has a cost ratio.",
			Action:     "enter the negotiated rates under 系统设置 → 计费与支付 → 模型定价 → 上游采购成本",
			RevenueUSD: report.Total.RevenueUSD,
			CostUSD:    report.Total.CostUSD,
		}}
	}

	var alerts []ReconcileAlert
	dimension := string(report.GroupBy)

	for _, line := range report.Lines {
		// Traffic we cannot cost is reported first and unconditionally: its
		// margin is overstated, so every other judgement about this line is
		// unreliable. Not gated on MinRevenueUSD -- an unpriced model is a
		// configuration hole regardless of how much it earned.
		if line.UnpricedRequests > 0 {
			alerts = append(alerts, ReconcileAlert{
				Severity: AlertCritical,
				Kind:     "unpriced-traffic",
				Subject:  line.Label,
				Detail: fmt.Sprintf(
					"%d requests used models with no catalog price (%v), so their upstream cost is counted as zero and this line's margin is overstated",
					line.UnpricedRequests, line.UnpricedModels),
				Action:     "add the models to setting/ratio_setting/unifyapi_catalog.go, or stop serving them",
				RevenueUSD: line.RevenueUSD, CostUSD: line.CostUSD,
				MarginUSD: line.MarginUSD, MarginPct: line.MarginPct,
			})
		}

		if line.RevenueUSD < thresholds.MinRevenueUSD {
			continue
		}

		switch {
		case line.MarginUSD < 0:
			alerts = append(alerts, ReconcileAlert{
				Severity: AlertCritical,
				Kind:     "loss-making",
				Subject:  line.Label,
				Detail: fmt.Sprintf(
					"billed $%.4f against $%.4f of modelled upstream cost, losing $%.4f over the period",
					line.RevenueUSD, line.CostUSD, -line.MarginUSD),
				Action: "the customer discount is deeper than the purchasing discount: raise the discount, " +
					"negotiate the upstream rate, or stop selling this " + dimension,
				RevenueUSD: line.RevenueUSD, CostUSD: line.CostUSD,
				MarginUSD: line.MarginUSD, MarginPct: line.MarginPct,
			})
		case line.MarginPct < thresholds.ThinMarginPct:
			alerts = append(alerts, ReconcileAlert{
				Severity: AlertWarning,
				Kind:     "thin-margin",
				Subject:  line.Label,
				Detail: fmt.Sprintf("margin is %.1f%%, below the %.1f%% floor",
					line.MarginPct, thresholds.ThinMarginPct),
				Action:     "one vendor price rise puts this underwater; review the discount before it happens",
				RevenueUSD: line.RevenueUSD, CostUSD: line.CostUSD,
				MarginUSD: line.MarginUSD, MarginPct: line.MarginPct,
			})
		}
	}

	sortAlerts(alerts)
	return alerts
}

// EvaluateVendorVarianceAlerts turns invoice comparison into findings. Split
// from the line rules because it needs invoice data the scheduled run only has
// once someone has entered it.
func EvaluateVendorVarianceAlerts(variances []VendorVariance, thresholds AlertThresholds) []ReconcileAlert {
	var alerts []ReconcileAlert
	for _, variance := range variances {
		if variance.ModelledUSD == 0 && variance.InvoicedUSD == 0 {
			continue
		}
		if abs(variance.VariancePct) <= thresholds.VendorVariancePct {
			continue
		}

		severity := AlertWarning
		// An invoice above the model means real spend we are not accounting
		// for, which understates cost everywhere it is used.
		if variance.VarianceUSD > 0 {
			severity = AlertCritical
		}

		alerts = append(alerts, ReconcileAlert{
			Severity: severity,
			Kind:     "invoice-variance",
			Subject:  variance.Vendor,
			Detail: fmt.Sprintf("invoice $%.2f vs modelled $%.2f, a %+.1f%% gap: %s",
				variance.InvoicedUSD, variance.ModelledUSD, variance.VariancePct, variance.Verdict),
			Action:  "check the channel cost ratios for this vendor, and whether all its traffic is attributed to it",
			CostUSD: variance.ModelledUSD,
		})
	}
	sortAlerts(alerts)
	return alerts
}

// sortAlerts puts critical first, then the largest money at stake. Within a
// severity, "largest" is by absolute margin: a $900 loss outranks a $3 loss and
// a thin margin on a big line outranks a thin margin on a small one.
func sortAlerts(alerts []ReconcileAlert) {
	sort.SliceStable(alerts, func(i, j int) bool {
		if (alerts[i].Severity == AlertCritical) != (alerts[j].Severity == AlertCritical) {
			return alerts[i].Severity == AlertCritical
		}
		return abs(alerts[i].MarginUSD) > abs(alerts[j].MarginUSD)
	})
}

// CountBySeverity summarises a finding list for a notification subject line.
func CountBySeverity(alerts []ReconcileAlert) (critical, warning int) {
	for _, alert := range alerts {
		if alert.Severity == AlertCritical {
			critical++
			continue
		}
		warning++
	}
	return critical, warning
}
