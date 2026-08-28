package model

// UNIFYAPI-FORK: persisted reconciliation runs.
//
// The on-demand report answers "how did last month go" when someone asks. A
// snapshot answers "when did this start", which is the question that actually
// comes up: a model that has been loss-making for three weeks looks identical to
// one that broke yesterday if you only ever query the current period.
//
// The full report is stored as JSON rather than normalised into columns. It is
// an immutable record of what the numbers were at the time -- normalising it
// would invite joining it against today's catalog, and then a historical
// snapshot would silently change whenever a vendor repriced.

import (
	"time"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// ReconcileSnapshot is one completed reconciliation run.
type ReconcileSnapshot struct {
	Id      int    `json:"id"`
	GroupBy string `json:"group_by" gorm:"type:varchar(32);index:idx_recon_period,priority:2"`

	// PeriodStart and PeriodEnd are the YYYY-MM-DD bounds the run covered,
	// stored as text so a snapshot reads the same as the query that produced it.
	PeriodStart string `json:"period_start" gorm:"type:varchar(16);index:idx_recon_period,priority:1"`
	PeriodEnd   string `json:"period_end" gorm:"type:varchar(16)"`

	RevenueUSD float64 `json:"revenue_usd"`
	CostUSD    float64 `json:"cost_usd"`
	MarginUSD  float64 `json:"margin_usd"`
	MarginPct  float64 `json:"margin_pct"`

	CriticalAlerts int `json:"critical_alerts"`
	WarningAlerts  int `json:"warning_alerts"`

	ReportJSON string `json:"report_json" gorm:"type:text"`
	AlertsJSON string `json:"alerts_json" gorm:"type:text"`

	CreatedAt int64 `json:"created_at" gorm:"bigint;index"`
}

// SaveReconcileSnapshot replaces any existing snapshot for the same period and
// dimension.
//
// Replace rather than append: a period gets recomputed when someone fills in a
// missing channel cost or catalogues a model that was unpriced, and two rows for
// the same month would leave a reader to guess which is current.
func SaveReconcileSnapshot(snapshot *ReconcileSnapshot) error {
	snapshot.CreatedAt = common.GetTimestamp()
	// One transaction, so a failure between the delete and the insert cannot
	// leave the period with no snapshot at all.
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("period_start = ? AND period_end = ? AND group_by = ?",
			snapshot.PeriodStart, snapshot.PeriodEnd, snapshot.GroupBy).
			Delete(&ReconcileSnapshot{}).Error; err != nil {
			return err
		}
		return tx.Create(snapshot).Error
	})
}

// ListReconcileSnapshots returns recent snapshots, newest first.
func ListReconcileSnapshots(groupBy string, limit int) ([]*ReconcileSnapshot, error) {
	if limit <= 0 || limit > 365 {
		limit = 60
	}
	tx := DB.Model(&ReconcileSnapshot{}).Order("period_start desc, id desc").Limit(limit)
	if groupBy != "" {
		tx = tx.Where("group_by = ?", groupBy)
	}
	var snapshots []*ReconcileSnapshot
	err := tx.Find(&snapshots).Error
	return snapshots, err
}

// LatestReconcileSnapshot returns the most recent run for a dimension, or nil.
func LatestReconcileSnapshot(groupBy string) (*ReconcileSnapshot, error) {
	var snapshot ReconcileSnapshot
	err := DB.Where("group_by = ?", groupBy).
		Order("period_start desc, id desc").First(&snapshot).Error
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

// ReconcileSnapshotExists reports whether a period has already been run, so the
// scheduler can skip work instead of recomputing an unchanged month every day.
func ReconcileSnapshotExists(periodStart, periodEnd, groupBy string) (bool, error) {
	var count int64
	err := DB.Model(&ReconcileSnapshot{}).
		Where("period_start = ? AND period_end = ? AND group_by = ?", periodStart, periodEnd, groupBy).
		Count(&count).Error
	return count > 0, err
}

// reconcileSnapshotRetention bounds how much history is kept. Two years is long
// enough for a year-over-year comparison and short enough that the table stays
// small; the underlying logs are pruned on their own schedule anyway.
const reconcileSnapshotRetention = 2 * 365 * 24 * time.Hour

// PruneReconcileSnapshots drops runs older than the retention window.
func PruneReconcileSnapshots() (int64, error) {
	cutoff := time.Now().Add(-reconcileSnapshotRetention).Unix()
	result := DB.Where("created_at < ?", cutoff).Delete(&ReconcileSnapshot{})
	return result.RowsAffected, result.Error
}
