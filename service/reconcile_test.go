package service

// UNIFYAPI-FORK: tests for the reconciliation arithmetic.
//
// These are the numbers an operator will use to decide whether a model is
// making money, and the ones finance will diff against a vendor invoice. The
// cases below pin the properties that make the report trustworthy: revenue
// comes from the ledger untouched, cost splits cached reads out at the vendor's
// cache price, an unpriced model is reported rather than silently costed at
// zero, and the total always equals the sum of the lines.

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

// usdToQuota converts dollars into the quota units a consume log stores, so a
// test can say "we charged $6" instead of "we charged 3000000".
func usdToQuota(usd float64) int64 {
	return int64(usd * 500_000)
}

func TestRevenueComesStraightFromTheLedger(t *testing.T) {
	// $12.50 charged must read back as $12.50. Revenue is the one column that
	// must never be re-derived: it is what the customer was actually billed.
	rows := []model.UsageRow{
		{Model: "gpt-4o", ChannelID: 1, Requests: 3, Quota: usdToQuota(12.50)},
	}
	report := Reconcile(rows, GroupByModel)
	require.Len(t, report.Lines, 1)
	require.InDelta(t, 12.50, report.Lines[0].RevenueUSD, 1e-9)
}

// TestCostSplitsCachedReadsFromFreshInput is the case that motivated promoting
// cached_tokens to a log column. Anthropic bills a cached read at 0.1x input,
// so folding cache into fresh input overstates cost by nearly 10x on
// cache-heavy traffic and turns a healthy margin into an apparent loss.
func TestCostSplitsCachedReadsFromFreshInput(t *testing.T) {
	// claude-opus-4-8: $5 in, $25 out, $0.5 cached read per 1M.
	// 1M prompt tokens of which 900k cached, plus 100k completion:
	//   fresh   100_000 / 1M x $5    = $0.50
	//   cached  900_000 / 1M x $0.50 = $0.45
	//   output  100_000 / 1M x $25   = $2.50
	//                                 -------
	//                                  $3.45
	rows := []model.UsageRow{{
		Model:            "claude-opus-4-8",
		ChannelID:        1,
		Requests:         1,
		PromptTokens:     1_000_000,
		CachedTokens:     900_000,
		CompletionTokens: 100_000,
		Quota:            usdToQuota(10),
	}}

	report := Reconcile(rows, GroupByModel)
	require.InDelta(t, 3.45, report.Lines[0].CostUSD, 1e-9)
	require.InDelta(t, 6.55, report.Lines[0].MarginUSD, 1e-9)
	require.InDelta(t, 65.5, report.Lines[0].MarginPct, 1e-9)

	// Same tokens with no cache attribution costs far more -- the exact error
	// the column removes.
	uncached := Reconcile([]model.UsageRow{{
		Model: "claude-opus-4-8", ChannelID: 1, Requests: 1,
		PromptTokens: 1_000_000, CompletionTokens: 100_000, Quota: usdToQuota(10),
	}}, GroupByModel)
	require.InDelta(t, 7.50, uncached.Lines[0].CostUSD, 1e-9)
	require.Greater(t, uncached.Lines[0].CostUSD, report.Lines[0].CostUSD)
}

// TestChannelCostRatioScalesCostOnly checks the boundary that keeps customer
// invoices reconcilable: a negotiated upstream rate changes our cost and must
// leave revenue untouched. If it ever leaked into revenue, the same request
// would bill differently depending on which channel load balancing picked.
func TestChannelCostRatioScalesCostOnly(t *testing.T) {
	require.NoError(t, ratio_setting.UpdateChannelCostRatioByJSONString(`{"7": 0.8}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateChannelCostRatioByJSONString(`{}`))
	})

	row := model.UsageRow{
		Model: "gpt-4o", Requests: 1,
		PromptTokens: 1_000_000, CompletionTokens: 1_000_000,
		Quota: usdToQuota(20),
	}

	atList := Reconcile([]model.UsageRow{withChannel(row, 1)}, GroupByModel)
	discounted := Reconcile([]model.UsageRow{withChannel(row, 7)}, GroupByModel)

	// gpt-4o is $2.50 in + $10 out = $12.50 at list.
	require.InDelta(t, 12.50, atList.Lines[0].CostUSD, 1e-9)
	require.InDelta(t, 10.00, discounted.Lines[0].CostUSD, 1e-9)
	require.InDelta(t, atList.Lines[0].RevenueUSD, discounted.Lines[0].RevenueUSD, 1e-9,
		"an upstream discount must never change what the customer was charged")
}

func withChannel(row model.UsageRow, channelID int) model.UsageRow {
	row.ChannelID = channelID
	return row
}

// TestUnpricedModelIsReportedNotCostedAtZero guards against the most dangerous
// possible bug in a margin report: traffic we cannot cost silently appearing as
// pure profit.
func TestUnpricedModelIsReportedNotCostedAtZero(t *testing.T) {
	rows := []model.UsageRow{{
		Model: "some-model-we-never-catalogued", ChannelID: 1, Requests: 4,
		PromptTokens: 500_000, CompletionTokens: 500_000, Quota: usdToQuota(9),
	}}

	report := Reconcile(rows, GroupByModel)
	require.Len(t, report.Lines, 1)
	require.Zero(t, report.Lines[0].CostUSD, "an unknown model has no modellable cost")
	require.EqualValues(t, 4, report.Lines[0].UnpricedRequests)
	require.Equal(t, []string{"some-model-we-never-catalogued"}, report.Lines[0].UnpricedModels)
	require.EqualValues(t, 4, report.Total.UnpricedRequests)
}

// TestLossMakersSurfaceModelsBillingBelowCost is the report's headline job: a
// mispriced model is invisible in a revenue chart and obvious here.
func TestLossMakersSurfaceModelsBillingBelowCost(t *testing.T) {
	rows := []model.UsageRow{
		// Sold at list: healthy.
		{Model: "gpt-4o", ChannelID: 1, Requests: 1,
			PromptTokens: 1_000_000, CompletionTokens: 0, Quota: usdToQuota(2.50)},
		// Sold at the drifted 0.085x that was live on production.
		{Model: "claude-opus-4-8", ChannelID: 1, Requests: 1,
			PromptTokens: 1_000_000, CompletionTokens: 0, Quota: usdToQuota(0.425)},
	}

	report := Reconcile(rows, GroupByModel)
	require.Len(t, report.LossMakers, 1)
	require.Equal(t, "claude-opus-4-8", report.LossMakers[0].Key)
	require.InDelta(t, -4.575, report.LossMakers[0].MarginUSD, 1e-9)
}

// TestTotalEqualsTheSumOfLines is a property a finance report must hold no
// matter which dimension it was grouped by, or nobody can check it by hand.
func TestTotalEqualsTheSumOfLines(t *testing.T) {
	rows := []model.UsageRow{
		{Day: "2026-08-01", Model: "gpt-4o", UserGroup: "Vip User", Username: "acme", UserID: 1,
			ChannelID: 1, ChannelName: "openai-direct", Requests: 5,
			PromptTokens: 300_000, CachedTokens: 100_000, CompletionTokens: 50_000, Quota: usdToQuota(4)},
		{Day: "2026-08-01", Model: "claude-opus-5", UserGroup: "Vip User", Username: "acme", UserID: 1,
			ChannelID: 2, ChannelName: "anthropic-direct", Requests: 2,
			PromptTokens: 100_000, CompletionTokens: 20_000, Quota: usdToQuota(3)},
		{Day: "2026-08-02", Model: "gpt-4o", UserGroup: "Standard User", Username: "globex", UserID: 2,
			ChannelID: 1, ChannelName: "openai-direct", Requests: 9,
			PromptTokens: 900_000, CompletionTokens: 90_000, Quota: usdToQuota(7)},
	}

	for _, groupBy := range []GroupBy{
		GroupByModel, GroupByChannel, GroupByCustomer, GroupByUserTier, GroupByDay, GroupByVendor,
	} {
		t.Run(string(groupBy), func(t *testing.T) {
			report := Reconcile(rows, groupBy)

			var revenue, cost float64
			var requests int64
			for _, line := range report.Lines {
				revenue += line.RevenueUSD
				cost += line.CostUSD
				requests += line.Requests
			}
			require.InDelta(t, revenue, report.Total.RevenueUSD, 1e-9)
			require.InDelta(t, cost, report.Total.CostUSD, 1e-9)
			require.EqualValues(t, requests, report.Total.Requests)
			require.EqualValues(t, 16, report.Total.Requests)
			require.InDelta(t, 14, report.Total.RevenueUSD, 1e-9)
		})
	}
}

// TestGroupingCollapsesTheRightRows checks each dimension actually folds on the
// field it names -- an easy thing to get subtly wrong and hard to notice, since
// a wrong grouping still produces a plausible-looking report.
func TestGroupingCollapsesTheRightRows(t *testing.T) {
	rows := []model.UsageRow{
		{Day: "2026-08-01", Model: "gpt-4o", UserGroup: "Vip User", Username: "acme", UserID: 1, ChannelID: 1, Requests: 1, Quota: usdToQuota(1)},
		{Day: "2026-08-02", Model: "gpt-4o", UserGroup: "Vip User", Username: "acme", UserID: 1, ChannelID: 2, Requests: 1, Quota: usdToQuota(1)},
		{Day: "2026-08-02", Model: "claude-opus-5", UserGroup: "Standard User", Username: "globex", UserID: 2, ChannelID: 2, Requests: 1, Quota: usdToQuota(1)},
	}

	for _, tc := range []struct {
		groupBy   GroupBy
		wantLines int
	}{
		{GroupByModel, 2},    // gpt-4o, claude-opus-5
		{GroupByChannel, 2},  // 1, 2
		{GroupByCustomer, 2}, // acme, globex
		{GroupByUserTier, 2}, // Vip, Standard
		{GroupByDay, 2},      // 08-01, 08-02
		{GroupByVendor, 2},   // openai, anthropic
	} {
		t.Run(string(tc.groupBy), func(t *testing.T) {
			require.Len(t, Reconcile(rows, tc.groupBy).Lines, tc.wantLines)
		})
	}
}

func TestGroupByVendorUsesTheCatalogVendor(t *testing.T) {
	rows := []model.UsageRow{
		{Model: "gpt-4o", ChannelID: 1, Requests: 1, Quota: usdToQuota(1)},
		{Model: "gpt-4o-mini", ChannelID: 1, Requests: 1, Quota: usdToQuota(1)},
		{Model: "claude-opus-5", ChannelID: 1, Requests: 1, Quota: usdToQuota(1)},
		{Model: "seedance-2.0", ChannelID: 1, Requests: 1, Quota: usdToQuota(1)},
	}

	report := Reconcile(rows, GroupByVendor)
	byKey := map[string]ReconcileLine{}
	for _, line := range report.Lines {
		byKey[line.Key] = line
	}

	require.EqualValues(t, 2, byKey["openai"].Requests, "both gpt models roll up to openai")
	require.EqualValues(t, 1, byKey["anthropic"].Requests)
	require.EqualValues(t, 1, byKey["unlisted"].Requests,
		"a catalog entry with no vendor must land in 'unlisted', not vanish")
}

// TestMarginPctIsUndefinedWithoutRevenue -- reporting -100% or an infinity for
// a line that cost money and earned none would both be lies, and either would
// poison a sorted report.
func TestMarginPctIsUndefinedWithoutRevenue(t *testing.T) {
	report := Reconcile([]model.UsageRow{{
		Model: "gpt-4o", ChannelID: 1, Requests: 1,
		PromptTokens: 1_000_000, Quota: 0,
	}}, GroupByModel)

	require.InDelta(t, 2.50, report.Lines[0].CostUSD, 1e-9)
	require.InDelta(t, -2.50, report.Lines[0].MarginUSD, 1e-9)
	require.Zero(t, report.Lines[0].MarginPct)
}

func TestParseGroupByRejectsUnknownDimensions(t *testing.T) {
	got, err := ParseGroupBy("")
	require.NoError(t, err)
	require.Equal(t, GroupByModel, got, "an omitted dimension defaults to model")

	_, err = ParseGroupBy("tenant")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown group_by")
}

// TestCompareVendorInvoicesClassifiesEachOutcome covers the four states finance
// cares about, including the two asymmetric ones that are easy to drop: a
// vendor we modelled but were not invoiced for, and the reverse.
func TestCompareVendorInvoicesClassifiesEachOutcome(t *testing.T) {
	report := Reconcile([]model.UsageRow{
		// $2.50 modelled for openai.
		{Model: "gpt-4o", ChannelID: 1, Requests: 1, PromptTokens: 1_000_000, Quota: usdToQuota(3)},
		// $5.00 modelled for anthropic.
		{Model: "claude-opus-5", ChannelID: 1, Requests: 1, PromptTokens: 1_000_000, Quota: usdToQuota(6)},
	}, GroupByVendor)

	variances := CompareVendorInvoices(report, map[string]float64{
		"openai":    2.51,  // within tolerance
		"anthropic": 6.00,  // 20% over the model
		"google":    12.00, // invoiced, nothing modelled
	})

	byVendor := map[string]VendorVariance{}
	for _, variance := range variances {
		byVendor[variance.Vendor] = variance
	}

	require.Equal(t, "reconciled", byVendor["openai"].Verdict)
	require.InDelta(t, 0.01, byVendor["openai"].VarianceUSD, 1e-9)

	require.Contains(t, byVendor["anthropic"].Verdict, "invoice exceeds the model")
	require.InDelta(t, 20, byVendor["anthropic"].VariancePct, 1e-9)

	require.Contains(t, byVendor["google"].Verdict, "invoiced but nothing modelled")
	require.InDelta(t, 12, byVendor["google"].VarianceUSD, 1e-9)
}

func TestCompareVendorInvoicesFlagsAMissingInvoice(t *testing.T) {
	report := Reconcile([]model.UsageRow{
		{Model: "gpt-4o", ChannelID: 1, Requests: 1, PromptTokens: 1_000_000, Quota: usdToQuota(3)},
	}, GroupByVendor)

	variances := CompareVendorInvoices(report, map[string]float64{})
	require.Len(t, variances, 1)
	require.Contains(t, variances[0].Verdict, "modelled but not invoiced")
}

// TestCompareVendorInvoicesNeedsVendorGrouping -- comparing per-model cost
// against a per-vendor invoice would silently produce nonsense, so the
// comparison refuses any other grouping instead.
func TestCompareVendorInvoicesNeedsVendorGrouping(t *testing.T) {
	report := Reconcile([]model.UsageRow{
		{Model: "gpt-4o", ChannelID: 1, Requests: 1, Quota: usdToQuota(1)},
	}, GroupByModel)
	require.Nil(t, CompareVendorInvoices(report, map[string]float64{"openai": 1}))
}
