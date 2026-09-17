/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func septemberStatements() []Statement {
	return BuildStatements([]model.UsageRow{
		{Model: "gpt-4o", UserID: 13, Username: "Aaron", BillingGroup: "UnifyAI",
			Requests: 2, Quota: usdToQuota(5)},
	}, StatementKindCustomer, "2026-09-01", "2026-09-30")
}

// The question people actually ask is "how much has this customer put in",
// which a month-scoped figure cannot answer. A customer that paid in August
// and not in September showed an empty funding section, which read as the
// feature being missing rather than the month being empty.
func TestACustomerShowsWhatItHasPaidInAltogether(t *testing.T) {
	statements := septemberStatements()

	// Nothing arrived in September.
	statements = AttachFunding(statements, StatementKindCustomer, nil, "2026-09-01", "2026-09-30")
	// $50 arrived earlier.
	toDate := []model.FundingRow{
		{UserID: 10, Username: "ycwtest", CustomerGroup: "UnifyAI",
			Provider: model.PaymentProviderAdmin, Orders: 1, CreditedUSD: 10},
		{UserID: 13, Username: "Aaron", CustomerGroup: "UnifyAI",
			Provider: model.PaymentProviderStripe, Orders: 2, CreditedUSD: 40},
	}
	out := AttachFundingToDate(statements, StatementKindCustomer, toDate)

	require.Len(t, out, 1)
	s := out[0]
	assert.Zero(t, s.FundedUSD, "nothing was paid in during the period")
	assert.Empty(t, s.Funding)
	assert.InDelta(t, 50, s.FundedToDateUSD, 1e-9, "but $50 has been paid in altogether")
	require.Len(t, s.FundingToDate, 2)
	assert.Equal(t, "Aaron", s.FundingToDate[0].Username, "largest funder first")
	assert.InDelta(t, 40, s.FundingToDate[0].CreditedUSD, 1e-9)
}

// The to-date figure must never invent a statement. Every customer that ever
// paid would otherwise appear in every month's view.
func TestToDateFundingNeverAddsACustomerToThePeriod(t *testing.T) {
	statements := septemberStatements()
	toDate := []model.FundingRow{
		{UserID: 99, Username: "stranger", CustomerGroup: "Kingdee",
			Provider: model.PaymentProviderStripe, Orders: 1, CreditedUSD: 500},
	}
	out := AttachFundingToDate(statements, StatementKindCustomer, toDate)

	require.Len(t, out, 1, "a customer absent from this period must stay absent")
	assert.Equal(t, "UnifyAI", out[0].Label)
	assert.Zero(t, out[0].FundedToDateUSD, "and must not pick up somebody else's money")
}

// Both figures coexist: the period's own receipts and the running total.
func TestThePeriodAndTheRunningTotalAreBothCarried(t *testing.T) {
	statements := septemberStatements()
	period := []model.FundingRow{
		{UserID: 13, Username: "Aaron", CustomerGroup: "UnifyAI",
			Provider: model.PaymentProviderStripe, Orders: 1, CreditedUSD: 10},
	}
	statements = AttachFunding(statements, StatementKindCustomer, period, "2026-09-01", "2026-09-30")
	toDate := []model.FundingRow{
		{UserID: 13, Username: "Aaron", CustomerGroup: "UnifyAI",
			Provider: model.PaymentProviderStripe, Orders: 4, CreditedUSD: 60},
	}
	out := AttachFundingToDate(statements, StatementKindCustomer, toDate)

	s := out[0]
	assert.InDelta(t, 10, s.FundedUSD, 1e-9, "this period")
	assert.InDelta(t, 60, s.FundedToDateUSD, 1e-9, "altogether")
	require.Len(t, s.Funding, 1)
	require.Len(t, s.FundingToDate, 1)
}

// A vendor is paid; it does not pay us. Its statement must carry neither.
//
// Note what this does NOT prove: removing the StatementKindCustomer guard
// leaves it passing, because a vendor's counterparty is a vendor name and a
// funding row keys on a customer pricing group, so nothing matches either way.
// The guard is belt and braces. What is asserted here is the property -- a
// vendor statement carries no funding -- which holds for both reasons.
func TestAVendorStatementCarriesNoFunding(t *testing.T) {
	statements := BuildStatements([]model.UsageRow{
		{Model: "gpt-4o", UserID: 13, Username: "Aaron", BillingGroup: "UnifyAI",
			Requests: 1, Quota: usdToQuota(5)},
	}, StatementKindVendor, "2026-09-01", "2026-09-30")
	toDate := []model.FundingRow{
		{UserID: 13, Username: "Aaron", CustomerGroup: "UnifyAI",
			Provider: model.PaymentProviderStripe, Orders: 1, CreditedUSD: 10},
	}
	out := AttachFundingToDate(statements, StatementKindVendor, toDate)
	for _, s := range out {
		assert.Zero(t, s.FundedToDateUSD)
		assert.Empty(t, s.FundingToDate)
	}
}
