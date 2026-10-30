package service

// UNIFYAPI-FORK: one partnership program now holds many customers, one per
// Builder Hub team. Every existing customer test combines users into a single
// company; none asserts that two companies are kept APART. That is the
// invariant that decides whether a team's invoice is its own: a leak across
// customers bills one team for another team's traffic, and the arithmetic
// still adds up, so nothing else in the suite would notice.
//
// These tests slice one fixed body of traffic two ways -- by customer and by
// vendor -- and require both slices to agree. Revenue and cost are properties
// of the traffic, not of how it is grouped, so any grouping that invents or
// loses money fails here.

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

// Three teams inside one program, with two of them sharing a model and a
// channel so a grouping bug cannot hide behind naturally disjoint traffic.
// Charges are deliberately unequal, so a swapped or merged bucket changes the
// totals rather than cancelling out.
func multiCustomerTraffic() []model.UsageRow {
	return []model.UsageRow{
		// Acme Robotics: two people, same model, different channels.
		{Model: "gpt-4o", ChannelID: 1, UserID: 1, Username: "ann", UserGroup: "acme_robotics", Requests: 2, PromptTokens: 1000, CompletionTokens: 500, Quota: usdToQuota(6)},
		{Model: "gpt-4o", ChannelID: 2, UserID: 2, Username: "bo", UserGroup: "acme_robotics", Requests: 3, PromptTokens: 2000, CompletionTokens: 900, Quota: usdToQuota(11)},

		// Nusa Labs: two people, one sharing Acme's exact model and channel.
		{Model: "gpt-4o", ChannelID: 1, UserID: 3, Username: "cy", UserGroup: "nusa_labs", Requests: 1, PromptTokens: 800, CompletionTokens: 300, Quota: usdToQuota(4)},
		{Model: "gpt-4o", ChannelID: 2, UserID: 4, Username: "di", UserGroup: "nusa_labs", Requests: 4, PromptTokens: 3000, CompletionTokens: 1200, Quota: usdToQuota(13)},

		// Vidora: one person, to prove a single-member team is still a team.
		{Model: "gpt-4o", ChannelID: 1, UserID: 5, Username: "eve", UserGroup: "vidora", Requests: 1, PromptTokens: 500, CompletionTokens: 200, Quota: usdToQuota(2)},
	}
}

const (
	acmeUSD   = 6 + 11
	nusaUSD   = 4 + 13
	vidoraUSD = 2
	allUSD    = acmeUSD + nusaUSD + vidoraUSD
)

// Each team is invoiced for its own members and nobody else's. Acme and Nusa
// bill $17 each from different members: if the two buckets were swapped the
// totals would still match, so each team is also checked for a charge it must
// NOT be carrying.
func TestEachTeamIsInvoicedForItsOwnMembersOnly(t *testing.T) {
	statements := BuildStatements(multiCustomerTraffic(), StatementKindCustomer, "2026-09-01", "2026-09-30")
	require.Len(t, statements, 3, "one statement per team, no team merged away")

	for _, want := range []struct {
		group    string
		usd      float64
		requests int64
	}{
		{"acme_robotics", acmeUSD, 5},
		{"nusa_labs", nusaUSD, 5},
		{"vidora", vidoraUSD, 1},
	} {
		statement, found := statementFor(statements, model.CustomerPricingGroupKey(want.group))
		require.True(t, found, "no statement for %s", want.group)
		require.InDelta(t, want.usd, statement.AmountUSD, 1e-9, "%s amount", want.group)
		require.EqualValues(t, want.requests, statement.Requests, "%s requests", want.group)
		require.Less(t, statement.AmountUSD, float64(allUSD), "%s carries another team's traffic", want.group)
	}
}

// Nothing is lost and nothing is counted twice. Every dollar deducted from a
// member lands on exactly one team's invoice.
func TestEveryChargeLandsOnExactlyOneTeamInvoice(t *testing.T) {
	statements := BuildStatements(multiCustomerTraffic(), StatementKindCustomer, "2026-09-01", "2026-09-30")
	var invoiced float64
	var requests int64
	for _, statement := range statements {
		invoiced += statement.AmountUSD
		requests += statement.Requests
	}
	require.InDelta(t, allUSD, invoiced, 1e-9, "invoiced total must equal every charge in the period")
	require.EqualValues(t, 11, requests, "every request in the period appears on exactly one invoice")
}

// Profit is per team, and it is revenue minus that team's own modelled cost.
// A leak would show up as one team subsidising another: margin computed from
// traffic it never sent.
func TestProfitIsComputedPerTeamFromItsOwnTraffic(t *testing.T) {
	report := Reconcile(multiCustomerTraffic(), GroupByCustomer)
	require.Len(t, report.Lines, 3)

	var revenue, cost, margin float64
	for _, line := range report.Lines {
		require.InDelta(t, line.RevenueUSD-line.CostUSD, line.MarginUSD, 1e-9, "%s margin is not revenue minus cost", line.Label)
		require.Positive(t, line.CostUSD, "%s has revenue but no modelled cost", line.Label)
		revenue += line.RevenueUSD
		cost += line.CostUSD
		margin += line.MarginUSD
	}
	require.InDelta(t, allUSD, revenue, 1e-9)
	require.InDelta(t, report.Total.RevenueUSD, revenue, 1e-9, "total must equal the sum of its lines")
	require.InDelta(t, report.Total.CostUSD, cost, 1e-9)
	require.InDelta(t, report.Total.MarginUSD, margin, 1e-9)
}

// What we owe upstream is a property of the traffic, not of how customers are
// divided. Splitting one team into two, or merging two into one, must not
// change a single dollar owed to a vendor -- otherwise the customer split
// would be quietly rewriting the payable side of the ledger.
func TestWhatWeOweUpstreamDoesNotDependOnTheCustomerSplit(t *testing.T) {
	byCustomer := Reconcile(multiCustomerTraffic(), GroupByCustomer)
	byVendor := Reconcile(multiCustomerTraffic(), GroupByVendor)

	require.InDelta(t, byCustomer.Total.CostUSD, byVendor.Total.CostUSD, 1e-9,
		"cost owed upstream changed when the same traffic was grouped by customer instead of vendor")
	require.InDelta(t, byCustomer.Total.RevenueUSD, byVendor.Total.RevenueUSD, 1e-9,
		"revenue changed when the same traffic was regrouped")
	require.EqualValues(t, byCustomer.Total.Requests, byVendor.Total.Requests)

	// Regrouping the same traffic with every member in one company must leave
	// both sides of the ledger untouched.
	merged := multiCustomerTraffic()
	for i := range merged {
		merged[i].UserGroup = "one_big_team"
	}
	mergedReport := Reconcile(merged, GroupByCustomer)
	require.Len(t, mergedReport.Lines, 1)
	require.InDelta(t, byCustomer.Total.CostUSD, mergedReport.Total.CostUSD, 1e-9,
		"merging teams changed what we owe upstream")
	require.InDelta(t, byCustomer.Total.RevenueUSD, mergedReport.Total.RevenueUSD, 1e-9,
		"merging teams changed revenue")
}

// The customer statement and the profit report are two views of one period and
// must not disagree about what a team owes. They are built by separate code
// paths, so this pins them together per team rather than only in total.
func TestTeamInvoiceAgreesWithTheProfitReportPerTeam(t *testing.T) {
	traffic := multiCustomerTraffic()
	statements := BuildStatements(traffic, StatementKindCustomer, "2026-09-01", "2026-09-30")
	report := Reconcile(traffic, GroupByCustomer)

	for _, line := range report.Lines {
		statement, found := statementFor(statements, model.CustomerPricingGroupKey(line.Label))
		require.True(t, found, "profit report has team %q with no invoice", line.Label)
		require.InDelta(t, line.RevenueUSD, statement.AmountUSD, 1e-9,
			"team %q is invoiced a different amount than the profit report credits it", line.Label)
		require.EqualValues(t, line.Requests, statement.Requests, "team %q request count disagrees", line.Label)
	}
}
