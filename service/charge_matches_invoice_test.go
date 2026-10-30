package service

// UNIFYAPI-FORK: the one place where what a customer is CHARGED and what a
// customer is INVOICED deliberately diverge.
//
// On the text path the wallet is settled with the gross quota:
//
//	SettleBilling(ctx, relayInfo, summary.Quota)
//
// while the ledger row that later becomes an invoice is written with a
// different expression:
//
//	Quota: CustomerChargeQuota(relayInfo, summary.Quota, other)
//
// Revenue is read straight from that ledger row and is never recomputed, so
// whatever CustomerChargeQuota returns IS the invoice. A wrong answer here
// cannot be caught downstream: the statement, the profit report and the vendor
// comparison would all agree with each other and all be wrong together.
//
// The function had no test. These pin its contract from both ends -- the
// returned number, and the invoice that number produces.

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

// Cash traffic must be invoiced for exactly what the wallet lost. This is the
// property that makes a bill defensible in a dispute: the customer's own usage
// page and the invoice are the same number because they are the same number.
func TestCashTrafficIsInvoicedForExactlyWhatTheWalletLost(t *testing.T) {
	for _, source := range []string{BillingSourceWallet, BillingSourceSubscription, ""} {
		other := map[string]interface{}{}
		charged := CustomerChargeQuota(&relaycommon.RelayInfo{BillingSource: source}, 12_345, other)
		require.Equal(t, 12_345, charged, "billing source %q must invoice the gross deduction", source)
		require.NotContains(t, other, "promotional", "cash traffic must not be marked promotional")
	}
}

// Promotional traffic is paid for out of a credit grant, not the customer's
// money, so it must reach the invoice as zero. Builder Hub's launch credit is
// exactly this: a team spending its $5 grant has real usage and a $0 bill.
func TestPromotionalTrafficIsNeverInvoiced(t *testing.T) {
	other := map[string]interface{}{}
	charged := CustomerChargeQuota(&relaycommon.RelayInfo{
		BillingSource:       BillingSourcePromotional,
		CreditPoolId:        7,
		CreditGrantId:       8,
		CreditReservationId: 9,
	}, 500_000, other)

	require.Zero(t, charged, "a free credit must never become a charge on an invoice")
}

// Zero on the invoice must not mean zero in the record. The consumption still
// happened, the grant was still drawn down, and an auditor has to be able to
// see how much and against which grant.
func TestPromotionalUsageStaysAuditableEvenThoughItIsNotCharged(t *testing.T) {
	other := map[string]interface{}{}
	CustomerChargeQuota(&relaycommon.RelayInfo{
		BillingSource:       BillingSourcePromotional,
		CreditPoolId:        7,
		CreditGrantId:       8,
		CreditReservationId: 9,
	}, 500_000, other)

	require.Equal(t, true, other["promotional"])
	require.Equal(t, 500_000, other["promotional_quota"], "the gross consumption must survive as metadata")
	require.Equal(t, 7, other["credit_pool_id"])
	require.Equal(t, 8, other["credit_grant_id"])
	require.Equal(t, 9, other["credit_reservation_id"])
}

// A nil relay info is a programming error, not free traffic. Defaulting to
// zero there would silently stop invoicing real usage, so the safe default is
// to charge the gross.
func TestMissingRelayInfoChargesRatherThanGivingTrafficAway(t *testing.T) {
	require.Equal(t, 999, CustomerChargeQuota(nil, 999, map[string]interface{}{}))
}

// The bridge from the deduction layer to the invoice layer. One team spends
// partly from its grant and partly from its own wallet; only the wallet half
// may appear on the invoice, and the grant half must leave the invoice
// untouched rather than reducing it.
func TestATeamsInvoiceShowsCashSpendAndNotItsGrant(t *testing.T) {
	const group = "acme_robotics"
	cash := CustomerChargeQuota(&relaycommon.RelayInfo{BillingSource: BillingSourceWallet}, usdToQuotaInt(20), map[string]interface{}{})
	granted := CustomerChargeQuota(&relaycommon.RelayInfo{BillingSource: BillingSourcePromotional}, usdToQuotaInt(5), map[string]interface{}{})

	rows := []model.UsageRow{
		{Model: "gpt-4o", ChannelID: 1, UserID: 1, Username: "ann", UserGroup: group, Requests: 1, Quota: int64(cash)},
		{Model: "gpt-4o", ChannelID: 1, UserID: 2, Username: "bo", UserGroup: group, Requests: 1, Quota: int64(granted)},
	}

	statements := BuildStatements(rows, StatementKindCustomer, "2026-09-01", "2026-09-30")
	require.Len(t, statements, 1)
	require.InDelta(t, 20, statements[0].AmountUSD, 1e-9,
		"the invoice must charge the cash spend only, with the grant neither added nor deducted")
	require.EqualValues(t, 2, statements[0].Requests,
		"grant-funded requests still happened and must still be counted")
}

func usdToQuotaInt(usd float64) int {
	return int(usdToQuota(usd))
}
