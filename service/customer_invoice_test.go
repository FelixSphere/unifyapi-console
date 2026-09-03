/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package service

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestRenderCustomerInvoiceHTMLUsesFrozenStatementAndEscapesParties(t *testing.T) {
	settlement := &model.Settlement{
		Id: 42, Kind: "customer", Status: model.SettlementStatusIssued,
		PeriodStart: "2026-08-01", PeriodEnd: "2026-08-31", AmountUSD: 12.3456,
		CreatedAt: 1_756_684_800,
	}
	statement := Statement{
		Kind: StatementKindCustomer, Label: `ACME <script>alert("x")</script>`, Group: "GenAI",
		PeriodStart: "2026-08-01", PeriodEnd: "2026-08-31", Requests: 3, AmountUSD: 12.3456,
		Lines: []StatementLine{{Model: "gpt-5", Requests: 3, PromptTokens: 100, CachedTokens: 20, CompletionTokens: 30, AmountUSD: 12.3456}},
	}

	html, err := RenderCustomerInvoiceHTML(settlement, statement)
	require.NoError(t, err)
	document := string(html)
	require.Contains(t, document, "UAI-202608-000042")
	require.Contains(t, document, "USD 12.3456")
	require.Contains(t, document, "UnifyAPI usage - gpt-5")
	require.Contains(t, document, "ACME &lt;script&gt;")
	require.NotContains(t, document, "<script>alert")
}

func TestRenderCustomerInvoiceHTMLRejectsVendorSettlement(t *testing.T) {
	_, err := RenderCustomerInvoiceHTML(
		&model.Settlement{Id: 1, Kind: "vendor"},
		Statement{Kind: StatementKindVendor},
	)
	require.ErrorContains(t, err, "only customer settlements")
}

func TestRenderCustomerInvoiceHTMLRejectsTotalsThatDoNotMatchFrozenLines(t *testing.T) {
	_, err := RenderCustomerInvoiceHTML(
		&model.Settlement{
			Id: 1, Kind: "customer", PeriodStart: "2026-08-01", PeriodEnd: "2026-08-31", AmountUSD: 10,
		},
		Statement{
			Kind: StatementKindCustomer, PeriodStart: "2026-08-01", PeriodEnd: "2026-08-31",
			AmountUSD: 10, Requests: 2,
			Lines: []StatementLine{{Model: "gpt-5", AmountUSD: 9, Requests: 2}},
		},
	)
	require.ErrorContains(t, err, "does not match")
}

func TestCustomerInvoiceNumberIsStableForMalformedLegacyPeriod(t *testing.T) {
	require.Equal(t, "UAI-UNKNOWN-000007", CustomerInvoiceNumber(&model.Settlement{Id: 7, PeriodStart: strings.Repeat("x", 2)}))
}

func TestReplacementInvoiceReferencesPreservedInvoiceNumbers(t *testing.T) {
	statement := Statement{
		Kind: StatementKindCustomer, Label: "Chinhin", Group: "Chinhin",
		PeriodStart: "2026-08-01", PeriodEnd: "2026-08-31", Requests: 1, AmountUSD: 2,
		Lines: []StatementLine{{Model: "gpt-5", Requests: 1, AmountUSD: 2}},
	}
	html, err := RenderCustomerInvoiceHTML(&model.Settlement{
		Id: 6, Kind: "customer", Status: model.SettlementStatusIssued,
		PeriodStart: statement.PeriodStart, PeriodEnd: statement.PeriodEnd, AmountUSD: 2,
		SupersedesIDs: []int{1, 4}, ReplacementReason: "Correct customer membership", CreatedAt: 1_756_684_800,
	}, statement)
	require.NoError(t, err)
	require.Contains(t, string(html), "Replaces")
	require.Contains(t, string(html), "UAI-202608-000001, UAI-202608-000004")
	require.Contains(t, string(html), "Correct customer membership")
}
