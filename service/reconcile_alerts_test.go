package service

// UNIFYAPI-FORK: tests for the alert rules.
//
// These decide what a nightly run shouts about, so the failure modes are:
// staying quiet about a model that is losing money, and crying wolf so often
// that nobody reads the list. Both are tested.

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func healthyRows() []model.UsageRow {
	// gpt-4o at official list price costs $2.50/1M in. Charging $10 against
	// 1M input tokens is a 75% margin.
	return []model.UsageRow{{
		Model: "gpt-4o", ChannelID: 1, Requests: 100,
		PromptTokens: 1_000_000, Quota: usdToQuota(10),
	}}
}

func TestNoAlertsOnAHealthyPeriod(t *testing.T) {
	report := Reconcile(healthyRows(), GroupByModel)
	require.Empty(t, EvaluateReconcileAlerts(report, DefaultAlertThresholds()),
		"a comfortable margin must not generate noise")
}

// TestLossMakingLineIsCritical is the finding the whole feature exists for.
func TestLossMakingLineIsCritical(t *testing.T) {
	// claude-opus-4-8 costs $5/1M in. Charging $4.25 is the 0.85 discount that
	// was live on production while we bought at list -- a loss on every request.
	report := Reconcile([]model.UsageRow{{
		Model: "claude-opus-4-8", ChannelID: 1, Requests: 50,
		PromptTokens: 1_000_000, Quota: usdToQuota(4.25),
	}}, GroupByModel)

	alerts := EvaluateReconcileAlerts(report, DefaultAlertThresholds())
	require.Len(t, alerts, 1)
	require.Equal(t, AlertCritical, alerts[0].Severity)
	require.Equal(t, "loss-making", alerts[0].Kind)
	require.Equal(t, "claude-opus-4-8", alerts[0].Subject)
	require.Contains(t, alerts[0].Detail, "losing $0.75")
	require.Contains(t, alerts[0].Action, "deeper than the purchasing discount")
}

func TestThinMarginIsAWarningNotCritical(t *testing.T) {
	// $2.50 cost, charge $2.65 -> 5.7% margin, under the 10% floor but positive.
	report := Reconcile([]model.UsageRow{{
		Model: "gpt-4o", ChannelID: 1, Requests: 10,
		PromptTokens: 1_000_000, Quota: usdToQuota(2.65),
	}}, GroupByModel)

	alerts := EvaluateReconcileAlerts(report, DefaultAlertThresholds())
	require.Len(t, alerts, 1)
	require.Equal(t, AlertWarning, alerts[0].Severity)
	require.Equal(t, "thin-margin", alerts[0].Kind)
	require.Contains(t, alerts[0].Detail, "below the 10.0% floor")
}

// TestThinMarginBoundary pins the comparison as strictly-below, so a line
// exactly at the floor is acceptable rather than permanently warned about.
//
// The numbers are chosen to land exactly on 10%: quota is an integer, so a
// revenue of 2.5/0.9 truncates to 9.99994% and would test integer rounding
// rather than the rule. gpt-4o is $2.50/1M in, so 3.6M prompt tokens cost
// exactly $9.00, and $10.00 of revenue is exactly a 10% margin.
func TestThinMarginBoundary(t *testing.T) {
	thresholds := DefaultAlertThresholds()

	atFloor := Reconcile([]model.UsageRow{{
		Model: "gpt-4o", ChannelID: 1, Requests: 1,
		PromptTokens: 3_600_000, Quota: usdToQuota(10),
	}}, GroupByModel)
	require.InDelta(t, 10, atFloor.Total.MarginPct, 1e-9, "fixture must sit exactly on the floor")
	require.Empty(t, EvaluateReconcileAlerts(atFloor, thresholds),
		"exactly at the floor is acceptable; warning here would never clear")

	justUnder := Reconcile([]model.UsageRow{{
		Model: "gpt-4o", ChannelID: 1, Requests: 1,
		PromptTokens: 3_600_000, Quota: usdToQuota(9.99),
	}}, GroupByModel)
	alerts := EvaluateReconcileAlerts(justUnder, thresholds)
	require.Len(t, alerts, 1)
	require.Equal(t, "thin-margin", alerts[0].Kind)
}

// TestTinyLinesAreSuppressed keeps the list a priority order. Without a revenue
// floor, one request against an oddly priced model outranks nothing and clutters
// everything.
func TestTinyLinesAreSuppressed(t *testing.T) {
	report := Reconcile([]model.UsageRow{{
		Model: "gpt-4o", ChannelID: 1, Requests: 1,
		PromptTokens: 1000, Quota: usdToQuota(0.0001),
	}}, GroupByModel)

	require.Empty(t, EvaluateReconcileAlerts(report, DefaultAlertThresholds()),
		"a line earning a hundredth of a cent is noise, not a finding")
}

// TestUnpricedTrafficAlertsRegardlessOfSize -- an uncatalogued model is a
// configuration hole, and its line's margin is overstated, so it is reported
// even below the revenue floor.
func TestUnpricedTrafficAlertsRegardlessOfSize(t *testing.T) {
	report := Reconcile([]model.UsageRow{{
		Model: "never-catalogued", ChannelID: 1, Requests: 1,
		PromptTokens: 100, Quota: usdToQuota(0.00001),
	}}, GroupByModel)

	alerts := EvaluateReconcileAlerts(report, DefaultAlertThresholds())
	require.Len(t, alerts, 1)
	require.Equal(t, AlertCritical, alerts[0].Severity)
	require.Equal(t, "unpriced-traffic", alerts[0].Kind)
	require.Contains(t, alerts[0].Detail, "never-catalogued")
	require.Contains(t, alerts[0].Detail, "margin is overstated")
}

// TestAlertsAreOrderedByMoneyAtStake -- the top of the list has to be the thing
// to fix first, or the list is just a set.
func TestAlertsAreOrderedByMoneyAtStake(t *testing.T) {
	report := Reconcile([]model.UsageRow{
		// Small loss: $2.50 cost, $2.00 revenue -> -$0.50.
		{Model: "gpt-4o", ChannelID: 1, Requests: 1,
			PromptTokens: 1_000_000, Quota: usdToQuota(2.00)},
		// Big loss: $25 cost, $5 revenue -> -$20.
		{Model: "claude-opus-5", ChannelID: 1, Requests: 1,
			PromptTokens: 5_000_000, Quota: usdToQuota(5.00)},
		// Thin margin, a warning, must sort below both criticals.
		{Model: "gpt-4o-mini", ChannelID: 1, Requests: 1,
			PromptTokens: 100_000_000, Quota: usdToQuota(16.00)},
	}, GroupByModel)

	alerts := EvaluateReconcileAlerts(report, DefaultAlertThresholds())
	require.Len(t, alerts, 3)
	require.Equal(t, "claude-opus-5", alerts[0].Subject, "largest loss first")
	require.Equal(t, "gpt-4o", alerts[1].Subject)
	require.Equal(t, AlertWarning, alerts[2].Severity, "warnings sort below criticals")
}

func TestCountBySeverity(t *testing.T) {
	critical, warning := CountBySeverity([]ReconcileAlert{
		{Severity: AlertCritical}, {Severity: AlertWarning}, {Severity: AlertCritical},
	})
	require.Equal(t, 2, critical)
	require.Equal(t, 1, warning)
}

// --- vendor invoice variance ---

func TestVendorVarianceWithinToleranceIsSilent(t *testing.T) {
	alerts := EvaluateVendorVarianceAlerts([]VendorVariance{
		{Vendor: "openai", ModelledUSD: 100, InvoicedUSD: 101, VariancePct: 1, Verdict: "reconciled"},
	}, DefaultAlertThresholds())
	require.Empty(t, alerts)
}

// TestInvoiceAboveTheModelIsCritical -- an invoice we did not predict is real
// spend missing from every margin we report, which is worse than the reverse.
func TestInvoiceAboveTheModelIsCritical(t *testing.T) {
	alerts := EvaluateVendorVarianceAlerts([]VendorVariance{
		{Vendor: "anthropic", ModelledUSD: 100, InvoicedUSD: 130,
			VarianceUSD: 30, VariancePct: 30, Verdict: "invoice exceeds the model"},
	}, DefaultAlertThresholds())

	require.Len(t, alerts, 1)
	require.Equal(t, AlertCritical, alerts[0].Severity)
	require.Equal(t, "invoice-variance", alerts[0].Kind)
	require.Contains(t, alerts[0].Detail, "+30.0% gap")
}

func TestInvoiceBelowTheModelIsOnlyAWarning(t *testing.T) {
	// We are paying less than modelled: margin is better than reported. Worth
	// configuring, not worth waking anyone.
	alerts := EvaluateVendorVarianceAlerts([]VendorVariance{
		{Vendor: "google", ModelledUSD: 100, InvoicedUSD: 70,
			VarianceUSD: -30, VariancePct: -30, Verdict: "invoice below the model"},
	}, DefaultAlertThresholds())

	require.Len(t, alerts, 1)
	require.Equal(t, AlertWarning, alerts[0].Severity)
}

func TestVendorWithNoActivityIsSkipped(t *testing.T) {
	require.Empty(t, EvaluateVendorVarianceAlerts([]VendorVariance{
		{Vendor: "cohere", ModelledUSD: 0, InvoicedUSD: 0, Verdict: "no activity"},
	}, DefaultAlertThresholds()))
}
