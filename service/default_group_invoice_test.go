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

// The default group is where accounts sit when they belong to nobody in
// particular. Billing it as one counterparty puts unrelated people on a single
// invoice with no way to tell their usage apart.
func TestEachAccountInTheDefaultGroupIsItsOwnInvoice(t *testing.T) {
	rows := []model.UsageRow{
		{Day: "2026-09-01", Model: "gpt-4o", UserID: 11, Username: "ana",
			UserGroup: "default", BillingGroup: "default", Requests: 1, Quota: usdToQuota(3)},
		{Day: "2026-09-01", Model: "gpt-4o", UserID: 12, Username: "bo",
			UserGroup: "default", BillingGroup: "default", Requests: 1, Quota: usdToQuota(7)},
	}
	statements := BuildStatements(rows, StatementKindCustomer, "2026-09-01", "2026-09-30")
	require.Len(t, statements, 2, "two unrelated accounts must not share one invoice")

	ana, ok := statementFor(statements, "11")
	require.True(t, ok, "the account is the counterparty, not the group")
	assert.Equal(t, "ana", ana.Label)

	bo, ok := statementFor(statements, "12")
	require.True(t, ok)
	assert.Equal(t, "bo", bo.Label)

	_, pooled := statementFor(statements, model.CustomerPricingGroupKey("default"))
	assert.False(t, pooled, "nothing may still be billed to the default group as a whole")
}

// The narrow scope is the point. Every other pricing group is somebody's
// customer, and re-keying those would split invoices that already carry real
// money -- in production, six of them totalling several hundred dollars.
func TestEveryOtherGroupIsStillBilledAsOneCustomer(t *testing.T) {
	rows := []model.UsageRow{
		{Day: "2026-09-01", Model: "gpt-4o", UserID: 21, Username: "ana",
			UserGroup: "Chinhin", BillingGroup: "Chinhin", Requests: 1, Quota: usdToQuota(3)},
		{Day: "2026-09-01", Model: "gpt-4o", UserID: 22, Username: "bo",
			UserGroup: "Chinhin", BillingGroup: "Chinhin", Requests: 1, Quota: usdToQuota(7)},
	}
	statements := BuildStatements(rows, StatementKindCustomer, "2026-09-01", "2026-09-30")
	require.Len(t, statements, 1, "a customer's members share one invoice, as before")

	statement, ok := statementFor(statements, model.CustomerPricingGroupKey("Chinhin"))
	require.True(t, ok, "the counterparty must still be the pricing group")
	assert.Equal(t, "Chinhin", statement.Label)
	assert.InDelta(t, 10, statement.AmountUSD, 1e-9, "and it must carry both members' usage")
}

// A team's members share a customer, so they stay one invoice. The default
// group is the exception, not the new rule.
func TestATeamIsStillOneInvoice(t *testing.T) {
	rows := []model.UsageRow{
		{Day: "2026-09-01", Model: "gpt-4o", UserID: 31, Username: "ana",
			UserGroup: "Nusa Labs", BillingGroup: "Nusa Labs", Requests: 1, Quota: usdToQuota(4)},
		{Day: "2026-09-01", Model: "gpt-4o", UserID: 32, Username: "bo",
			UserGroup: "Nusa Labs", BillingGroup: "Nusa Labs", Requests: 1, Quota: usdToQuota(6)},
	}
	statements := BuildStatements(rows, StatementKindCustomer, "2026-09-01", "2026-09-30")
	require.Len(t, statements, 1)
	statement, ok := statementFor(statements, model.CustomerPricingGroupKey("Nusa Labs"))
	require.True(t, ok)
	assert.InDelta(t, 10, statement.AmountUSD, 1e-9)
}
