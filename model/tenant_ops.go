/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"errors"
)

// Operator-facing tenant reporting: who our customers are, what they are
// running, and what they have spent.
//
// Upstream has no equivalent. Its admin surfaces (GetAllUsers, GetAllLogs,
// GetAllQuotaDates) are per-user and global, so there is no way to answer
// "which customers do we have and what is each one costing/earning us" without
// aggregating by hand.
//
// Spend is derived from the consume logs rather than from tenants.used_quota so
// it can be scoped to a time window. used_quota is the lifetime counter and is
// reported alongside it.

// TenantOverview is one row of the operations list.
type TenantOverview struct {
	TenantId  int    `json:"tenant_id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	Status    int    `json:"status"`
	Group     string `json:"group"`
	CreatedAt int64  `json:"created_at"`

	MemberCount int `json:"member_count"`

	// Balance remaining and lifetime consumption, both in quota units.
	Quota     int `json:"quota"`
	UsedQuota int `json:"used_quota"`

	// Windowed activity, from the consume logs.
	PeriodQuota            int   `json:"period_quota"`
	PeriodRequests         int   `json:"period_requests"`
	PeriodPromptTokens     int   `json:"period_prompt_tokens"`
	PeriodCompletionTokens int   `json:"period_completion_tokens"`
	LastActivityAt         int64 `json:"last_activity_at"`
}

// TenantModelUsage is per-model spend for a single tenant.
type TenantModelUsage struct {
	ModelName        string `json:"model_name"`
	Requests         int    `json:"requests"`
	Quota            int    `json:"quota"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
}

// GetTenantOverviews lists tenants with balance, membership, and spend over
// [startAt, endAt] (unix seconds; pass 0 for either to leave that side open).
//
// Two queries rather than one join: aggregating members and logs in a single
// statement multiplies rows and inflates the sums. Kept explicit because a
// silently wrong revenue number is worse than a second round trip.
func GetTenantOverviews(startAt int64, endAt int64, limit int, offset int) ([]*TenantOverview, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	var rows []*TenantOverview
	err := DB.Model(&Tenant{}).
		Select("id as tenant_id, name, slug, status, `group`, created_at, quota, used_quota").
		Order("id asc").
		Limit(limit).
		Offset(offset).
		Find(&rows).Error
	if err != nil {
		// `group` is reserved; MySQL needs backticks, Postgres needs double
		// quotes. Retry with the portable quoted form before giving up.
		var retry []*TenantOverview
		if retryErr := DB.Model(&Tenant{}).
			Select(`id as tenant_id, name, slug, status, "group", created_at, quota, used_quota`).
			Order("id asc").Limit(limit).Offset(offset).Find(&retry).Error; retryErr != nil {
			return nil, err
		}
		rows = retry
	}
	if len(rows) == 0 {
		return rows, nil
	}

	byId := make(map[int]*TenantOverview, len(rows))
	ids := make([]int, 0, len(rows))
	for _, r := range rows {
		byId[r.TenantId] = r
		ids = append(ids, r.TenantId)
	}

	// Membership counts.
	type memberCount struct {
		TenantId int
		Total    int
	}
	var members []memberCount
	if err := DB.Model(&User{}).
		Select("tenant_id, count(*) as total").
		Where("tenant_id in ?", ids).
		Group("tenant_id").
		Find(&members).Error; err != nil {
		return nil, err
	}
	for _, m := range members {
		if row, ok := byId[m.TenantId]; ok {
			row.MemberCount = m.Total
		}
	}

	// Windowed spend. Logs carry user_id, not tenant_id, so join through users.
	// A dedicated logs.tenant_id column would let this read the pre-aggregated
	// QuotaData cube instead -- worth doing before log volume gets large.
	type spendRow struct {
		TenantId         int
		Quota            int
		Requests         int
		PromptTokens     int
		CompletionTokens int
		LastActivityAt   int64
	}
	spendQuery := DB.Table("logs").
		Select(`users.tenant_id as tenant_id,
			coalesce(sum(logs.quota),0) as quota,
			count(*) as requests,
			coalesce(sum(logs.prompt_tokens),0) as prompt_tokens,
			coalesce(sum(logs.completion_tokens),0) as completion_tokens,
			coalesce(max(logs.created_at),0) as last_activity_at`).
		Joins("join users on users.id = logs.user_id").
		Where("users.tenant_id in ?", ids).
		Where("logs.type = ?", LogTypeConsume).
		Group("users.tenant_id")
	if startAt > 0 {
		spendQuery = spendQuery.Where("logs.created_at >= ?", startAt)
	}
	if endAt > 0 {
		spendQuery = spendQuery.Where("logs.created_at <= ?", endAt)
	}

	var spend []spendRow
	if err := spendQuery.Find(&spend).Error; err != nil {
		return nil, err
	}
	for _, s := range spend {
		if row, ok := byId[s.TenantId]; ok {
			row.PeriodQuota = s.Quota
			row.PeriodRequests = s.Requests
			row.PeriodPromptTokens = s.PromptTokens
			row.PeriodCompletionTokens = s.CompletionTokens
			row.LastActivityAt = s.LastActivityAt
		}
	}

	return rows, nil
}

func CountTenants() (int64, error) {
	var total int64
	err := DB.Model(&Tenant{}).Count(&total).Error
	return total, err
}

// GetTenantModelUsage breaks one tenant's spend down by model -- the "what are
// they actually doing" view.
func GetTenantModelUsage(tenantId int, startAt int64, endAt int64) ([]*TenantModelUsage, error) {
	if tenantId == 0 {
		return nil, errors.New("tenant id is empty")
	}

	query := DB.Table("logs").
		Select(`logs.model_name as model_name,
			count(*) as requests,
			coalesce(sum(logs.quota),0) as quota,
			coalesce(sum(logs.prompt_tokens),0) as prompt_tokens,
			coalesce(sum(logs.completion_tokens),0) as completion_tokens`).
		Joins("join users on users.id = logs.user_id").
		Where("users.tenant_id = ?", tenantId).
		Where("logs.type = ?", LogTypeConsume).
		Group("logs.model_name").
		Order("quota desc")
	if startAt > 0 {
		query = query.Where("logs.created_at >= ?", startAt)
	}
	if endAt > 0 {
		query = query.Where("logs.created_at <= ?", endAt)
	}

	var usage []*TenantModelUsage
	err := query.Find(&usage).Error
	return usage, err
}
