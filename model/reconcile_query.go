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
	"net/url"
	"strings"
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
	Day   string
	Model string
	// UserGroup is the immutable request-time group. BillingGroup is the
	// account's current Pricing Group, used as the customer invoice owner.
	UserGroup        string
	BillingGroup     string
	Username         string
	UserID           int
	ChannelID        int
	ChannelName      string
	ChannelBaseURL   string
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

// NormalizeChannelBaseURL keeps the endpoint useful as an accounting
// dimension: host casing, credentials, query strings and trailing slashes do
// not create fake suppliers or fake channel-detail rows. The path is retained
// because it can distinguish contracts on the same host.
func NormalizeChannelBaseURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimRight(raw, "/")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""
	return strings.TrimRight(parsed.String(), "/")
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
	ChannelID        int
	ChannelBaseURL   string
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
			channel_id,
			channel_base_url,
			COUNT(*) AS requests,
			COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(cached_tokens), 0) AS cached_tokens,
			COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
			COALESCE(SUM(quota), 0) AS quota`).
		Where("type = ?", LogTypeConsume).
		Where("created_at >= ? AND created_at < ?", query.StartTimestamp, query.EndTimestamp).
		Group(dayExpr + ", model_name, " + quoteColumn("group") + ", username, user_id, channel_id, channel_base_url")

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

	channelDetails := reconcileChannelDetails(scanned)
	userIDs := make([]int, 0, len(scanned))
	for _, row := range scanned {
		userIDs = append(userIDs, row.UserID)
	}
	currentGroups, err := ResolveCurrentCustomerPricingGroups(userIDs)
	if err != nil {
		return nil, false, err
	}

	rows := make([]UsageRow, 0, len(scanned))
	for _, row := range scanned {
		detail := channelDetails[row.ChannelID]
		baseURL := NormalizeChannelBaseURL(row.ChannelBaseURL)
		if baseURL == "" {
			// Rows written before channel_base_url was added have no snapshot.
			// Falling back to the current channel is explicitly best-effort; new
			// rows never take this branch and remain historically stable.
			baseURL = NormalizeChannelBaseURL(detail.BaseURL)
		}
		rows = append(rows, UsageRow{
			Day:              row.Day,
			Model:            row.ModelName,
			UserGroup:        row.UserGroup,
			BillingGroup:     currentGroups[row.UserID],
			Username:         row.Username,
			UserID:           row.UserID,
			ChannelID:        row.ChannelID,
			ChannelName:      detail.Name,
			ChannelBaseURL:   baseURL,
			Requests:         row.Requests,
			PromptTokens:     row.PromptTokens,
			CachedTokens:     row.CachedTokens,
			CompletionTokens: row.CompletionTokens,
			Quota:            row.Quota,
		})
	}
	return rows, truncated, nil
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

type reconcileChannelDetail struct {
	Name    string
	BaseURL string
}

// reconcileChannelDetails resolves display names and legacy-row URL fallbacks
// in one query, so a report never performs N channel lookups.
func reconcileChannelDetails(rows []reconcileScanRow) map[int]reconcileChannelDetail {
	ids := map[int]bool{}
	for _, row := range rows {
		if row.ChannelID != 0 {
			ids[row.ChannelID] = true
		}
	}
	if len(ids) == 0 {
		return map[int]reconcileChannelDetail{}
	}

	list := make([]int, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}

	var channels []Channel
	details := make(map[int]reconcileChannelDetail, len(list))
	// A failure here costs labels, not numbers, so the report still stands.
	if err := DB.Table("channels").Select("id, name, type, base_url").Where("id IN ?", list).Find(&channels).Error; err != nil {
		common.SysError("reconcile: failed to resolve channel names: " + err.Error())
		return details
	}
	for _, channel := range channels {
		details[channel.Id] = reconcileChannelDetail{
			Name:    channel.Name,
			BaseURL: channel.GetBaseURL(),
		}
	}
	return details
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
