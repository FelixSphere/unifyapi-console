/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

// UNIFYAPI-FORK: tests for the settlement record.
//
// The record exists to make a period stop moving once it has been acted on. The
// cases below pin that: re-issuing corrects a period rather than duplicating it,
// the frozen statement survives an invoice being typed in afterwards, and
// clearing a field actually clears it.

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupSettlementTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Settlement{}, &User{}, &TopUp{}))

	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous })
}

func augustVendorSettlement() *Settlement {
	return &Settlement{
		Kind:          "vendor",
		Counterparty:  "anthropic",
		Label:         "anthropic",
		PeriodStart:   "2026-08-01",
		PeriodEnd:     "2026-08-31",
		AmountUSD:     1240.55,
		StatementJSON: `{"amount_usd":1240.55}`,
	}
}

// TestReissuingCorrectsAPeriodRatherThanDuplicatingIt. A corrected August has
// to replace August. Two rows for the same counterparty and period leave a
// reader to guess which one was paid, which is worse than no record.
func TestReissuingCorrectsAPeriodRatherThanDuplicatingIt(t *testing.T) {
	setupSettlementTestDB(t)

	first, err := SaveSettlement(augustVendorSettlement())
	require.NoError(t, err)
	require.NotZero(t, first.Id)

	corrected := augustVendorSettlement()
	corrected.AmountUSD = 1300.00
	second, err := SaveSettlement(corrected)
	require.NoError(t, err)

	assert.Equal(t, first.Id, second.Id, "the same period must keep the same row")

	all, err := ListSettlements("vendor", "2026-08-01", "2026-08-31", 0)
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.InDelta(t, 1300.00, all[0].AmountUSD, 1e-9)
	assert.Equal(t, first.CreatedAt, all[0].CreatedAt, "issue time is when it was first issued")
}

// TestADifferentPeriodIsADifferentSettlement -- the upsert key includes the
// period, or September would overwrite August.
func TestADifferentPeriodIsADifferentSettlement(t *testing.T) {
	setupSettlementTestDB(t)

	_, err := SaveSettlement(augustVendorSettlement())
	require.NoError(t, err)

	september := augustVendorSettlement()
	september.PeriodStart, september.PeriodEnd = "2026-09-01", "2026-09-30"
	september.AmountUSD = 990
	_, err = SaveSettlement(september)
	require.NoError(t, err)

	all, err := ListSettlements("vendor", "", "", 0)
	require.NoError(t, err)
	require.Len(t, all, 2)
}

// TestCustomerAndVendorDoNotCollide. Both sides share one table, so a customer
// whose id happens to match a vendor id must not overwrite it.
func TestCustomerAndVendorDoNotCollide(t *testing.T) {
	setupSettlementTestDB(t)

	vendor := augustVendorSettlement()
	_, err := SaveSettlement(vendor)
	require.NoError(t, err)

	customer := augustVendorSettlement()
	customer.Kind = "customer"
	customer.AmountUSD = 42
	_, err = SaveSettlement(customer)
	require.NoError(t, err)

	vendors, err := ListSettlements("vendor", "2026-08-01", "2026-08-31", 0)
	require.NoError(t, err)
	require.Len(t, vendors, 1)
	assert.InDelta(t, 1240.55, vendors[0].AmountUSD, 1e-9)

	customers, err := ListSettlements("customer", "2026-08-01", "2026-08-31", 0)
	require.NoError(t, err)
	require.Len(t, customers, 1)
	assert.InDelta(t, 42, customers[0].AmountUSD, 1e-9)
}

// TestClearingAFieldActuallyClearsIt. GORM's Save skips zero values on a struct
// update, so "the invoice was actually $0" and "delete this note" would both
// silently do nothing. SaveSettlement names its columns to avoid that; this is
// the test that keeps it named.
func TestClearingAFieldActuallyClearsIt(t *testing.T) {
	setupSettlementTestDB(t)

	initial := augustVendorSettlement()
	initial.InvoicedUSD = 1250
	initial.InvoiceRecorded = true
	initial.Note = "waiting on the credit memo"
	saved, err := SaveSettlement(initial)
	require.NoError(t, err)

	saved.InvoicedUSD = 0
	saved.Note = ""
	saved.InvoiceRecorded = false
	_, err = SaveSettlement(saved)
	require.NoError(t, err)

	reloaded, err := GetSettlement(saved.Id)
	require.NoError(t, err)
	assert.Zero(t, reloaded.InvoicedUSD)
	assert.Empty(t, reloaded.Note)
	assert.False(t, reloaded.InvoiceRecorded)
}

// TestAnUnrecordedInvoiceIsNotAZeroInvoice. Without the recorded flag, a vendor
// whose invoice has not arrived yet looks like a vendor who billed nothing --
// a 100% variance, reported as a finding, every month, until someone stops
// reading the column.
func TestAnUnrecordedInvoiceIsNotAZeroInvoice(t *testing.T) {
	pending := augustVendorSettlement()
	assert.False(t, pending.InvoiceRecorded)
	assert.Zero(t, pending.VarianceUSD(), "no invoice yet means no variance to report")

	pending.InvoiceRecorded = true
	assert.InDelta(t, -1240.55, pending.VarianceUSD(), 1e-9,
		"an invoice genuinely at zero IS a finding, once someone says so")
}

func TestDeleteSettlementReportsAMissingRow(t *testing.T) {
	setupSettlementTestDB(t)
	saved, err := SaveSettlement(augustVendorSettlement())
	require.NoError(t, err)

	require.NoError(t, DeleteSettlement(saved.Id))
	assert.Error(t, DeleteSettlement(saved.Id), "deleting twice must not report success")
}

func TestSaveSettlementRejectsAnUnkeyedRecord(t *testing.T) {
	setupSettlementTestDB(t)
	_, err := SaveSettlement(&Settlement{Kind: "vendor", PeriodStart: "a", PeriodEnd: "b"})
	assert.Error(t, err, "a settlement with no counterparty has nothing to be about")

	_, err = SaveSettlement(&Settlement{Kind: "vendor", Counterparty: "anthropic"})
	assert.Error(t, err, "a settlement with no period cannot be reconciled against anything")
}

// TestCreditedQuotaMatchesEachProvider is the one that would otherwise be found
// by a customer. model/topup.go credits Creem's Amount as raw quota and every
// other gateway's as dollars; a single formula here would misreport a Creem
// payment by a factor of QuotaPerUnit -- 500,000 by default, which is not a
// discrepancy anyone would mistake for rounding.
func TestCreditedQuotaMatchesEachProvider(t *testing.T) {
	previous := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = previous })

	// Stripe credits from Money.
	assert.InDelta(t, 20.0, creditedUSD(PaymentProviderStripe, 999, 20), 1e-9)

	// Creem writes quota units straight into Amount.
	assert.InDelta(t, 20.0, creditedUSD(PaymentProviderCreem, 10_000_000, 137), 1e-9)

	// Everything else: Amount is dollars.
	for _, provider := range []string{
		PaymentProviderEpay, PaymentProviderWaffo, PaymentProviderWaffoPancake, "",
	} {
		assert.InDelta(t, 20.0, creditedUSD(provider, 20, 137), 1e-9,
			"provider %q must read Amount as dollars", provider)
	}
}

func TestFetchCustomerPaymentsCountsCompletionNotCreation(t *testing.T) {
	setupSettlementTestDB(t)
	previous := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = previous })

	const augustStart, augustEnd = 1_754_000_000, 1_756_600_000

	rows := []*TopUp{
		// Created in July, paid in August: August's money.
		{UserId: 1, PaymentProvider: PaymentProviderWaffo, Amount: 100, Money: 100,
			TradeNo: "t1", Status: common.TopUpStatusSuccess,
			CreateTime: augustStart - 900_000, CompleteTime: augustStart + 10},
		// Paid after the window closed.
		{UserId: 1, PaymentProvider: PaymentProviderWaffo, Amount: 500, Money: 500,
			TradeNo: "t2", Status: common.TopUpStatusSuccess,
			CreateTime: augustStart, CompleteTime: augustEnd + 10},
		// Never paid.
		{UserId: 1, PaymentProvider: PaymentProviderWaffo, Amount: 900, Money: 900,
			TradeNo: "t3", Status: "pending",
			CreateTime: augustStart, CompleteTime: augustStart + 20},
		{UserId: 2, PaymentProvider: PaymentProviderStripe, Amount: 0, Money: 30,
			TradeNo: "t4", Status: common.TopUpStatusSuccess,
			CreateTime: augustStart, CompleteTime: augustStart + 30},
	}
	for _, row := range rows {
		require.NoError(t, DB.Create(row).Error)
	}

	payments, err := FetchCustomerPayments(augustStart, augustEnd)
	require.NoError(t, err)

	require.Len(t, payments[1], 1)
	assert.EqualValues(t, 1, payments[1][0].Orders, "only the settled August order counts")
	assert.InDelta(t, 100, payments[1][0].CreditedUSD, 1e-9)

	require.Len(t, payments[2], 1)
	assert.InDelta(t, 30, payments[2][0].CreditedUSD, 1e-9)
}

func TestCustomerPaymentsCombineCompanyUsersAndKeepTheOriginalCompany(t *testing.T) {
	setupSettlementTestDB(t)
	previous := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = previous })

	const start, end = 1_754_000_000, 1_756_600_000
	users := []*User{
		{Id: 1, Username: "Aaron", Group: "GenAI", AffCode: "aaron-company-payment"},
		{Id: 2, Username: "Joshua", Group: "GenAI", AffCode: "joshua-company-payment"},
	}
	for _, user := range users {
		require.NoError(t, DB.Create(user).Error)
	}
	for _, row := range []*TopUp{
		{UserId: 1, PaymentProvider: PaymentProviderWaffo, Amount: 100, Money: 100,
			TradeNo: "company-1", Status: common.TopUpStatusSuccess, CompleteTime: start + 10},
		{UserId: 2, PaymentProvider: PaymentProviderWaffo, Amount: 30, Money: 30,
			TradeNo: "company-2", Status: common.TopUpStatusSuccess, CompleteTime: start + 20},
	} {
		require.NoError(t, DB.Create(row).Error)
		require.Equal(t, "GenAI", row.CustomerGroup, "the payment must snapshot its company")
	}

	// Reassigning a login later must not move historical cash to a new company.
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 1).Update("group", "UnifyAI").Error)

	payments, err := FetchCustomerPaymentsByCustomer(start, end)
	require.NoError(t, err)
	require.Len(t, payments, 1)
	require.Len(t, payments["GenAI"], 1)
	assert.EqualValues(t, 2, payments["GenAI"][0].Orders)
	assert.InDelta(t, 130, payments["GenAI"][0].CreditedUSD, 1e-9)
	assert.NotContains(t, payments, "UnifyAI")
}
