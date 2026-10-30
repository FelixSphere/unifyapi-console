/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

// UNIFYAPI-FORK: the numbers a seller can check against their own vendor
// console.
//
// A seller hands us a key and is paid a share of what it sells. They have to
// take our word for the revenue -- it is our price list and our customers --
// but they do NOT have to take our word for the USAGE, and usage is what the
// share is computed from. Their vendor console shows tokens per day per model
// on their own account, from a source we cannot touch.
//
// So this reports the same shape their console does: day, model, tokens. If
// the two agree, our consumption figure is honest; the revenue then follows
// from it by arithmetic anyone can repeat, because the customer price is
// published. That is the whole trust argument, and it only works if the
// numbers are offered in a form that lines up with theirs rather than in a
// form that suits us.
//
// Deliberately NOT the reconciliation query: that one groups by user and
// username, which is our customers' business and never a seller's.

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// SupplierUsageRow is one day of one model on one seller's keys.
type SupplierUsageRow struct {
	Day              string `json:"day"`
	Model            string `json:"model"`
	Requests         int64  `json:"requests"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CachedTokens     int64  `json:"cached_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	UsageSemantic    string `json:"-"`
	Quota            int64  `json:"-"`

	// ListUSD is what the vendor charged the seller's own account for this
	// row, at the vendor's published price. It is the figure to compare with
	// their console, and the figure their credit balance went down by.
	ListUSD float64 `json:"list_usd"`
	// Priced is false when the model has no catalogue price, so a zero ListUSD
	// reads as "we could not price this", never as "it was free".
	Priced bool `json:"priced"`
	// SoldUSD is what customers actually paid us for this row, and ShareUSD is
	// the seller's cut of it at the share their key was taken on.
	SoldUSD  float64 `json:"sold_usd"`
	ShareUSD float64 `json:"share_usd"`
}

// GetSupplierUsageDetail reports a seller's traffic the way their vendor
// console reports it: by day and model, in tokens, over a half-open window.
func GetSupplierUsageDetail(supplierId int, start, end int64, sharePct float64) ([]SupplierUsageRow, error) {
	if end <= start {
		return nil, fmt.Errorf("end must be after start")
	}
	channelIds, err := supplierChannelIds(supplierId)
	if err != nil {
		return nil, err
	}
	if len(channelIds) == 0 {
		return []SupplierUsageRow{}, nil
	}

	dayExpr := reconcileDayExpression()
	var rows []SupplierUsageRow
	err = LOG_DB.Table("logs").
		Select(dayExpr+` AS day,
			model_name AS model,
			usage_semantic,
			COUNT(*) AS requests,
			COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(cached_tokens), 0) AS cached_tokens,
			COALESCE(SUM(cache_write_tokens), 0) AS cache_write_tokens,
			COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
			COALESCE(SUM(quota), 0) AS quota`).
		Where("type = ?", LogTypeConsume).
		Where("created_at >= ? AND created_at < ?", start, end).
		Where("channel_id IN ?", channelIds).
		Group(dayExpr + ", model_name, usage_semantic").
		Order("day desc, model asc").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	for i := range rows {
		row := &rows[i]
		row.ListUSD, row.Priced = ratio_setting.ListPriceUSD(row.Model, ratio_setting.TokenUsage{
			PromptTokens:     row.PromptTokens,
			CachedTokens:     row.CachedTokens,
			CacheWriteTokens: row.CacheWriteTokens,
			CompletionTokens: row.CompletionTokens,
			Semantic:         row.UsageSemantic,
		})
		if row.Quota > 0 && common.QuotaPerUnit > 0 {
			row.SoldUSD = float64(row.Quota) / common.QuotaPerUnit
		}
		row.ShareUSD = row.SoldUSD * sharePct
	}
	return rows, nil
}

// supplierChannelIds lists every channel a seller's keys have ever been bound
// to, rejected lots included: their traffic is still their traffic.
func supplierChannelIds(supplierId int) ([]int, error) {
	var ids []int
	err := DB.Model(&CreditLot{}).
		Where("supplier_id = ? AND channel_id <> 0", supplierId).
		Distinct().Pluck("channel_id", &ids).Error
	return ids, err
}

// SupplierUsageCSV renders the same rows as a spreadsheet a seller can diff
// against an export from their vendor. Same numbers, same order, no summary
// arithmetic they cannot redo themselves.
func SupplierUsageCSV(rows []SupplierUsageRow) string {
	var b strings.Builder
	b.WriteString("day,model,requests,prompt_tokens,cached_tokens,cache_write_tokens,completion_tokens,vendor_list_usd,sold_usd,your_share_usd\n")
	for _, row := range rows {
		list := fmt.Sprintf("%.6f", row.ListUSD)
		if !row.Priced {
			list = "unpriced"
		}
		b.WriteString(fmt.Sprintf("%s,%s,%d,%d,%d,%d,%d,%s,%.6f,%.6f\n",
			row.Day, row.Model, row.Requests, row.PromptTokens, row.CachedTokens,
			row.CacheWriteTokens, row.CompletionTokens, list, row.SoldUSD, row.ShareUSD))
	}
	return b.String()
}
