package service

// UNIFYAPI-FORK: settlement statements -- the documents you hand to a
// counterparty, in both directions.
//
// A statement is a FOLD OVER THE SAME ROWS the profit screen reads, never a
// second query. That is deliberate: if the bill sent to a customer and the
// margin read off the dashboard came from different SQL they would eventually
// disagree, and the first person to notice would be the customer.
//
// The two directions are asymmetric for the same reason reconciliation is (see
// reconcile.go):
//
//	a CUSTOMER statement is a BILL. Every amount is quota actually deducted
//	from the ledger, already carrying the model discount, the group ratio and
//	cache pricing. Nothing is re-derived, so it is defensible line by line in
//	a dispute -- which is the only property that makes it worth sending.
//
//	a VENDOR statement is a CLAIM TO BE CHECKED. Its amounts are modelled from
//	token counts, because vendors issue no per-request receipt. Issuing one is
//	how you find out whether the model is right: you diff it against the
//	invoice that arrives.
//
// Both are built here as pure functions over rows so the arithmetic is testable
// without a database.

import (
	"sort"
	"strconv"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// StatementKind is which side of the business a statement faces.
type StatementKind string

const (
	// StatementKindCustomer is money owed to us: read from the ledger.
	StatementKindCustomer StatementKind = "customer"
	// StatementKindVendor is money we owe upstream: modelled, then checked.
	StatementKindVendor StatementKind = "vendor"
)

// ParseStatementKind validates a caller-supplied side.
func ParseStatementKind(raw string) (StatementKind, bool) {
	switch StatementKind(raw) {
	case StatementKindCustomer, StatementKindVendor:
		return StatementKind(raw), true
	case "":
		return StatementKindCustomer, true
	default:
		return "", false
	}
}

// StatementLine is one model's worth of activity on a statement.
//
// Models, not days: a counterparty checking a bill asks "what did I pay for
// gpt-4o", and a per-day breakdown of the same total answers a question nobody
// is asking. The daily grain is still in the profit screen.
type StatementLine struct {
	Model            string  `json:"model"`
	Requests         int64   `json:"requests"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CachedTokens     int64   `json:"cached_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	AmountUSD        float64 `json:"amount_usd"`

	// Unpriced marks a line whose amount could not be modelled at all. Only
	// ever set on a vendor statement -- a customer line's amount comes from the
	// ledger and always exists, even for a model we forgot to catalogue.
	Unpriced bool `json:"unpriced,omitempty"`
}

// Statement is one counterparty's activity for one period.
type Statement struct {
	Kind StatementKind `json:"kind"`

	// Counterparty is the stable id -- a User Group for a customer/company, a
	// models.dev vendor id for an upstream. Label is what it was called at build
	// time. A tenant may contain several login users but receives one statement.
	Counterparty string `json:"counterparty"`
	Label        string `json:"label"`

	// Group is the customer's pricing tier, carried because it is half the
	// explanation of the amount: the same tokens bill differently per group.
	Group string `json:"group,omitempty"`

	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`

	Lines []StatementLine `json:"lines"`

	Requests         int64   `json:"requests"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CachedTokens     int64   `json:"cached_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	AmountUSD        float64 `json:"amount_usd"`

	// UnpricedRequests is vendor-side only: traffic whose model has no catalog
	// price, so it contributes nothing to AmountUSD. A vendor statement with
	// unpriced traffic UNDERSTATES what we owe, and sending it as-is means
	// under-paying an invoice. Surfaced rather than dropped for that reason.
	UnpricedRequests int64    `json:"unpriced_requests"`
	UnpricedModels   []string `json:"unpriced_models,omitempty"`
}

// Complete reports whether every request on this statement could be priced.
// An incomplete vendor statement is not a bill you can pay from.
func (s Statement) Complete() bool { return s.UnpricedRequests == 0 }

// BuildStatements folds usage rows into one statement per counterparty.
//
// The customer side sums quota deducted; the vendor side sums modelled cost.
// Both walk the same rows in the same order, so a customer's bill and the cost
// of serving it are always drawn from the same underlying facts.
func BuildStatements(rows []model.UsageRow, kind StatementKind, periodStart, periodEnd string) []Statement {
	type draft struct {
		statement Statement
		byModel   map[string]*StatementLine
		unpriced  map[string]bool
	}

	drafts := map[string]*draft{}
	for _, row := range rows {
		key, label, group := statementParty(row, kind)
		entry, ok := drafts[key]
		if !ok {
			entry = &draft{
				statement: Statement{
					Kind:         kind,
					Counterparty: key,
					Label:        label,
					Group:        group,
					PeriodStart:  periodStart,
					PeriodEnd:    periodEnd,
				},
				byModel:  map[string]*StatementLine{},
				unpriced: map[string]bool{},
			}
			drafts[key] = entry
		}

		amount, priced := statementAmount(row, kind)
		if !priced {
			entry.statement.UnpricedRequests += row.Requests
			entry.unpriced[row.Model] = true
		}

		line, ok := entry.byModel[row.Model]
		if !ok {
			line = &StatementLine{Model: row.Model}
			entry.byModel[row.Model] = line
		}
		line.Requests += row.Requests
		line.PromptTokens += row.PromptTokens
		line.CachedTokens += row.CachedTokens
		line.CompletionTokens += row.CompletionTokens
		line.AmountUSD += amount
		if !priced {
			line.Unpriced = true
		}

		entry.statement.Requests += row.Requests
		entry.statement.PromptTokens += row.PromptTokens
		entry.statement.CachedTokens += row.CachedTokens
		entry.statement.CompletionTokens += row.CompletionTokens
		entry.statement.AmountUSD += amount
	}

	out := make([]Statement, 0, len(drafts))
	for _, entry := range drafts {
		statement := entry.statement
		statement.Lines = sortedLines(entry.byModel)
		statement.UnpricedModels = sortedKeys(entry.unpriced)
		out = append(out, statement)
	}

	// Largest bill first: that is the one whose accuracy costs the most to get
	// wrong, and the one someone opens first.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].AmountUSD != out[j].AmountUSD {
			return out[i].AmountUSD > out[j].AmountUSD
		}
		return out[i].Counterparty < out[j].Counterparty
	})
	return out
}

// statementAmount is the whole asymmetry between the two directions, in one
// place: what we charged, versus what we think we were charged.
func statementAmount(row model.UsageRow, kind StatementKind) (amount float64, priced bool) {
	if kind == StatementKindCustomer {
		// Read, not derived. A customer bill that recomputed the price could
		// disagree with the deduction the customer already saw.
		return RevenueUSD(row.Quota), true
	}
	return ratio_setting.UpstreamCostUSD(
		row.Model, row.ChannelID, row.PromptTokens, row.CachedTokens, row.CompletionTokens)
}

func statementParty(row model.UsageRow, kind StatementKind) (key, label, group string) {
	if kind == StatementKindCustomer {
		if row.UserGroup != "" {
			return row.UserGroup, row.UserGroup, row.UserGroup
		}
		// Preserve upstream behaviour for the tenantless/root edge case.
		key = strconv.Itoa(row.UserID)
		label = row.Username
		if label == "" {
			label = "user " + key
		}
		return key, label, ""
	}

	key = "unlisted"
	if entry, ok := ratio_setting.CatalogEntryFor(row.Model); ok && entry.Vendor != "" {
		key = entry.Vendor
	}
	return key, key, ""
}

func sortedLines(byModel map[string]*StatementLine) []StatementLine {
	out := make([]StatementLine, 0, len(byModel))
	for _, line := range byModel {
		out = append(out, *line)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].AmountUSD != out[j].AmountUSD {
			return out[i].AmountUSD > out[j].AmountUSD
		}
		return out[i].Model < out[j].Model
	})
	return out
}

// StatementTotals is the sum across every counterparty in a period. It exists
// so the screen's header cannot drift from the rows beneath it -- it is derived
// from the same slice, not queried separately.
type StatementTotals struct {
	Counterparties   int     `json:"counterparties"`
	Requests         int64   `json:"requests"`
	AmountUSD        float64 `json:"amount_usd"`
	UnpricedRequests int64   `json:"unpriced_requests"`
}

// SumStatements totals a batch of statements.
func SumStatements(statements []Statement) StatementTotals {
	totals := StatementTotals{Counterparties: len(statements)}
	for _, statement := range statements {
		totals.Requests += statement.Requests
		totals.AmountUSD += statement.AmountUSD
		totals.UnpricedRequests += statement.UnpricedRequests
	}
	return totals
}
