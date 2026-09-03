package model

// UNIFYAPI-FORK: money actually received from a customer in a period.
//
// A usage statement on its own answers "what did they consume". A settlement
// also has to answer "what have they already paid", or the operator is doing
// that half in a spreadsheet -- which is where the arithmetic errors live.
//
// The awkward part is that the amount credited by a top-up is NOT one formula.
// model/topup.go credits per provider:
//
//	stripe          quota = Money  x QuotaPerUnit
//	epay (non-stripe) quota = Amount x QuotaPerUnit
//	waffo           quota = Amount x QuotaPerUnit
//	waffo_pancake   quota = Amount x QuotaPerUnit
//	creem           quota = Amount            <- raw quota, NOT multiplied
//
// So `Amount` means dollars for four providers and quota units for the fifth.
// creditedQuota below mirrors those branches exactly and is pinned per provider
// by TestCreditedQuotaMatchesEachProvider. If a new gateway is added to
// topup.go, it has to be added here too -- a provider that falls through to the
// default is counted at the wrong scale by a factor of QuotaPerUnit, which is
// 500,000 by default and would not look like a rounding error.

import (
	"sort"
	"strconv"

	"github.com/QuantumNous/new-api/common"
)

// CustomerPayment is one period's receipts from one customer, split by gateway
// because that is the grain a payment processor's own statement arrives at.
type CustomerPayment struct {
	UserID       int     `json:"user_id"`
	Provider     string  `json:"provider"`
	Orders       int64   `json:"orders"`
	CreditedUSD  float64 `json:"credited_usd"`
	ChargedMoney float64 `json:"charged_money"`
}

// topUpScanRow mirrors the SELECT list; kept separate from TopUp so a schema
// change cannot silently alter what is summed.
type topUpScanRow struct {
	UserId          int
	CustomerGroup   string
	PaymentProvider string
	Amount          int64
	Money           float64
}

func fetchCustomerPaymentRows(startTimestamp, endTimestamp int64) ([]topUpScanRow, error) {
	var rows []topUpScanRow
	err := DB.Table("top_ups").
		Select("user_id, customer_group, payment_provider, amount, money").
		Where("status = ?", common.TopUpStatusSuccess).
		Where("complete_time >= ? AND complete_time < ?", startTimestamp, endTimestamp).
		Scan(&rows).Error
	return rows, err
}

// FetchCustomerPayments returns successful top-ups completed inside the window.
//
// Bounded by CompleteTime, not CreateTime: an order created in July and paid in
// August is August's money. Rows are summed in Go rather than SQL because the
// credited amount is a per-provider branch, not an expression.
func FetchCustomerPayments(startTimestamp, endTimestamp int64) (map[int][]CustomerPayment, error) {
	rows, err := fetchCustomerPaymentRows(startTimestamp, endTimestamp)
	if err != nil {
		return nil, err
	}

	type key struct {
		userID   int
		provider string
	}
	totals := map[key]*CustomerPayment{}
	for _, row := range rows {
		provider := row.PaymentProvider
		if provider == "" {
			provider = "unknown"
		}
		id := key{userID: row.UserId, provider: provider}
		entry, ok := totals[id]
		if !ok {
			entry = &CustomerPayment{UserID: row.UserId, Provider: provider}
			totals[id] = entry
		}
		entry.Orders++
		entry.CreditedUSD += creditedUSD(provider, row.Amount, row.Money)
		entry.ChargedMoney += row.Money
	}

	out := map[int][]CustomerPayment{}
	for id, payment := range totals {
		out[id.userID] = append(out[id.userID], *payment)
	}
	return out, nil
}

// FetchCustomerPaymentsByCustomer returns receipts at the same company grain
// as customer statements. A company may have many login users; their top-ups
// must all appear beside the one company invoice. New rows use the immutable
// CustomerGroup snapshot. Legacy rows fall back to the user's current group,
// or to the user id for tenantless/operator accounts.
func FetchCustomerPaymentsByCustomer(startTimestamp, endTimestamp int64) (map[string][]CustomerPayment, error) {
	rows, err := fetchCustomerPaymentRows(startTimestamp, endTimestamp)
	if err != nil {
		return nil, err
	}

	legacyUserIDs := make([]int, 0)
	seenUserID := map[int]bool{}
	for _, row := range rows {
		if row.CustomerGroup == "" && !seenUserID[row.UserId] {
			seenUserID[row.UserId] = true
			legacyUserIDs = append(legacyUserIDs, row.UserId)
		}
	}
	currentGroups := map[int]string{}
	if len(legacyUserIDs) > 0 {
		var users []User
		if err := DB.Select("Id", "Group").Where("id IN ?", legacyUserIDs).Find(&users).Error; err != nil {
			return nil, err
		}
		for _, user := range users {
			currentGroups[user.Id] = user.Group
		}
	}

	type key struct {
		customer string
		provider string
	}
	totals := map[key]*CustomerPayment{}
	for _, row := range rows {
		customer := row.CustomerGroup
		if customer == "" {
			customer = currentGroups[row.UserId]
		}
		if customer == "" {
			customer = strconv.Itoa(row.UserId)
		} else {
			customer = CustomerPricingGroupKey(customer)
		}
		provider := row.PaymentProvider
		if provider == "" {
			provider = "unknown"
		}
		id := key{customer: customer, provider: provider}
		entry, ok := totals[id]
		if !ok {
			entry = &CustomerPayment{Provider: provider}
			totals[id] = entry
		}
		entry.Orders++
		entry.CreditedUSD += creditedUSD(provider, row.Amount, row.Money)
		entry.ChargedMoney += row.Money
	}

	out := map[string][]CustomerPayment{}
	for id, payment := range totals {
		out[id.customer] = append(out[id.customer], *payment)
	}
	for customer := range out {
		sort.Slice(out[customer], func(i, j int) bool {
			return out[customer][i].Provider < out[customer][j].Provider
		})
	}
	return out, nil
}

// creditedUSD converts a top-up row into the dollars of quota it credited.
// It mirrors model/topup.go's per-provider branches; see the file header.
func creditedUSD(provider string, amount int64, money float64) float64 {
	quota := creditedQuota(provider, amount, money)
	if common.QuotaPerUnit == 0 {
		return 0
	}
	return quota / common.QuotaPerUnit
}

// creditedQuota is the branch table from model/topup.go, in one place.
func creditedQuota(provider string, amount int64, money float64) float64 {
	switch provider {
	case PaymentProviderStripe:
		return money * common.QuotaPerUnit
	case PaymentProviderCreem:
		// Creem writes quota units into Amount directly. Multiplying here would
		// overstate every Creem payment by QuotaPerUnit.
		return float64(amount)
	default:
		// epay, waffo, waffo_pancake and anything added later: Amount is
		// dollars.
		return float64(amount) * common.QuotaPerUnit
	}
}
