package controller

// UNIFYAPI-FORK: the scheduled reconciliation run.
//
// An on-demand report is only a control if somebody remembers to open it. This
// handler runs the same computation on a schedule, stores the result, and records
// what needs acting on -- so a model that goes underwater is found within a day
// instead of at quarter close.
//
// It rides the existing system-task framework rather than a goroutine of its own:
// that gives it a database lease (so two master instances do not both run it), a
// task row per run for the ops page, and cancellation when the lease is lost.
//
// It reconciles YESTERDAY, never today. A partial day looks like a margin
// collapse: pre-consumed quota is deducted at request time and settled after, so
// an in-flight day's revenue lags its token counts.

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// reconcileDimensions are the breakdowns snapshotted on every run.
//
// Three, not all six: model answers "what is mispriced", vendor answers "does
// the invoice match", customer answers "which account is unprofitable". Channel
// and day are derivable from the stored report when someone needs them, and
// snapshotting every dimension every night is a lot of duplicated JSON.
var reconcileDimensions = []service.GroupBy{
	service.GroupByModel,
	service.GroupByVendor,
	service.GroupByCustomer,
}

type reconcileHandler struct{}

func (reconcileHandler) Type() string { return model.SystemTaskTypeReconcile }

// Enabled defaults to ON. Reconciliation that has to be switched on is
// reconciliation nobody switches on; the run is a couple of aggregate queries
// against an indexed table once a day.
//
// Safe to leave on before any purchasing cost is entered: with no
// ChannelCostRatio configured, EvaluateReconcileAlerts reports one "no cost
// basis" warning instead of flagging the entire catalog as loss-making. The
// snapshots it writes in that state still carry real revenue and token counts.
func (reconcileHandler) Enabled() bool {
	value, ok := common.OptionMap["ReconcileEnabled"]
	if !ok || value == "" {
		return true
	}
	return value == "true"
}

// Interval is a check cadence, not the reporting period. The handler itself
// decides which day still needs a snapshot, so waking more often than daily is
// harmless and means a snapshot appears soon after a restart.
func (reconcileHandler) Interval() time.Duration { return 6 * time.Hour }

func (reconcileHandler) NewPayload() any { return nil }

// reconcileTaskPayload lets a manual trigger re-run a specific window, e.g.
// after filling in a channel cost that was missing when the night's run went.
type reconcileTaskPayload struct {
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
	Force bool   `json:"force,omitempty"`
}

func (h reconcileHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	payload := reconcileTaskPayload{}
	if err := task.DecodePayload(&payload); err != nil {
		logger.LogWarn(ctx, "reconcile: bad payload: "+err.Error())
	}

	start, end := payload.Start, payload.End
	if start == "" || end == "" {
		// Yesterday, in the operator's timezone, so a snapshot lines up with the
		// calendar day they would ask about.
		yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
		start, end = yesterday, yesterday
	}

	result, err := h.reconcilePeriod(ctx, start, end, payload.Force)
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("reconcile %s..%s failed: %v", start, end, err))
		finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, nil, err)
		return
	}
	finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusSucceeded, result, nil)
}

type reconcileRunResult struct {
	Start          string `json:"start"`
	End            string `json:"end"`
	Snapshots      int    `json:"snapshots"`
	Skipped        int    `json:"skipped"`
	CriticalAlerts int    `json:"critical_alerts"`
	WarningAlerts  int    `json:"warning_alerts"`
}

func (h reconcileHandler) reconcilePeriod(ctx context.Context, start, end string, force bool) (*reconcileRunResult, error) {
	from, to, err := model.ParseReconcileWindow(start, end)
	if err != nil {
		return nil, err
	}

	result := &reconcileRunResult{Start: start, End: end}
	thresholds := service.DefaultAlertThresholds()

	for _, dimension := range reconcileDimensions {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		if !force {
			exists, err := model.ReconcileSnapshotExists(start, end, string(dimension))
			if err != nil {
				return nil, err
			}
			if exists {
				result.Skipped++
				continue
			}
		}

		rows, truncated, err := model.FetchReconcileUsage(model.ReconcileQuery{
			StartTimestamp: from,
			EndTimestamp:   to,
		})
		if err != nil {
			return nil, err
		}
		if truncated {
			// Storing a knowingly incomplete snapshot would be worse than
			// storing none: it would read as authoritative later.
			return nil, fmt.Errorf(
				"usage for %s..%s exceeded the row cap; snapshot not stored because it would be incomplete", start, end)
		}

		report := service.Reconcile(rows, dimension)
		alerts := service.EvaluateReconcileAlerts(report, thresholds)
		critical, warning := service.CountBySeverity(alerts)

		reportJSON, err := common.Marshal(report)
		if err != nil {
			return nil, err
		}
		alertsJSON, err := common.Marshal(alerts)
		if err != nil {
			return nil, err
		}

		snapshot := &model.ReconcileSnapshot{
			GroupBy:        string(dimension),
			PeriodStart:    start,
			PeriodEnd:      end,
			RevenueUSD:     report.Total.RevenueUSD,
			CostUSD:        report.Total.CostUSD,
			MarginUSD:      report.Total.MarginUSD,
			MarginPct:      report.Total.MarginPct,
			CriticalAlerts: critical,
			WarningAlerts:  warning,
			ReportJSON:     string(reportJSON),
			AlertsJSON:     string(alertsJSON),
		}
		if err := model.SaveReconcileSnapshot(snapshot); err != nil {
			return nil, err
		}

		result.Snapshots++
		result.CriticalAlerts += critical
		result.WarningAlerts += warning

		// Log every critical finding individually. A count in a task row is easy
		// to skim past; a named model losing money is not.
		for _, alert := range alerts {
			if alert.Severity != service.AlertCritical {
				continue
			}
			logger.LogWarn(ctx, fmt.Sprintf("RECONCILE %s [%s/%s] %s: %s -- %s",
				alert.Severity, dimension, alert.Subject, alert.Kind, alert.Detail, alert.Action))
		}
	}

	if _, err := model.PruneReconcileSnapshots(); err != nil {
		logger.LogWarn(ctx, "reconcile: snapshot prune failed: "+err.Error())
	}

	logger.LogInfo(ctx, fmt.Sprintf(
		"reconcile %s..%s: %d snapshots, %d skipped, %d critical, %d warning",
		start, end, result.Snapshots, result.Skipped, result.CriticalAlerts, result.WarningAlerts))

	return result, nil
}

// TriggerReconcile enqueues a run, optionally for a specific window. Used by the
// admin "re-run reconciliation" action after a cost or catalog fix.
func TriggerReconcile(start, end string, force bool) error {
	_, _, err := service.EnqueueSystemTask(model.SystemTaskTypeReconcile, reconcileTaskPayload{
		Start: start,
		End:   end,
		Force: force,
	})
	return err
}

// GetReconcileSnapshots serves the stored history. The point of the history is
// "when did this start" -- a model that has been underwater for three weeks is
// indistinguishable from one that broke last night if you only query today.
func GetReconcileSnapshots(c *gin.Context) {
	groupBy := c.Query("group_by")
	if groupBy != "" {
		if _, err := service.ParseGroupBy(groupBy); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
			return
		}
	}

	limit, err := optionalInt(c.Query("limit"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid limit"})
		return
	}

	snapshots, err := model.ListReconcileSnapshots(groupBy, limit)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	// The stored report JSON is large and the list view only needs the headline
	// numbers and the findings, so the report body is dropped here. Fetch a
	// single period through /api/pricing/reconcile when the detail is wanted.
	type snapshotRow struct {
		ID             int     `json:"id"`
		GroupBy        string  `json:"group_by"`
		PeriodStart    string  `json:"period_start"`
		PeriodEnd      string  `json:"period_end"`
		RevenueUSD     float64 `json:"revenue_usd"`
		CostUSD        float64 `json:"cost_usd"`
		MarginUSD      float64 `json:"margin_usd"`
		MarginPct      float64 `json:"margin_pct"`
		CriticalAlerts int     `json:"critical_alerts"`
		WarningAlerts  int     `json:"warning_alerts"`
		Alerts         any     `json:"alerts"`
		CreatedAt      int64   `json:"created_at"`
	}

	rows := make([]snapshotRow, 0, len(snapshots))
	for _, snapshot := range snapshots {
		var alerts []service.ReconcileAlert
		if snapshot.AlertsJSON != "" {
			// A snapshot with unreadable alert JSON is still worth listing for
			// its totals, so a decode failure degrades rather than fails.
			_ = common.Unmarshal([]byte(snapshot.AlertsJSON), &alerts)
		}
		rows = append(rows, snapshotRow{
			ID: snapshot.Id, GroupBy: snapshot.GroupBy,
			PeriodStart: snapshot.PeriodStart, PeriodEnd: snapshot.PeriodEnd,
			RevenueUSD: snapshot.RevenueUSD, CostUSD: snapshot.CostUSD,
			MarginUSD: snapshot.MarginUSD, MarginPct: snapshot.MarginPct,
			CriticalAlerts: snapshot.CriticalAlerts, WarningAlerts: snapshot.WarningAlerts,
			Alerts: alerts, CreatedAt: snapshot.CreatedAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": rows})
}

// RunReconcileNow enqueues a reconciliation run, so an operator does not have to
// wait for the next scheduled pass after fixing a channel cost or catalog gap.
// force=true recomputes a period that already has a snapshot.
func RunReconcileNow(c *gin.Context) {
	start, end := c.Query("start"), c.Query("end")
	if (start == "") != (end == "") {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "start and end must be given together, or both omitted to reconcile yesterday",
		})
		return
	}
	if start != "" {
		if _, _, err := model.ParseReconcileWindow(start, end); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
			return
		}
	}

	if err := TriggerReconcile(start, end, c.Query("force") == "true"); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "对账任务已入队，完成后可在 /api/pricing/reconcile/snapshots 查看",
	})
}
