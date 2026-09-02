package service

// UNIFYAPI-FORK: reconciliation between what we charged customers and what our
// upstreams charged us.
//
// The two sides are deliberately asymmetric, because their trustworthiness is:
//
//	revenue  is READ FROM THE LEDGER. Every consume log carries the quota that
//	         was actually deducted, already including the model discount, the
//	         group ratio, cache discounts, tiered rules and any clamp. Nothing
//	         is re-derived, so the revenue column cannot disagree with what the
//	         customer was billed -- which is the only property that makes this
//	         usable for invoicing disputes.
//
//	cost     is MODELLED, from token counts x the vendor's official list price
//	         x the channel's cost multiplier. We do not get a per-request
//	         receipt from a vendor, so modelling is the only option. That makes
//	         the cost column an estimate, and the point of the exercise is to
//	         diff it against the vendor's actual invoice: a small variance
//	         validates the model, a large one is a finding.
//
// Everything in this file is a pure function over rows so the arithmetic is
// unit-testable without a database. The query that produces the rows lives in
// model/reconcile_query.go.

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// GroupBy is the dimension a reconciliation report is broken down by.
type GroupBy string

const (
	GroupByModel    GroupBy = "model"
	GroupByChannel  GroupBy = "channel"
	GroupByCustomer GroupBy = "customer"
	GroupByUserTier GroupBy = "group"
	GroupByDay      GroupBy = "day"
	GroupByVendor   GroupBy = "vendor"
)

// ParseGroupBy validates a caller-supplied dimension.
func ParseGroupBy(raw string) (GroupBy, error) {
	switch GroupBy(raw) {
	case GroupByModel, GroupByChannel, GroupByCustomer, GroupByUserTier, GroupByDay, GroupByVendor:
		return GroupBy(raw), nil
	case "":
		return GroupByModel, nil
	default:
		return "", fmt.Errorf("unknown group_by %q; expected one of model, channel, customer, group, day, vendor", raw)
	}
}

// ReconcileLine is one row of the report.
type ReconcileLine struct {
	Key              string  `json:"key"`
	Label            string  `json:"label"`
	Requests         int64   `json:"requests"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CachedTokens     int64   `json:"cached_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	RevenueUSD       float64 `json:"revenue_usd"`
	CostUSD          float64 `json:"cost_usd"`
	MarginUSD        float64 `json:"margin_usd"`
	MarginPct        float64 `json:"margin_pct"`

	// UnpricedRequests counts traffic whose model is not in the catalog, so no
	// cost could be modelled. Surfaced per line rather than dropped, because a
	// line with unpriced traffic has an understated cost and an overstated
	// margin, and a reader has to know that.
	UnpricedRequests int64    `json:"unpriced_requests"`
	UnpricedModels   []string `json:"unpriced_models,omitempty"`
}

// ReconcileReport is the whole answer: lines plus a total computed from the
// same rows, so the total cannot drift from the sum of its parts.
type ReconcileReport struct {
	GroupBy GroupBy         `json:"group_by"`
	Lines   []ReconcileLine `json:"lines"`
	Total   ReconcileLine   `json:"total"`

	// LossMakers lists lines billing below modelled cost, worst first. This is
	// the whole reason the report exists: a mispriced model is invisible in a
	// revenue chart and obvious here.
	LossMakers []ReconcileLine `json:"loss_makers,omitempty"`
}

// RevenueUSD converts deducted quota into dollars. QuotaPerUnit is how many
// quota units make one dollar (500,000 by default).
func RevenueUSD(quota int64) float64 {
	if common.QuotaPerUnit == 0 {
		return 0
	}
	return float64(quota) / common.QuotaPerUnit
}

// Reconcile aggregates usage rows into a report along one dimension.
func Reconcile(rows []model.UsageRow, groupBy GroupBy) ReconcileReport {
	type bucket struct {
		line     ReconcileLine
		unpriced map[string]bool
	}
	buckets := map[string]*bucket{}
	order := []string{}

	for _, row := range rows {
		key, label := reconcileKey(row, groupBy)
		entry, ok := buckets[key]
		if !ok {
			entry = &bucket{
				line:     ReconcileLine{Key: key, Label: label},
				unpriced: map[string]bool{},
			}
			buckets[key] = entry
			order = append(order, key)
		}

		entry.line.Requests += row.Requests
		entry.line.PromptTokens += row.PromptTokens
		entry.line.CachedTokens += row.CachedTokens
		entry.line.CompletionTokens += row.CompletionTokens
		entry.line.RevenueUSD += RevenueUSD(row.Quota)

		cost, priced := ratio_setting.UpstreamCostUSD(
			row.Model, row.ChannelID, row.PromptTokens, row.CachedTokens, row.CompletionTokens)
		if priced {
			entry.line.CostUSD += cost
		} else {
			entry.line.UnpricedRequests += row.Requests
			entry.unpriced[row.Model] = true
		}
	}

	report := ReconcileReport{GroupBy: groupBy}
	for _, key := range order {
		entry := buckets[key]
		line := entry.line
		line.UnpricedModels = sortedKeys(entry.unpriced)
		finalizeLine(&line)
		report.Lines = append(report.Lines, line)

		report.Total.Requests += line.Requests
		report.Total.PromptTokens += line.PromptTokens
		report.Total.CachedTokens += line.CachedTokens
		report.Total.CompletionTokens += line.CompletionTokens
		report.Total.RevenueUSD += line.RevenueUSD
		report.Total.CostUSD += line.CostUSD
		report.Total.UnpricedRequests += line.UnpricedRequests
	}

	// Biggest revenue first: a 40% margin on the top model matters more than a
	// 90% margin on a model nobody calls.
	sort.SliceStable(report.Lines, func(i, j int) bool {
		return report.Lines[i].RevenueUSD > report.Lines[j].RevenueUSD
	})

	report.Total.Key = "total"
	report.Total.Label = "total"
	finalizeLine(&report.Total)

	for _, line := range report.Lines {
		if line.MarginUSD < 0 {
			report.LossMakers = append(report.LossMakers, line)
		}
	}
	sort.SliceStable(report.LossMakers, func(i, j int) bool {
		return report.LossMakers[i].MarginUSD < report.LossMakers[j].MarginUSD
	})

	return report
}

// finalizeLine derives margin from revenue and cost.
//
// MarginPct is a percentage OF REVENUE, so it stays bounded and comparable
// across lines. It is left at zero when there is no revenue -- a line with cost
// and no revenue has an undefined margin percentage, and reporting -100% or an
// infinity there would both be lies.
func finalizeLine(line *ReconcileLine) {
	line.MarginUSD = line.RevenueUSD - line.CostUSD
	if line.RevenueUSD != 0 {
		line.MarginPct = line.MarginUSD / line.RevenueUSD * 100
	}
}

func reconcileKey(row model.UsageRow, groupBy GroupBy) (key, label string) {
	switch groupBy {
	case GroupByChannel:
		key = strconv.Itoa(row.ChannelID)
		label = row.ChannelName
		if label == "" {
			label = "channel " + key
		}
	case GroupByCustomer:
		if row.TenantID > 0 {
			key = "tenant:" + strconv.Itoa(row.TenantID)
			label = row.TenantName
			if label == "" {
				label = "customer " + strconv.Itoa(row.TenantID)
			}
		} else {
			key = "user:" + strconv.Itoa(row.UserID)
			label = row.Username
			if label == "" {
				label = "user " + strconv.Itoa(row.UserID)
			}
		}
	case GroupByUserTier:
		key, label = row.UserGroup, row.UserGroup
	case GroupByDay:
		key, label = row.Day, row.Day
	case GroupByVendor:
		key = "unlisted"
		if entry, ok := ratio_setting.CatalogEntryFor(row.Model); ok && entry.Vendor != "" {
			key = entry.Vendor
		}
		label = key
	default:
		key, label = row.Model, row.Model
	}
	if key == "" {
		key, label = "(unset)", "(unset)"
	}
	return key, label
}

func sortedKeys(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// VendorVariance compares modelled upstream cost against a vendor's actual
// invoice. This is the step that turns an estimate into an audited number.
type VendorVariance struct {
	Vendor      string  `json:"vendor"`
	ModelledUSD float64 `json:"modelled_usd"`
	InvoicedUSD float64 `json:"invoiced_usd"`
	VarianceUSD float64 `json:"variance_usd"`
	VariancePct float64 `json:"variance_pct"`
	Verdict     string  `json:"verdict"`
}

// varianceTolerancePct is how far modelled cost may sit from an invoice before
// it is worth investigating. Some drift is expected and benign: vendors bill in
// their own rounding, tokenizer counts differ slightly from ours, and a request
// that failed after we counted tokens is billed by neither side identically.
const varianceTolerancePct = 2.0

// CompareVendorInvoices diffs modelled cost per vendor against invoiced
// amounts. Vendors present on either side appear in the result, because a
// vendor we modelled but were not invoiced for -- and one we were invoiced for
// but never modelled -- are both findings.
func CompareVendorInvoices(report ReconcileReport, invoiced map[string]float64) []VendorVariance {
	if report.GroupBy != GroupByVendor {
		return nil
	}

	modelled := map[string]float64{}
	for _, line := range report.Lines {
		modelled[line.Key] = line.CostUSD
	}

	vendors := map[string]bool{}
	for vendor := range modelled {
		vendors[vendor] = true
	}
	for vendor := range invoiced {
		vendors[vendor] = true
	}

	out := make([]VendorVariance, 0, len(vendors))
	for _, vendor := range sortedKeys(vendors) {
		variance := VendorVariance{
			Vendor:      vendor,
			ModelledUSD: modelled[vendor],
			InvoicedUSD: invoiced[vendor],
		}
		variance.VarianceUSD = variance.InvoicedUSD - variance.ModelledUSD
		switch {
		case variance.ModelledUSD != 0:
			variance.VariancePct = variance.VarianceUSD / variance.ModelledUSD * 100
		case variance.InvoicedUSD != 0:
			variance.VariancePct = 100
		}
		variance.Verdict = varianceVerdict(variance)
		out = append(out, variance)
	}
	return out
}

func varianceVerdict(variance VendorVariance) string {
	switch {
	case variance.ModelledUSD == 0 && variance.InvoicedUSD == 0:
		return "no activity"
	case variance.ModelledUSD == 0:
		return "invoiced but nothing modelled: traffic went somewhere our logs do not attribute to this vendor"
	case variance.InvoicedUSD == 0:
		return "modelled but not invoiced: either the invoice is missing or the channel is not this vendor"
	case abs(variance.VariancePct) <= varianceTolerancePct:
		return "reconciled"
	case variance.VarianceUSD > 0:
		return "invoice exceeds the model: check for a cost multiplier that is set too low, or untracked usage"
	default:
		return "invoice below the model: check for a negotiated rate we have not configured"
	}
}

func abs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
