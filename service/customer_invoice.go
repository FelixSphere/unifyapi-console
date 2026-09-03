/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package service

import (
	"bytes"
	"fmt"
	"html/template"
	"math"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/model"
)

// CustomerInvoiceNumber is deterministic because the settlement id is the
// immutable accounting record. Reprinting must never mint a second number for
// the same issued invoice.
func CustomerInvoiceNumber(settlement *model.Settlement) string {
	period := "UNKNOWN"
	if parsed, err := time.Parse("2006-01-02", settlement.PeriodStart); err == nil {
		period = parsed.Format("200601")
	}
	return fmt.Sprintf("UAI-%s-%06d", period, settlement.Id)
}

type customerInvoiceView struct {
	Number           string
	Status           string
	IssueDate        string
	PeriodStart      string
	PeriodEnd        string
	BillTo           string
	CustomerGroup    string
	Lines            []StatementLine
	TotalUSD         float64
	Requests         int64
	Replaces         []string
	SupersededBy     string
	CorrectionReason string
}

// RenderCustomerInvoiceHTML builds the document a browser prints or saves as
// PDF. It consumes only the frozen statement; live logs and current pricing
// cannot rewrite an invoice that has already been issued.
func RenderCustomerInvoiceHTML(settlement *model.Settlement, statement Statement) ([]byte, error) {
	if settlement == nil || settlement.Id == 0 {
		return nil, fmt.Errorf("issued settlement is required")
	}
	if settlement.Kind != string(StatementKindCustomer) || statement.Kind != StatementKindCustomer {
		return nil, fmt.Errorf("only customer settlements can be rendered as outgoing invoices")
	}
	if statement.PeriodStart != settlement.PeriodStart || statement.PeriodEnd != settlement.PeriodEnd {
		return nil, fmt.Errorf("frozen invoice period does not match its settlement")
	}
	var lineAmount float64
	var lineRequests int64
	for _, line := range statement.Lines {
		lineAmount += line.AmountUSD
		lineRequests += line.Requests
	}
	if math.Abs(lineAmount-statement.AmountUSD) > 1e-8 ||
		math.Abs(statement.AmountUSD-settlement.AmountUSD) > 1e-8 ||
		lineRequests != statement.Requests {
		return nil, fmt.Errorf("frozen invoice total does not match its line items")
	}
	status := strings.ToUpper(settlement.Status)
	if status == "" {
		status = "ISSUED"
	}
	if status == "SETTLED" {
		status = "PAID"
	}
	view := customerInvoiceView{
		Number:           CustomerInvoiceNumber(settlement),
		Status:           status,
		IssueDate:        time.Unix(settlement.CreatedAt, 0).UTC().Format("2006-01-02"),
		PeriodStart:      statement.PeriodStart,
		PeriodEnd:        statement.PeriodEnd,
		BillTo:           statement.Label,
		CustomerGroup:    statement.Group,
		Lines:            statement.Lines,
		TotalUSD:         statement.AmountUSD,
		Requests:         statement.Requests,
		CorrectionReason: settlement.ReplacementReason,
	}
	for _, id := range settlement.SupersedesIDs {
		view.Replaces = append(view.Replaces, CustomerInvoiceNumber(&model.Settlement{Id: id, PeriodStart: settlement.PeriodStart}))
	}
	if settlement.SupersededByID != 0 {
		view.SupersededBy = CustomerInvoiceNumber(&model.Settlement{Id: settlement.SupersededByID, PeriodStart: settlement.PeriodStart})
	}
	var output bytes.Buffer
	if err := customerInvoiceTemplate.Execute(&output, view); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

var customerInvoiceTemplate = template.Must(template.New("customer-invoice").Funcs(template.FuncMap{
	"money": func(value float64) string { return fmt.Sprintf("%.4f", value) },
	"integer": func(value int64) string {
		return fmt.Sprintf("%d", value)
	},
}).Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Invoice {{.Number}}</title>
  <style>
    :root { color-scheme: light; font-family: Inter, Arial, sans-serif; color: #172033; }
    * { box-sizing: border-box; }
    body { margin: 0; background: #eef2f7; font-size: 12px; line-height: 1.45; }
    .sheet { width: 210mm; min-height: 297mm; margin: 16px auto; padding: 18mm; background: #fff; }
    .top { display: flex; justify-content: space-between; gap: 24px; border-bottom: 3px solid #3157d5; padding-bottom: 20px; }
    .brand { font-size: 26px; font-weight: 800; letter-spacing: -.04em; color: #1d3ca6; }
    .product { margin-top: 3px; color: #62708a; font-size: 11px; letter-spacing: .12em; text-transform: uppercase; }
    h1 { margin: 0; text-align: right; font-size: 30px; letter-spacing: .08em; }
    .status { display: inline-block; margin-top: 8px; padding: 4px 9px; border-radius: 999px; background: #e8edff; color: #2444ad; font-weight: 700; }
    .meta { display: grid; grid-template-columns: 1fr 1fr; gap: 28px; margin: 28px 0; }
    .eyebrow { color: #6e7890; font-size: 10px; font-weight: 700; letter-spacing: .12em; text-transform: uppercase; }
    .party { margin-top: 6px; font-size: 17px; font-weight: 700; }
    .muted { color: #68748a; }
    dl { display: grid; grid-template-columns: auto 1fr; gap: 5px 14px; margin: 0; }
    dt { color: #68748a; } dd { margin: 0; text-align: right; font-weight: 600; }
    table { width: 100%; border-collapse: collapse; }
    th { padding: 9px 7px; background: #f3f5f9; color: #59667c; font-size: 9px; letter-spacing: .06em; text-align: right; text-transform: uppercase; }
    th:first-child, td:first-child { text-align: left; }
    td { padding: 10px 7px; border-bottom: 1px solid #dfe4ed; text-align: right; vertical-align: top; }
    thead { display: table-header-group; }
    tr, .totals, .notice, footer { break-inside: avoid; }
    .model { font-weight: 650; overflow-wrap: anywhere; }
    .totals { width: 48%; margin: 22px 0 0 auto; }
    .total-row { display: flex; justify-content: space-between; padding: 7px 0; }
    .grand { margin-top: 5px; padding-top: 11px; border-top: 2px solid #3157d5; font-size: 18px; font-weight: 800; }
    .notice { margin-top: 34px; padding: 14px 16px; border-left: 3px solid #3157d5; background: #f6f8fc; color: #4e5a70; }
    footer { margin-top: 38px; padding-top: 14px; border-top: 1px solid #dfe4ed; color: #778196; font-size: 10px; }
    .void { color: #a11; background: #fee; }
    @page { size: A4; margin: 0; }
    @media print {
      body { background: #fff; }
      .sheet { margin: 0; box-shadow: none; }
    }
  </style>
</head>
<body>
  <main class="sheet">
    <header class="top">
      <div><div class="brand">UnifyAI</div><div class="product">UnifyAPI services</div></div>
      <div><h1>INVOICE</h1><div class="status {{if or (eq .Status "VOID") (eq .Status "SUPERSEDED")}}void{{end}}">{{.Status}}</div></div>
    </header>
    <section class="meta">
      <div>
        <div class="eyebrow">Bill to</div>
        <div class="party">{{.BillTo}}</div>
        {{if and .CustomerGroup (ne .CustomerGroup .BillTo)}}<div class="muted">Account group: {{.CustomerGroup}}</div>{{end}}
      </div>
      <dl>
        <dt>Invoice number</dt><dd>{{.Number}}</dd>
        <dt>Issue date</dt><dd>{{.IssueDate}}</dd>
        <dt>Service period</dt><dd>{{.PeriodStart}} to {{.PeriodEnd}}</dd>
        <dt>Currency</dt><dd>USD</dd>
        <dt>Payment terms</dt><dd>Due upon receipt</dd>
		{{if .Replaces}}<dt>Replaces</dt><dd>{{range $index, $number := .Replaces}}{{if $index}}, {{end}}{{$number}}{{end}}</dd>{{end}}
		{{if .CorrectionReason}}<dt>Correction reason</dt><dd>{{.CorrectionReason}}</dd>{{end}}
		{{if .SupersededBy}}<dt>Superseded by</dt><dd>{{.SupersededBy}}</dd>{{end}}
      </dl>
    </section>
    <table aria-label="Invoice line items">
      <thead><tr><th>Service / model</th><th>Requests</th><th>Input tokens</th><th>Cached tokens</th><th>Output tokens</th><th>Amount (USD)</th></tr></thead>
      <tbody>
        {{range .Lines}}<tr><td class="model">UnifyAPI usage - {{.Model}}</td><td>{{integer .Requests}}</td><td>{{integer .PromptTokens}}</td><td>{{integer .CachedTokens}}</td><td>{{integer .CompletionTokens}}</td><td>{{money .AmountUSD}}</td></tr>{{end}}
      </tbody>
    </table>
    <section class="totals">
      <div class="total-row"><span class="muted">Requests</span><strong>{{integer .Requests}}</strong></div>
      <div class="total-row grand"><span>Total due</span><span>USD {{money .TotalUSD}}</span></div>
    </section>
    <div class="notice">Please settle this invoice according to your commercial agreement with UnifyAI and quote invoice number <strong>{{.Number}}</strong> with the payment.</div>
    <footer>Issued by UnifyAI for UnifyAPI usage. This document is generated from the immutable monthly usage statement stored at issuance.</footer>
  </main>
</body>
</html>`))
