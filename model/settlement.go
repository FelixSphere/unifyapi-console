package model

// UNIFYAPI-FORK: issued settlements -- the record that a period has been billed
// or paid, and for how much.
//
// The reason this is a table and not a re-query:
//
//	revenue is stable, cost is NOT. A customer's amount comes from the consume
//	log, which never changes. An upstream's amount is modelled from today's
//	catalog prices and today's per-channel purchasing ratios. Renegotiate a
//	rate in December and re-running August silently produces a different
//	August. A number you have already acted on -- sent to a customer, paid to a
//	vendor -- must not move underneath you afterwards.
//
// So issuing freezes the whole statement as JSON. What was sent is what is
// stored; the live report stays available beside it, and a difference between
// the two is a finding rather than a bug.

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Settlement statuses. The progression is one-directional in practice but not
// enforced as a state machine: real settlements get corrected, and a workflow
// that refuses to go backwards just gets worked around with a second row.
const (
	SettlementStatusIssued     = "issued"
	SettlementStatusSettled    = "settled"
	SettlementStatusVoid       = "void"
	SettlementStatusSuperseded = "superseded"
)

// Settlement is one issued statement for one counterparty and period.
type Settlement struct {
	Id int `json:"id"`

	// Kind is "customer" (money owed to us) or "vendor" (money we owe).
	Kind string `json:"kind" gorm:"type:varchar(16);index:idx_settlement_period,priority:1;uniqueIndex:uidx_settlement_revision,priority:1"`

	// Counterparty is the stable id: a namespaced Pricing Group key for a
	// customer, a vendor id for an upstream. Label is the display name AT ISSUE TIME, stored rather than
	// joined so a renamed customer does not retroactively rename their invoices.
	Counterparty string `json:"counterparty" gorm:"type:varchar(64);index:idx_settlement_period,priority:2;uniqueIndex:uidx_settlement_revision,priority:2"`
	Label        string `json:"label" gorm:"type:varchar(191)"`

	PeriodStart string `json:"period_start" gorm:"type:varchar(16);index:idx_settlement_period,priority:3;uniqueIndex:uidx_settlement_revision,priority:3"`
	PeriodEnd   string `json:"period_end" gorm:"type:varchar(16);index:idx_settlement_period,priority:4;uniqueIndex:uidx_settlement_revision,priority:4"`

	// Revision lets a corrected invoice receive a new immutable number while
	// every earlier document remains available in the audit trail.
	Revision int `json:"revision" gorm:"not null;default:1;uniqueIndex:uidx_settlement_revision,priority:5"`

	// Replacement links are accounting provenance, not mutable UI notes. A new
	// invoice lists every document it replaces; each old document points back.
	SupersedesIDs                  []int  `json:"supersedes_ids,omitempty" gorm:"serializer:json;type:text"`
	SupersededByID                 int    `json:"superseded_by_id,omitempty" gorm:"index"`
	ReplacementReason              string `json:"replacement_reason,omitempty" gorm:"type:text"`
	ReplacementComplianceConfirmed bool   `json:"replacement_compliance_confirmed,omitempty"`

	// AmountUSD is our figure: billed to the customer, or modelled for the
	// vendor.
	AmountUSD float64 `json:"amount_usd"`

	// InvoicedUSD is the counterparty's figure, typed in from their invoice.
	// Vendor side only, and the entire point of the vendor half: the variance
	// between it and AmountUSD is what reconciliation produces.
	InvoicedUSD float64 `json:"invoiced_usd"`

	// InvoiceRecorded distinguishes "their invoice says zero" from "no invoice
	// entered yet". Without it a blank field and a genuine zero are the same
	// row, and a missing invoice reads as a 100% variance finding.
	InvoiceRecorded bool `json:"invoice_recorded"`

	Status string `json:"status" gorm:"type:varchar(16);default:'issued'"`
	Note   string `json:"note" gorm:"type:text"`

	// StatementJSON is the frozen service.Statement. See the file header.
	StatementJSON string `json:"statement_json" gorm:"type:text"`

	// PricingSnapshotDate records which catalog vintage priced this. A vendor
	// settlement issued against a stale catalog is explainable; one whose
	// provenance is unknown is not.
	PricingSnapshotDate string `json:"pricing_snapshot_date" gorm:"type:varchar(16)"`

	CreatedAt int64 `json:"created_at" gorm:"bigint;index"`
	UpdatedAt int64 `json:"updated_at" gorm:"bigint"`
}

// VarianceUSD is the counterparty's figure minus ours. Positive means they are
// asking for more than we modelled.
func (s *Settlement) VarianceUSD() float64 {
	if !s.InvoiceRecorded {
		return 0
	}
	return s.InvoicedUSD - s.AmountUSD
}

var (
	ErrSettlementAlreadyIssued = errors.New("settlement already issued")
	ErrSettlementImmutable     = errors.New("issued settlements are immutable; void the record instead")
)

// EnsureSettlementRevisionIndex retires the original one-document-per-period
// constraint after AutoMigrate has created its revision-aware replacement.
// Keeping both would make revision 2 fail despite the new schema.
func EnsureSettlementRevisionIndex() error {
	if DB.Migrator().HasIndex(&Settlement{}, "uidx_settlement_period") {
		return DB.Migrator().DropIndex(&Settlement{}, "uidx_settlement_period")
	}
	return nil
}

func validateSettlementKey(settlement *Settlement) error {
	if settlement.Kind == "" || settlement.Counterparty == "" {
		return errors.New("settlement requires a kind and a counterparty")
	}
	if settlement.PeriodStart == "" || settlement.PeriodEnd == "" {
		return errors.New("settlement requires a period")
	}
	return nil
}

// CreateSettlement freezes a statement exactly once. Corrections belong in a
// void workflow record, never in a rewrite of what was already sent or paid.
func CreateSettlement(settlement *Settlement) (*Settlement, error) {
	if err := validateSettlementKey(settlement); err != nil {
		return nil, err
	}
	if settlement.Status == "" {
		settlement.Status = SettlementStatusIssued
	}
	if settlement.Revision == 0 {
		settlement.Revision = 1
	}

	now := common.GetTimestamp()
	err := DB.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&Settlement{}).
			Where("kind = ? AND counterparty = ? AND period_start = ? AND period_end = ? AND revision = ?",
				settlement.Kind, settlement.Counterparty, settlement.PeriodStart, settlement.PeriodEnd, settlement.Revision).
			Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return ErrSettlementAlreadyIssued
		}
		settlement.CreatedAt = now
		settlement.UpdatedAt = now
		return tx.Create(settlement).Error
	})
	if err != nil {
		return nil, err
	}
	return settlement, nil
}

// CreateNextSettlementRevision issues the next available immutable document
// for an accounting key. The controller uses this after an earlier revision
// was voided; active revisions are rejected before this function is called.
func CreateNextSettlementRevision(settlement *Settlement) (*Settlement, error) {
	if err := validateSettlementKey(settlement); err != nil {
		return nil, err
	}
	if settlement.Status == "" {
		settlement.Status = SettlementStatusIssued
	}

	now := common.GetTimestamp()
	err := DB.Transaction(func(tx *gorm.DB) error {
		var existing []*Settlement
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("kind = ? AND counterparty = ? AND period_start = ? AND period_end = ?",
				settlement.Kind, settlement.Counterparty, settlement.PeriodStart, settlement.PeriodEnd).
			Find(&existing).Error; err != nil {
			return err
		}
		maxRevision := 0
		for _, prior := range existing {
			if prior.Revision > maxRevision {
				maxRevision = prior.Revision
			}
		}
		settlement.Revision = maxRevision + 1
		settlement.CreatedAt = now
		settlement.UpdatedAt = now
		return tx.Create(settlement).Error
	})
	if err != nil {
		return nil, err
	}
	return settlement, nil
}

func SettlementIsActive(settlement *Settlement) bool {
	return settlement != nil && (settlement.Status == SettlementStatusIssued || settlement.Status == SettlementStatusSettled)
}

var ErrSettlementReplacementConflict = errors.New("invoice replacement conflict")

// ReplaceCustomerSettlements atomically issues one new immutable invoice and
// marks every active predecessor as superseded. It never deletes or rewrites a
// frozen statement, and it cannot leave the period with no active invoice.
func ReplaceCustomerSettlements(settlement *Settlement, supersededIDs []int) (*Settlement, error) {
	if err := validateSettlementKey(settlement); err != nil {
		return nil, err
	}
	if settlement.Kind != "customer" {
		return nil, fmt.Errorf("%w: only customer invoices can be replaced", ErrSettlementReplacementConflict)
	}
	settlement.ReplacementReason = strings.TrimSpace(settlement.ReplacementReason)
	if settlement.ReplacementReason == "" || !settlement.ReplacementComplianceConfirmed {
		return nil, fmt.Errorf("%w: replacement reason and compliance confirmation are required", ErrSettlementReplacementConflict)
	}
	unique := map[int]bool{}
	ids := make([]int, 0, len(supersededIDs))
	for _, id := range supersededIDs {
		if id > 0 && !unique[id] {
			unique[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("%w: no active invoice was selected", ErrSettlementReplacementConflict)
	}
	sort.Ints(ids)

	now := common.GetTimestamp()
	err := DB.Transaction(func(tx *gorm.DB) error {
		var previous []*Settlement
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", ids).Find(&previous).Error; err != nil {
			return err
		}
		if len(previous) != len(ids) {
			return fmt.Errorf("%w: one or more previous invoices no longer exist", ErrSettlementReplacementConflict)
		}
		for _, old := range previous {
			if old.Kind != settlement.Kind || old.PeriodStart != settlement.PeriodStart || old.PeriodEnd != settlement.PeriodEnd || !SettlementIsActive(old) {
				return fmt.Errorf("%w: invoice #%d is no longer an active invoice for this period", ErrSettlementReplacementConflict, old.Id)
			}
		}

		var maxRevision int
		if err := tx.Model(&Settlement{}).
			Where("kind = ? AND counterparty = ? AND period_start = ? AND period_end = ?", settlement.Kind, settlement.Counterparty, settlement.PeriodStart, settlement.PeriodEnd).
			Select("COALESCE(MAX(revision), 0)").Scan(&maxRevision).Error; err != nil {
			return err
		}
		settlement.Revision = maxRevision + 1
		settlement.Status = SettlementStatusIssued
		settlement.SupersedesIDs = ids
		settlement.CreatedAt = now
		settlement.UpdatedAt = now
		if err := tx.Create(settlement).Error; err != nil {
			return err
		}
		return tx.Model(&Settlement{}).Where("id IN ?", ids).Updates(map[string]any{
			"status":           SettlementStatusSuperseded,
			"superseded_by_id": settlement.Id,
			"updated_at":       now,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return settlement, nil
}

// UpdateSettlementCounterparty changes only workflow metadata and the amount
// on the counterparty's own invoice. Frozen amount and line items cannot enter
// this update path.
func UpdateSettlementCounterparty(settlement *Settlement) (*Settlement, error) {
	if settlement.Id == 0 {
		return nil, errors.New("settlement id is required")
	}
	current, err := GetSettlement(settlement.Id)
	if err != nil {
		return nil, err
	}
	if current.Status == SettlementStatusSuperseded {
		return nil, ErrSettlementImmutable
	}
	settlement.UpdatedAt = common.GetTimestamp()
	result := DB.Model(&Settlement{}).Where("id = ? AND status <> ?", settlement.Id, SettlementStatusSuperseded).
		Select("invoiced_usd", "invoice_recorded", "status", "note", "updated_at").
		Updates(settlement)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, fmt.Errorf("settlement %d not found", settlement.Id)
	}
	return GetSettlement(settlement.Id)
}

// GetSettlementByPeriod returns the frozen record for one accounting key.
func GetSettlementByPeriod(kind, counterparty, periodStart, periodEnd string) (*Settlement, error) {
	var settlement Settlement
	if err := DB.Where("kind = ? AND counterparty = ? AND period_start = ? AND period_end = ?",
		kind, counterparty, periodStart, periodEnd).Order("revision desc, id desc").First(&settlement).Error; err != nil {
		return nil, err
	}
	return &settlement, nil
}

// ListSettlements returns issued settlements for a period, or for a kind across
// all periods when the period is left blank.
func ListSettlements(kind, periodStart, periodEnd string, limit int) ([]*Settlement, error) {
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	tx := DB.Model(&Settlement{}).Order("period_start desc, revision desc, amount_usd desc, id desc").Limit(limit)
	if kind != "" {
		tx = tx.Where("kind = ?", kind)
	}
	if periodStart != "" && periodEnd != "" {
		tx = tx.Where("period_start = ? AND period_end = ?", periodStart, periodEnd)
	}
	var settlements []*Settlement
	err := tx.Find(&settlements).Error
	return settlements, err
}

// GetSettlement fetches one settlement by id.
func GetSettlement(id int) (*Settlement, error) {
	var settlement Settlement
	if err := DB.Where("id = ?", id).First(&settlement).Error; err != nil {
		return nil, err
	}
	return &settlement, nil
}

// DeleteSettlement deliberately refuses to erase issued accounting history.
// The endpoint remains for compatibility and tells callers to void instead.
func DeleteSettlement(id int) error {
	if _, err := GetSettlement(id); err != nil {
		return err
	}
	return ErrSettlementImmutable
}
