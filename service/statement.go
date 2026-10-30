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
	"fmt"
	"sort"
	"strconv"
	"time"

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

// ValidateClosedCalendarMonth guards the accounting boundary at the server.
// The UI may preview any month, but an official statement can only be issued
// for one complete natural month that ended before today.
func ValidateClosedCalendarMonth(start, end string, today time.Time) error {
	const layout = "2006-01-02"
	location := today.Location()
	from, err := time.ParseInLocation(layout, start, location)
	if err != nil {
		return fmt.Errorf("invalid start %q: expected YYYY-MM-DD", start)
	}
	to, err := time.ParseInLocation(layout, end, location)
	if err != nil {
		return fmt.Errorf("invalid end %q: expected YYYY-MM-DD", end)
	}
	if from.Day() != 1 || !to.Equal(from.AddDate(0, 1, -1)) {
		return fmt.Errorf("official statements require one complete calendar month")
	}
	todayStart := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, location)
	if !to.Before(todayStart) {
		return fmt.Errorf("period %s is still open; preview is allowed but issuing is not", from.Format("2006-01"))
	}
	return nil
}

// StatementLine is one model's worth of activity on a statement.
//
// Models, not days: a counterparty checking a bill asks "what did I pay for
// gpt-4o", and a per-day breakdown of the same total answers a question nobody
// is asking. The daily grain is still in the profit screen.
type StatementLine struct {
	Model            string  `json:"model"`
	ChannelID        int     `json:"channel_id,omitempty"`
	ChannelName      string  `json:"channel_name,omitempty"`
	ChannelBaseURL   string  `json:"channel_base_url,omitempty"`
	CostRatio        float64 `json:"cost_ratio,omitempty"`
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

// StatementUserLine is one login's share of a customer statement.
//
// A customer with several logins asks "which of my people spent this" in the
// same breath as "what did I pay for gpt-4o", so the bill carries both grains.
// Keyed on the log's user id and labelled with the username the log recorded
// at the time: a login deleted since still appears, because its usage was
// still billed to the customer. (22.8M quota of one deleted login went
// unattributable in production when this was joined through the live users
// table instead.)
type StatementUserLine struct {
	UserID           int     `json:"user_id"`
	Username         string  `json:"username"`
	Requests         int64   `json:"requests"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CachedTokens     int64   `json:"cached_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	AmountUSD        float64 `json:"amount_usd"`
}

// StatementFundingLine is one login's receipts on a customer statement: what
// they paid in (or an operator granted them) during the period, in the same
// grain as StatementUserLine so "who spent it" and "who funded it" sit side by
// side. Keyed on the top-up's user id and labelled from the live users table
// when the login still exists, by id when it does not.
type StatementFundingLine struct {
	UserID   int    `json:"user_id"`
	Username string `json:"username"`
	// Orders are gateway top-ups; Grants are operator adjustments, which can
	// be negative. Neither is a payment against an invoice -- wallet credit is
	// prepaid, and TestCustomerSettlementResponseNeverTreatsTopUpsAsInvoicePayments
	// keeps that word out of this document.
	Orders      int64   `json:"orders"`
	Grants      int64   `json:"grants"`
	CreditedUSD float64 `json:"credited_usd"`
}

// Statement is one counterparty's activity for one period.
type Statement struct {
	Kind StatementKind `json:"kind"`

	// Counterparty is the stable id -- a User Group for a customer/company, or
	// the supplier identified from the actual Channel Base URL for an upstream.
	// Label is what it was called at build time. A tenant may contain several
	// login users but receives one statement.
	Counterparty string `json:"counterparty"`
	Label        string `json:"label"`

	// Group is the customer's pricing tier, carried because it is half the
	// explanation of the amount: the same tokens bill differently per group.
	Group string `json:"group,omitempty"`

	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`

	Lines []StatementLine `json:"lines"`

	// Users is the same total broken down by login. Customer side only: a
	// supplier is owed for models on channels, not for who called them.
	Users []StatementUserLine `json:"users,omitempty"`
	// Funding is what the customer's logins paid in during the period, by
	// login; FundedUSD is its total. Customer side only. See AttachFunding.
	Funding   []StatementFundingLine `json:"funding,omitempty"`
	FundedUSD float64                `json:"funded_usd,omitempty"`
	// FundingToDate is everything the customer's logins have ever paid in, up
	// to the end of this period, and FundedToDateUSD is its total. The period
	// figures answer "what arrived this month"; these answer "what has this
	// customer put in altogether", which is the question usually being asked
	// and which a month-scoped view cannot show. See AttachFundingToDate.
	FundingToDate    []StatementFundingLine `json:"funding_to_date,omitempty"`
	FundedToDateUSD  float64                `json:"funded_to_date_usd,omitempty"`
	Requests         int64                  `json:"requests"`
	PromptTokens     int64                  `json:"prompt_tokens"`
	CachedTokens     int64                  `json:"cached_tokens"`
	CompletionTokens int64                  `json:"completion_tokens"`
	AmountUSD        float64                `json:"amount_usd"`

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
		byUser    map[int]*StatementUserLine
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
				byUser:   map[int]*StatementUserLine{},
				unpriced: map[string]bool{},
			}
			drafts[key] = entry
		}

		amount, priced := statementAmount(row, kind)
		if !priced {
			entry.statement.UnpricedRequests += row.Requests
			entry.unpriced[row.Model] = true
		}

		lineKey := row.Model
		if kind == StatementKindVendor {
			// A supplier invoice must retain the channel that incurred the cost.
			// The same model on two channels can have different purchasing ratios.
			lineKey = row.Model + "\x00" + strconv.Itoa(row.ChannelID) + "\x00" + model.NormalizeChannelBaseURL(row.ChannelBaseURL)
		}
		line, ok := entry.byModel[lineKey]
		if !ok {
			line = &StatementLine{Model: row.Model}
			if kind == StatementKindVendor {
				line.ChannelID = row.ChannelID
				line.ChannelName = row.ChannelName
				line.ChannelBaseURL = model.NormalizeChannelBaseURL(row.ChannelBaseURL)
				line.CostRatio = ratio_setting.GetChannelCostRatio(row.ChannelID)
			}
			entry.byModel[lineKey] = line
		}
		line.Requests += row.Requests
		line.PromptTokens += row.PromptTokens
		line.CachedTokens += row.CachedTokens
		line.CompletionTokens += row.CompletionTokens
		line.AmountUSD += amount
		if !priced {
			line.Unpriced = true
		}

		if kind == StatementKindCustomer {
			userLine, ok := entry.byUser[row.UserID]
			if !ok {
				name := row.Username
				if name == "" {
					name = "user " + strconv.Itoa(row.UserID)
				}
				userLine = &StatementUserLine{UserID: row.UserID, Username: name}
				entry.byUser[row.UserID] = userLine
			}
			userLine.Requests += row.Requests
			userLine.PromptTokens += row.PromptTokens
			userLine.CachedTokens += row.CachedTokens
			userLine.CompletionTokens += row.CompletionTokens
			userLine.AmountUSD += amount
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
		if kind == StatementKindCustomer {
			statement.Users = sortedUserLines(entry.byUser)
		}
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

// AttachFunding puts the period's receipts on the customer statements they
// were paid into, one line per login. A customer that paid but did not consume
// still gets a statement -- with no usage lines -- because money that arrived
// must appear somewhere. Vendor statements are returned unchanged: a supplier
// is paid, it does not pay us.
func AttachFunding(statements []Statement, kind StatementKind, rows []model.FundingRow, periodStart, periodEnd string) []Statement {
	if kind != StatementKindCustomer || len(rows) == 0 {
		return statements
	}
	out := make([]Statement, len(statements))
	copy(out, statements)
	index := map[string]int{}
	for i := range out {
		index[out[i].Counterparty] = i
	}
	byUser := map[string]map[int]*StatementFundingLine{}
	for _, row := range rows {
		key, label, group := fundingParty(row)
		i, ok := index[key]
		if !ok {
			out = append(out, Statement{
				Kind: kind, Counterparty: key, Label: label, Group: group,
				PeriodStart: periodStart, PeriodEnd: periodEnd, Lines: []StatementLine{},
			})
			i = len(out) - 1
			index[key] = i
		}
		lines, ok := byUser[key]
		if !ok {
			lines = map[int]*StatementFundingLine{}
			byUser[key] = lines
		}
		line, ok := lines[row.UserID]
		if !ok {
			name := row.Username
			if name == "" {
				name = "user " + strconv.Itoa(row.UserID)
			}
			line = &StatementFundingLine{UserID: row.UserID, Username: name}
			lines[row.UserID] = line
		}
		if row.Provider == model.PaymentProviderAdmin {
			line.Grants += row.Orders
		} else {
			line.Orders += row.Orders
		}
		line.CreditedUSD += row.CreditedUSD
		out[i].FundedUSD += row.CreditedUSD
	}
	for key, lines := range byUser {
		sorted := make([]StatementFundingLine, 0, len(lines))
		for _, line := range lines {
			sorted = append(sorted, *line)
		}
		sort.SliceStable(sorted, func(a, b int) bool {
			if sorted[a].CreditedUSD != sorted[b].CreditedUSD {
				return sorted[a].CreditedUSD > sorted[b].CreditedUSD
			}
			return sorted[a].UserID < sorted[b].UserID
		})
		out[index[key]].Funding = sorted
	}
	return out
}

// fundingParty keys a receipt the way statementParty keys usage, so the money
// lands on the same statement as the consumption it paid for.
func fundingParty(row model.FundingRow) (key, label, group string) {
	if row.CustomerGroup == "" || row.CustomerGroup == systemDefaultGroup {
		key = strconv.Itoa(row.UserID)
		label = row.Username
		if label == "" {
			label = "user " + key
		}
		return key, label, row.CustomerGroup
	}
	return model.CustomerPricingGroupKey(row.CustomerGroup), row.CustomerGroup, row.CustomerGroup
}

// statementAmount is the whole asymmetry between the two directions, in one
// place: what we charged, versus what we think we were charged.
func statementAmount(row model.UsageRow, kind StatementKind) (amount float64, priced bool) {
	if kind == StatementKindCustomer {
		// Read, not derived. A customer bill that recomputed the price could
		// disagree with the deduction the customer already saw.
		return RevenueUSD(row.Quota), true
	}
	return ratio_setting.UpstreamCostUSD(row.Model, row.ChannelID, usageOf(row))
}

// systemDefaultGroup is the pricing group every account starts in. It is a
// starting point, not a customer.
const systemDefaultGroup = "default"

func statementParty(row model.UsageRow, kind StatementKind) (key, label, group string) {
	if kind == StatementKindCustomer {
		billingGroup := row.BillingGroup
		if billingGroup == "" {
			// Deleted/missing users cannot be resolved against today's account
			// table. Their historical usage must still be billed somewhere.
			billingGroup = row.UserGroup
		}
		// The system default group is not a customer. It is where accounts sit
		// when they belong to nobody in particular, so billing it as one
		// counterparty would put unrelated people on a single invoice with no
		// way to tell their usage apart. Each of them is their own.
		//
		// Deliberately only this group. Every other pricing group is somebody's
		// customer, and re-keying those would split invoices that already
		// carry real money.
		if billingGroup == systemDefaultGroup {
			key = strconv.Itoa(row.UserID)
			label = row.Username
			if label == "" {
				label = "user " + key
			}
			return key, label, billingGroup
		}
		if billingGroup != "" {
			return model.CustomerPricingGroupKey(billingGroup), billingGroup, billingGroup
		}
		// Preserve upstream behaviour for the tenantless/root edge case.
		key = strconv.Itoa(row.UserID)
		label = row.Username
		if label == "" {
			label = "user " + key
		}
		return key, label, ""
	}

	key, label = UpstreamVendor(row)
	return key, label, ""
}

// sortedUserLines orders a customer's logins largest spender first -- the one
// whose figure is worth checking -- with the id as a stable tiebreak.
func sortedUserLines(byUser map[int]*StatementUserLine) []StatementUserLine {
	out := make([]StatementUserLine, 0, len(byUser))
	for _, line := range byUser {
		out = append(out, *line)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].AmountUSD != out[j].AmountUSD {
			return out[i].AmountUSD > out[j].AmountUSD
		}
		return out[i].UserID < out[j].UserID
	})
	return out
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
		if out[i].Model != out[j].Model {
			return out[i].Model < out[j].Model
		}
		return out[i].ChannelID < out[j].ChannelID
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

// AttachFundingToDate records everything paid in up to the end of the period,
// alongside the period's own receipts.
//
// Unlike AttachFunding this never creates a statement. A customer that funded
// long ago and neither paid nor spent this period does not belong in this
// period's list; adding it would fill a month's view with everyone who ever
// paid. This only annotates statements that are already there.
func AttachFundingToDate(statements []Statement, kind StatementKind, rows []model.FundingRow) []Statement {
	if kind != StatementKindCustomer || len(rows) == 0 {
		return statements
	}
	out := make([]Statement, len(statements))
	copy(out, statements)
	index := map[string]int{}
	for i := range out {
		index[out[i].Counterparty] = i
	}
	byUser := map[string]map[int]*StatementFundingLine{}
	for _, row := range rows {
		key, _, _ := fundingParty(row)
		i, ok := index[key]
		if !ok {
			continue
		}
		lines, ok := byUser[key]
		if !ok {
			lines = map[int]*StatementFundingLine{}
			byUser[key] = lines
		}
		line, ok := lines[row.UserID]
		if !ok {
			name := row.Username
			if name == "" {
				name = "user " + strconv.Itoa(row.UserID)
			}
			line = &StatementFundingLine{UserID: row.UserID, Username: name}
			lines[row.UserID] = line
		}
		if row.Provider == model.PaymentProviderAdmin {
			line.Grants += row.Orders
		} else {
			line.Orders += row.Orders
		}
		line.CreditedUSD += row.CreditedUSD
		out[i].FundedToDateUSD += row.CreditedUSD
	}
	for key, lines := range byUser {
		sorted := make([]StatementFundingLine, 0, len(lines))
		for _, line := range lines {
			sorted = append(sorted, *line)
		}
		sort.Slice(sorted, func(a, b int) bool {
			if sorted[a].CreditedUSD != sorted[b].CreditedUSD {
				return sorted[a].CreditedUSD > sorted[b].CreditedUSD
			}
			return sorted[a].UserID < sorted[b].UserID
		})
		out[index[key]].FundingToDate = sorted
	}
	return out
}
