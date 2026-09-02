package model

// UNIFYAPI-FORK: the log query behind the reconciliation report.
//
// It aggregates in SQL rather than streaming rows into Go, because a month of
// traffic is millions of consume logs and the report only ever needs sums. The
// grouping is the finest useful grain -- day x model x channel x user x group --
// so one query can serve every breakdown the report offers; service.Reconcile
// then folds it down to whichever dimension was asked for.

import (
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// maxReconcileRows bounds the result set. At day x model x channel x user grain
// this is a lot of distinct combinations; the cap exists so a careless date
// range cannot try to materialise a year of them at once. Hitting it is
// reported, never silently truncated -- a reconciliation report that quietly
// dropped rows would be worse than no report.
const maxReconcileRows = 200_000

// UsageRow is one pre-aggregated slice of consumption. It is whatever the log
// query grouped by, so several rows can share a model or a channel.
type UsageRow struct {
	Day              string
	Model            string
	UserGroup        string
	Username         string
	UserID           int
	TenantID         int
	TenantName       string
	ChannelID        int
	ChannelName      string
	Requests         int64
	PromptTokens     int64
	CachedTokens     int64
	CompletionTokens int64
	Quota            int64 // quota actually deducted from the customer
}

// ReconcileQuery bounds a reconciliation run.
type ReconcileQuery struct {
	StartTimestamp int64
	EndTimestamp   int64
	ModelName      string
	Username       string
	ChannelID      int
	UserGroup      string
}

// reconcileScanRow mirrors the SELECT list. It is separate from
// service.UsageRow so a column rename cannot silently change the report's
// meaning: the mapping is written out once, below.
type reconcileScanRow struct {
	Day              string
	ModelName        string
	UserGroup        string
	Username         string
	UserID           int
	TenantID         int
	ChannelID        int
	Requests         int64
	PromptTokens     int64
	CachedTokens     int64
	CompletionTokens int64
	Quota            int64
}

// FetchReconcileUsage returns pre-aggregated consumption for the window.
//
// Only LogTypeConsume rows are counted. Top-ups, refunds and management events
// are not consumption and must not appear on either side of a margin: a refund
// row carries a negative-ish quota meaning and would corrupt revenue.
func FetchReconcileUsage(query ReconcileQuery) ([]UsageRow, bool, error) {
	if query.EndTimestamp <= query.StartTimestamp {
		return nil, false, fmt.Errorf("end must be after start")
	}

	// Bucket by local day so a report lines up with how the operator reads a
	// calendar. Timestamps are unix seconds, so the arithmetic is done in SQL
	// on the driver's own clock only for the label; every sum is timestamp
	// bounded and therefore timezone independent.
	dayExpr := reconcileDayExpression()

	tx := LOG_DB.Table("logs").
		Select(dayExpr+` AS day,
			model_name,
			`+quoteColumn("group")+` AS user_group,
			username,
			user_id,
			tenant_id,
			channel_id,
			COUNT(*) AS requests,
			COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(cached_tokens), 0) AS cached_tokens,
			COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
			COALESCE(SUM(quota), 0) AS quota`).
		Where("type = ?", LogTypeConsume).
		Where("created_at >= ? AND created_at < ?", query.StartTimestamp, query.EndTimestamp).
		Group(dayExpr + ", model_name, " + quoteColumn("group") + ", username, user_id, tenant_id, channel_id")

	if query.ModelName != "" {
		tx = tx.Where("model_name = ?", query.ModelName)
	}
	if query.Username != "" {
		tx = tx.Where("username = ?", query.Username)
	}
	if query.ChannelID != 0 {
		tx = tx.Where("channel_id = ?", query.ChannelID)
	}
	if query.UserGroup != "" {
		tx = tx.Where(quoteColumn("group")+" = ?", query.UserGroup)
	}

	var scanned []reconcileScanRow
	if err := tx.Limit(maxReconcileRows + 1).Scan(&scanned).Error; err != nil {
		return nil, false, err
	}

	truncated := len(scanned) > maxReconcileRows
	if truncated {
		scanned = scanned[:maxReconcileRows]
	}

	channelNames := reconcileChannelNames(scanned)
	tenantNames := reconcileTenantNames(scanned)

	rows := make([]UsageRow, 0, len(scanned))
	for _, row := range scanned {
		rows = append(rows, UsageRow{
			Day:              row.Day,
			Model:            row.ModelName,
			UserGroup:        row.UserGroup,
			Username:         row.Username,
			UserID:           row.UserID,
			TenantID:         row.TenantID,
			TenantName:       tenantNames[row.TenantID],
			ChannelID:        row.ChannelID,
			ChannelName:      channelNames[row.ChannelID],
			Requests:         row.Requests,
			PromptTokens:     row.PromptTokens,
			CachedTokens:     row.CachedTokens,
			CompletionTokens: row.CompletionTokens,
			Quota:            row.Quota,
		})
	}
	return rows, truncated, nil
}

func reconcileTenantNames(rows []reconcileScanRow) map[int]string {
	ids := map[int]bool{}
	for _, row := range rows {
		if row.TenantID > 0 {
			ids[row.TenantID] = true
		}
	}
	if len(ids) == 0 {
		return map[int]string{}
	}

	list := make([]int, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	var tenants []struct {
		Id   int
		Name string
	}
	names := make(map[int]string, len(list))
	if err := DB.Table("tenants").Select("id, name").Where("id IN ?", list).Find(&tenants).Error; err != nil {
		common.SysError("reconcile: failed to resolve tenant names: " + err.Error())
		return names
	}
	for _, tenant := range tenants {
		names[tenant.Id] = tenant.Name
	}
	return names
}

// reconcileDayExpression renders a unix timestamp as YYYY-MM-DD per dialect.
// GORM will not do this for us and the three databases spell it three ways.
// It keys off the LOG database's type, not the main one -- logs can live in a
// separate store (including ClickHouse), and that is the one this query runs
// against.
func reconcileDayExpression() string {
	switch {
	case common.UsingLogDatabase(common.DatabaseTypePostgreSQL):
		return "TO_CHAR(TO_TIMESTAMP(created_at), 'YYYY-MM-DD')"
	case common.UsingLogDatabase(common.DatabaseTypeSQLite):
		return "STRFTIME('%Y-%m-%d', created_at, 'unixepoch')"
	case common.UsingLogDatabase(common.DatabaseTypeClickHouse):
		return "formatDateTime(toDateTime(created_at), '%F')"
	default: // MySQL
		return "DATE_FORMAT(FROM_UNIXTIME(created_at), '%Y-%m-%d')"
	}
}

// quoteColumn quotes an identifier that collides with a reserved word --
// `group` does on every dialect we support.
func quoteColumn(name string) string {
	if common.UsingLogDatabase(common.DatabaseTypeMySQL) {
		return "`" + name + "`"
	}
	return `"` + name + `"`
}

// reconcileChannelNames resolves channel ids to names in one query, so a report
// grouped by channel is readable without N lookups.
func reconcileChannelNames(rows []reconcileScanRow) map[int]string {
	ids := map[int]bool{}
	for _, row := range rows {
		if row.ChannelID != 0 {
			ids[row.ChannelID] = true
		}
	}
	if len(ids) == 0 {
		return map[int]string{}
	}

	list := make([]int, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}

	var channels []struct {
		Id   int
		Name string
	}
	names := make(map[int]string, len(list))
	// A failure here costs labels, not numbers, so the report still stands.
	if err := DB.Table("channels").Select("id, name").Where("id IN ?", list).Find(&channels).Error; err != nil {
		common.SysError("reconcile: failed to resolve channel names: " + err.Error())
		return names
	}
	for _, channel := range channels {
		names[channel.Id] = channel.Name
	}
	return names
}

// ParseReconcileWindow turns caller-supplied dates into a timestamp range.
//
// `end` is treated as inclusive of that whole day, because "2026-08-01 to
// 2026-08-31" has to mean August, not August minus its last day -- the
// off-by-one that makes a monthly report disagree with an invoice.
func ParseReconcileWindow(start, end string) (int64, int64, error) {
	const layout = "2006-01-02"
	if start == "" || end == "" {
		return 0, 0, fmt.Errorf("start and end are required, as YYYY-MM-DD")
	}
	from, err := time.ParseInLocation(layout, start, time.Local)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid start %q: expected YYYY-MM-DD", start)
	}
	to, err := time.ParseInLocation(layout, end, time.Local)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid end %q: expected YYYY-MM-DD", end)
	}
	if to.Before(from) {
		return 0, 0, fmt.Errorf("end %s is before start %s", end, start)
	}
	return from.Unix(), to.AddDate(0, 0, 1).Unix(), nil
}
