package service

// UNIFYAPI-FORK: tests for the settlement statements.
//
// These are the numbers that leave the building -- one becomes an invoice sent
// to a customer, the other becomes a payment made to a vendor. Two failures
// matter more than the rest: a customer bill that disagrees with what was
// actually deducted, and a vendor statement that silently omits traffic it
// could not price and so under-pays.

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

func statementFor(statements []Statement, counterparty string) (Statement, bool) {
	for _, statement := range statements {
		if statement.Counterparty == counterparty {
			return statement, true
		}
	}
	return Statement{}, false
}

// TestCustomerStatementIsTheLedgerNotARecalculation is the property that makes
// a bill defensible. The amount charged is whatever quota was deducted --
// including a discount, a group ratio, or a price that has since changed. If
// the statement re-derived it from today's catalog, a customer who repriced
// mid-month would get an invoice that disagrees with their own usage page.
func TestCustomerStatementIsTheLedgerNotARecalculation(t *testing.T) {
	rows := []model.UsageRow{
		// Deliberately absurd relative to gpt-4o's list price: the point is
		// that the statement reports it anyway.
		{Day: "2026-08-01", Model: "gpt-4o", UserID: 7, Username: "acme", UserGroup: "vip",
			Requests: 2, PromptTokens: 1000, CompletionTokens: 100, Quota: usdToQuota(0.25)},
		{Day: "2026-08-02", Model: "gpt-4o", UserID: 7, Username: "acme", UserGroup: "vip",
			Requests: 3, PromptTokens: 2000, CompletionTokens: 200, Quota: usdToQuota(0.75)},
	}

	statements := BuildStatements(rows, StatementKindCustomer, "2026-08-01", "2026-08-31")
	require.Len(t, statements, 1)

	statement := statements[0]
	require.Equal(t, "vip", statement.Counterparty)
	require.Equal(t, "vip", statement.Label)
	require.Equal(t, "vip", statement.Group, "the tier is half the explanation of the amount")
	require.InDelta(t, 1.00, statement.AmountUSD, 1e-9)
	require.EqualValues(t, 5, statement.Requests)

	// Days collapse into one model line: a counterparty checking a bill asks
	// what they paid for gpt-4o, not what they paid on the 2nd.
	require.Len(t, statement.Lines, 1)
	require.Equal(t, "gpt-4o", statement.Lines[0].Model)
	require.InDelta(t, 1.00, statement.Lines[0].AmountUSD, 1e-9)
	require.EqualValues(t, 3000, statement.Lines[0].PromptTokens)
}

// TestStatementLinesSumToTheStatementTotal keeps the invoice footer honest: the
// figure a customer is asked to pay must be the sum of the lines they can check.
func TestStatementLinesSumToTheStatementTotal(t *testing.T) {
	rows := []model.UsageRow{
		{Model: "gpt-4o", UserID: 1, Username: "a", Requests: 1, Quota: usdToQuota(3)},
		{Model: "claude-opus-4-8", UserID: 1, Username: "a", Requests: 1, Quota: usdToQuota(7)},
		{Model: "gpt-5-mini", UserID: 1, Username: "a", Requests: 1, Quota: usdToQuota(0.0064)},
	}
	statements := BuildStatements(rows, StatementKindCustomer, "2026-08-01", "2026-08-31")
	require.Len(t, statements, 1)

	var sum float64
	for _, line := range statements[0].Lines {
		sum += line.AmountUSD
	}
	require.InDelta(t, statements[0].AmountUSD, sum, 1e-9)
	require.InDelta(t, 10.0064, statements[0].AmountUSD, 1e-9)
}

// TestCustomerStatementCombinesEveryUserInTheCompany. User Group is the
// customer boundary: three logins for GenAI must produce one company bill.
func TestCustomerStatementCombinesEveryUserInTheCompany(t *testing.T) {
	rows := []model.UsageRow{
		{Model: "gpt-4o", UserID: 1, Username: "Aaron", UserGroup: "GenAI", Requests: 1, Quota: usdToQuota(5)},
		{Model: "gpt-4o", UserID: 2, Username: "Joshua", UserGroup: "GenAI", Requests: 1, Quota: usdToQuota(9)},
		{Model: "gpt-4o", UserID: 3, Username: "Chris", UserGroup: "UnifyAI", Requests: 1, Quota: usdToQuota(4)},
	}
	statements := BuildStatements(rows, StatementKindCustomer, "2026-08-01", "2026-08-31")
	require.Len(t, statements, 2)

	require.Equal(t, "GenAI", statements[0].Counterparty)
	require.InDelta(t, 14, statements[0].AmountUSD, 1e-9)
	require.EqualValues(t, 2, statements[0].Requests)
	require.Equal(t, "UnifyAI", statements[1].Counterparty)
}

// TestVendorStatementIsModelledAndTracksTheChannelRatio. The vendor side is the
// opposite of the customer side: nothing is read, everything is computed, and
// the negotiated purchasing rate has to be in it or the statement is list price
// pretending to be a bill.
func TestVendorStatementIsModelledAndTracksTheChannelRatio(t *testing.T) {
	require.NoError(t, ratio_setting.UpdateChannelCostRatioByJSONString(`{"3": 0.5}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateChannelCostRatioByJSONString(`{}`))
	})

	// gpt-4o at list: 1M prompt + 1M completion.
	rows := []model.UsageRow{{
		Model: "gpt-4o", ChannelID: 3, UserID: 1, Username: "a", Requests: 1,
		ChannelName: "flatkey-primary", ChannelBaseURL: "https://api.flatkey.ai/v1",
		PromptTokens: 1_000_000, CompletionTokens: 1_000_000, Quota: usdToQuota(100),
	}}

	discounted := BuildStatements(rows, StatementKindVendor, "2026-08-01", "2026-08-31")
	require.Len(t, discounted, 1)

	rows[0].ChannelID = 99 // no ratio configured: costed at list
	list := BuildStatements(rows, StatementKindVendor, "2026-08-01", "2026-08-31")

	// Absolute figures, not just the ratio between them: gpt-4o lists at $2.50
	// in and $10 out per 1M, so 1M+1M is $12.50 at list and $6.25 at half.
	// Asserting only that one is half the other would pass with both at zero,
	// which is exactly what a broken catalog lookup produces.
	require.InDelta(t, 12.50, list[0].AmountUSD, 1e-9)
	require.InDelta(t, 6.25, discounted[0].AmountUSD, 1e-9)
	require.Equal(t, "flatkey", discounted[0].Counterparty)
	require.Equal(t, "Flatkey", discounted[0].Label)
	require.Equal(t, "https://api.flatkey.ai/v1", discounted[0].Lines[0].ChannelBaseURL)
	require.Equal(t, "flatkey-primary", discounted[0].Lines[0].ChannelName)
	require.InDelta(t, 0.5, discounted[0].Lines[0].CostRatio, 1e-9)

	// And the customer side of the very same rows is untouched by it: routing
	// picks a channel, so a channel's cost must never reach a customer's bill.
	customer := BuildStatements(rows, StatementKindCustomer, "2026-08-01", "2026-08-31")
	require.InDelta(t, 100, customer[0].AmountUSD, 1e-9)
}

// TestVendorStatementFlagsTrafficItCouldNotPrice. This is the one that costs
// money if it is wrong. An uncatalogued model contributes nothing to the
// modelled total, so a statement that hid it would understate what we owe and
// the operator would under-pay an invoice without ever seeing why.
func TestVendorStatementFlagsTrafficItCouldNotPrice(t *testing.T) {
	rows := []model.UsageRow{
		{Model: "gpt-4o", ChannelID: 1, ChannelBaseURL: "https://openrouter.ai/api", UserID: 1, Username: "a", Requests: 2,
			PromptTokens: 1_000_000, CompletionTokens: 0, Quota: usdToQuota(10)},
		{Model: "not-in-the-catalog", ChannelID: 1, ChannelBaseURL: "https://openrouter.ai/api", UserID: 1, Username: "a", Requests: 5,
			PromptTokens: 9_000_000, CompletionTokens: 9_000_000, Quota: usdToQuota(50)},
	}

	statements := BuildStatements(rows, StatementKindVendor, "2026-08-01", "2026-08-31")

	openrouter, found := statementFor(statements, "openrouter")
	require.True(t, found, "all traffic through the reseller belongs on its payable")
	require.False(t, openrouter.Complete())
	require.EqualValues(t, 5, openrouter.UnpricedRequests)
	require.Equal(t, []string{"not-in-the-catalog"}, openrouter.UnpricedModels)
	require.InDelta(t, 2.50, openrouter.AmountUSD, 1e-9)
	require.True(t, openrouter.Lines[1].Unpriced)
}

func TestVendorLinesPreserveModelChannelAndCostRatio(t *testing.T) {
	require.NoError(t, ratio_setting.UpdateChannelCostRatioByJSONString(`{"8":0.7,"9":0.9}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateChannelCostRatioByJSONString(`{}`)) })
	rows := []model.UsageRow{
		{Model: "gpt-4o", ChannelID: 8, ChannelName: "or-a", ChannelBaseURL: "https://openrouter.ai/api", Requests: 1, PromptTokens: 1000},
		{Model: "gpt-4o", ChannelID: 9, ChannelName: "or-b", ChannelBaseURL: "https://openrouter.ai/api", Requests: 2, PromptTokens: 2000},
	}
	statements := BuildStatements(rows, StatementKindVendor, "2026-08-01", "2026-08-31")
	require.Len(t, statements, 1)
	require.Len(t, statements[0].Lines, 2)
	byChannel := map[int]StatementLine{}
	for _, line := range statements[0].Lines {
		byChannel[line.ChannelID] = line
	}
	require.Equal(t, "or-a", byChannel[8].ChannelName)
	require.InDelta(t, 0.7, byChannel[8].CostRatio, 1e-9)
	require.EqualValues(t, 2, byChannel[9].Requests)
	require.InDelta(t, 0.9, byChannel[9].CostRatio, 1e-9)
}

func TestSupplierRollupUsesChannelHostNeverModelAuthor(t *testing.T) {
	rows := []model.UsageRow{
		// Different model authors, one Flatkey payable.
		{Model: "gpt-4o", ChannelID: 1, ChannelBaseURL: "https://console.flatkey.ai/", Requests: 1, PromptTokens: 1000},
		{Model: "claude-opus-5", ChannelID: 1, ChannelBaseURL: "https://console.flatkey.ai/v1", Requests: 2, PromptTokens: 2000},
		// Same model author, a different actual supplier.
		{Model: "gpt-4o", ChannelID: 2, ChannelBaseURL: "https://openrouter.ai", Requests: 3, PromptTokens: 3000},
		{Model: "gpt-4o", ChannelID: 3, ChannelBaseURL: "https://openrouter.ai/api", Requests: 4, PromptTokens: 4000},
		// Unknown hosts stay independent.
		{Model: "gpt-4o", ChannelID: 4, ChannelBaseURL: "https://API.Reseller.Example/v1", Requests: 5, PromptTokens: 5000},
	}
	statements := BuildStatements(rows, StatementKindVendor, "2026-08-01", "2026-08-31")
	require.Len(t, statements, 3)

	flatkey, found := statementFor(statements, "flatkey")
	require.True(t, found)
	require.Equal(t, "Flatkey", flatkey.Label)
	require.EqualValues(t, 3, flatkey.Requests)
	require.Len(t, flatkey.Lines, 2)

	openrouter, found := statementFor(statements, "openrouter")
	require.True(t, found)
	require.Equal(t, "OpenRouter", openrouter.Label)
	require.EqualValues(t, 7, openrouter.Requests,
		"root and /api paths on openrouter.ai are one supplier")
	require.Len(t, openrouter.Lines, 2, "channel details stay separate below the supplier")

	unknown, found := statementFor(statements, "api.reseller.example")
	require.True(t, found)
	require.Equal(t, "api.reseller.example", unknown.Label)
	require.EqualValues(t, 5, unknown.Requests)
}

func TestStatementTotalEqualsEveryModelChannelDetail(t *testing.T) {
	rows := []model.UsageRow{
		{Model: "gpt-4o", ChannelID: 1, ChannelBaseURL: "https://console.flatkey.ai", Requests: 1, PromptTokens: 1_000_000},
		{Model: "gpt-4o", ChannelID: 2, ChannelBaseURL: "https://console.flatkey.ai/api", Requests: 2, CompletionTokens: 1_000_000},
		{Model: "claude-opus-5", ChannelID: 1, ChannelBaseURL: "https://console.flatkey.ai", Requests: 3, PromptTokens: 1_000_000},
	}
	statement, found := statementFor(
		BuildStatements(rows, StatementKindVendor, "2026-08-01", "2026-08-31"), "flatkey")
	require.True(t, found)
	var amount float64
	var requests int64
	for _, line := range statement.Lines {
		amount += line.AmountUSD
		requests += line.Requests
	}
	require.InDelta(t, statement.AmountUSD, amount, 1e-9)
	require.Equal(t, statement.Requests, requests)
}

func TestValidateClosedCalendarMonth(t *testing.T) {
	today := time.Date(2026, time.September, 15, 9, 0, 0, 0, time.Local)
	require.NoError(t, ValidateClosedCalendarMonth("2026-08-01", "2026-08-31", today))
	require.Error(t, ValidateClosedCalendarMonth("2026-09-01", "2026-09-30", today), "open month")
	require.Error(t, ValidateClosedCalendarMonth("2026-08-02", "2026-08-31", today), "partial month")
	require.Error(t, ValidateClosedCalendarMonth("2026-08-01", "2026-09-01", today), "cross-month range")
}

// TestCustomerStatementIsNeverIncomplete. An uncatalogued model has no cost we
// can model, but it always has a quota that was deducted. The customer side
// must therefore never report unpriced traffic -- if it ever did, we would be
// under-billing.
func TestCustomerStatementIsNeverIncomplete(t *testing.T) {
	rows := []model.UsageRow{{
		Model: "not-in-the-catalog", ChannelID: 1, UserID: 4, Username: "d",
		Requests: 5, Quota: usdToQuota(50),
	}}
	statements := BuildStatements(rows, StatementKindCustomer, "2026-08-01", "2026-08-31")
	require.True(t, statements[0].Complete())
	require.Zero(t, statements[0].UnpricedRequests)
	require.InDelta(t, 50, statements[0].AmountUSD, 1e-9)
}

// TestStatementsAgreeWithTheProfitScreen. The bill and the dashboard are folds
// over the same rows; if they ever diverged, the first person to notice would
// be a customer holding an invoice.
func TestStatementsAgreeWithTheProfitScreen(t *testing.T) {
	rows := []model.UsageRow{
		{Model: "gpt-4o", ChannelID: 1, UserID: 1, Username: "a", Requests: 4,
			PromptTokens: 500_000, CompletionTokens: 50_000, Quota: usdToQuota(4)},
		{Model: "claude-opus-4-8", ChannelID: 2, UserID: 2, Username: "b", Requests: 6,
			PromptTokens: 800_000, CachedTokens: 600_000, CompletionTokens: 90_000,
			Quota: usdToQuota(11)},
	}

	customer := SumStatements(BuildStatements(rows, StatementKindCustomer, "2026-08-01", "2026-08-31"))
	vendor := SumStatements(BuildStatements(rows, StatementKindVendor, "2026-08-01", "2026-08-31"))
	report := Reconcile(rows, GroupByModel)

	require.InDelta(t, report.Total.RevenueUSD, customer.AmountUSD, 1e-9,
		"what we bill customers must equal the revenue on the profit screen")
	require.InDelta(t, report.Total.CostUSD, vendor.AmountUSD, 1e-9,
		"what we owe upstream must equal the cost on the profit screen")
	require.InDelta(t, report.Total.MarginUSD, customer.AmountUSD-vendor.AmountUSD, 1e-9)
}

func TestParseStatementKind(t *testing.T) {
	for _, raw := range []string{"customer", "vendor"} {
		kind, ok := ParseStatementKind(raw)
		require.True(t, ok)
		require.EqualValues(t, raw, kind)
	}
	kind, ok := ParseStatementKind("")
	require.True(t, ok)
	require.Equal(t, StatementKindCustomer, kind, "the safe default is the read-only side")

	_, ok = ParseStatementKind("supplier")
	require.False(t, ok)
}
