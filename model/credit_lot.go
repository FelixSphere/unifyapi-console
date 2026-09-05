/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

// UNIFYAPI-FORK: one tranche of supplier credits. See credit_supplier.go for
// the model and docs/credit-supply.md for the lifecycle diagram.

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"gorm.io/gorm"
)

const (
	CreditLotStatusPending   = "pending"
	CreditLotStatusActive    = "active"
	CreditLotStatusSuspended = "suspended"
	CreditLotStatusExhausted = "exhausted"
	CreditLotStatusExpired   = "expired"
	CreditLotStatusRejected  = "rejected"

	CreditLotSourceAdmin    = "admin"
	CreditLotSourceSupplier = "supplier"

	// CreditLotAttestationVersion names the wording of the right-to-transfer
	// attestation a lot was recorded under. Bump it when the wording changes so
	// old lots keep pointing at what was actually agreed.
	CreditLotAttestationVersion = "2026-09-credit-supply-v1"
)

var (
	ErrCreditLotChannelBound = errors.New("that channel already backs another live credit lot")
	ErrCreditLotNeedsChannel = errors.New("bind the lot to a channel before activating it")
	ErrCreditLotTransition   = errors.New("that status change is not allowed from the lot's current status")
	// ErrCreditLotApprovalNeedsConfirmation is enforced here, not only in the
	// screen: approval is the one moment the right-to-transfer question can be
	// asked, so the server refuses to activate a submission without the answer.
	ErrCreditLotApprovalNeedsConfirmation = errors.New("approval requires confirming the supplier's right to transfer these credits")
	ErrCreditLotReasonRequired            = errors.New("a reason is required when rejecting or suspending a lot")
	ErrCreditLotSecretInText              = errors.New("that text looks like it contains an API key; keys belong on the channel, never in notes")
	creditLotVendorPattern                = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,63}$`)
)

// textLooksLikeProviderSecret catches the common shapes of vendor API keys so a key
// pasted into a note or payout-terms field is refused rather than stored in
// plain text next to the operator's memory. Absorbed from the earlier
// credit-contribution draft.
func textLooksLikeProviderSecret(values ...string) bool {
	for _, value := range values {
		lower := strings.ToLower(value)
		for _, marker := range []string{"sk-ant-", "sk-proj-", "sk-live-", "sk-or-", "bearer ey", "api_key=", "aiza"} {
			if strings.Contains(lower, marker) {
				return true
			}
		}
	}
	return false
}

// CreditLot is one supplier's tranche of one vendor's credits, routed through
// one channel.
type CreditLot struct {
	Id         int `json:"id" gorm:"primaryKey"`
	SupplierId int `json:"supplier_id" gorm:"not null;index"`
	// Vendor is a lowercase slug ("openai", "anthropic") for grouping and
	// display. Attribution for settlement goes by supplier, not by vendor.
	Vendor string `json:"vendor" gorm:"type:varchar(64);not null"`
	// ChannelId is the channel carrying this lot's upstream key. 0 until bound.
	ChannelId int `json:"channel_id" gorm:"not null;default:0;index"`

	// FaceValueUSD is the lot's worth at the vendor's official list price.
	FaceValueUSD float64 `json:"face_value_usd" gorm:"column:face_value_usd;not null;default:0"`
	// AcquisitionRate is what we pay per $1 of face value consumed, in (0, 1].
	// It becomes the bound channel's ChannelCostRatio on activation.
	AcquisitionRate float64 `json:"acquisition_rate" gorm:"not null;default:1"`
	// ConsumedUSD is face value drawn down so far, at list price.
	ConsumedUSD float64 `json:"consumed_usd" gorm:"column:consumed_usd;not null;default:0"`
	// UnpricedRequests counts requests that drew nothing because the model has
	// no catalogue price. Non-zero means ConsumedUSD is an understatement.
	UnpricedRequests int64 `json:"unpriced_requests" gorm:"not null;default:0"`
	// LowWaterUSD is the remaining balance at or below which one notification
	// fires. 0 disables it.
	LowWaterUSD        float64 `json:"low_water_usd" gorm:"column:low_water_usd;not null;default:0"`
	LowWaterNotifiedAt int64   `json:"low_water_notified_at" gorm:"not null;default:0"`

	ExpiresAt int64  `json:"expires_at" gorm:"not null;default:0"`
	Status    string `json:"status" gorm:"type:varchar(16);not null;default:'pending';index"`
	Source    string `json:"source" gorm:"type:varchar(16);not null;default:'admin'"`
	Note      string `json:"note" gorm:"type:text"`
	// StatusReason is the human explanation of the last operator decision --
	// why a lot was rejected or suspended. Shown to the supplier.
	StatusReason string `json:"status_reason" gorm:"type:varchar(500)"`
	// Attestation records who asserted the right to transfer these credits and
	// under which wording: the supplier at submission, or the operator when
	// entering a lot on the supplier's behalf.
	AttestationVersion string `json:"attestation_version" gorm:"type:varchar(64)"`
	AttestedAt         int64  `json:"attested_at" gorm:"not null;default:0"`
	AttestedBy         string `json:"attested_by" gorm:"type:varchar(64)"`
	// ApprovedBy / ApprovedAt record the operator's own confirmation at
	// approval time. Empty on admin-created lots born active, whose creation
	// was the approval.
	ApprovedBy string `json:"approved_by" gorm:"type:varchar(64)"`
	ApprovedAt int64  `json:"approved_at" gorm:"not null;default:0"`
	// RetiredAt is when the lot became exhausted or expired.
	RetiredAt int64 `json:"retired_at" gorm:"not null;default:0"`
	CreatedAt int64 `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt int64 `json:"updated_at" gorm:"autoUpdateTime"`
}

// CreditLotEvent is one line of a lot's history: who moved it, from what to
// what, and why. Compliance evidence lives here, so it is append-only.
type CreditLotEvent struct {
	Id         int    `json:"id" gorm:"primaryKey"`
	LotId      int    `json:"lot_id" gorm:"not null;index"`
	Actor      string `json:"actor" gorm:"type:varchar(64)"`
	EventType  string `json:"event_type" gorm:"type:varchar(40);not null"`
	FromStatus string `json:"from_status" gorm:"type:varchar(16)"`
	ToStatus   string `json:"to_status" gorm:"type:varchar(16)"`
	Message    string `json:"message" gorm:"type:varchar(500)"`
	CreatedAt  int64  `json:"created_at" gorm:"autoCreateTime"`
}

func appendCreditLotEvent(tx *gorm.DB, lotId int, actor, eventType, from, to, message string) error {
	return tx.Create(&CreditLotEvent{
		LotId: lotId, Actor: actor, EventType: eventType,
		FromStatus: from, ToStatus: to, Message: strings.TrimSpace(message),
	}).Error
}

// GetCreditLotEvents returns a lot's history, newest first.
func GetCreditLotEvents(lotId int, limit int) ([]CreditLotEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var events []CreditLotEvent
	err := DB.Where("lot_id = ?", lotId).Order("id desc").Limit(limit).Find(&events).Error
	return events, err
}

// CreditLotUsage is one lot's draw-down for one calendar day. It exists so the
// pool and portal screens can chart consumption without scanning the consume
// log, and so a lot's balance can be audited against something other than
// itself.
type CreditLotUsage struct {
	Id       int     `json:"id" gorm:"primaryKey"`
	LotId    int     `json:"lot_id" gorm:"not null;uniqueIndex:uidx_credit_lot_usage_day,priority:1"`
	Day      string  `json:"day" gorm:"type:varchar(10);not null;uniqueIndex:uidx_credit_lot_usage_day,priority:2"`
	Requests int64   `json:"requests" gorm:"not null;default:0"`
	FaceUSD  float64 `json:"face_usd" gorm:"column:face_usd;not null;default:0"`
}

func (lot *CreditLot) RemainingUSD() float64 {
	remaining := lot.FaceValueUSD - lot.ConsumedUSD
	if remaining < 0 {
		return 0
	}
	return remaining
}

// PayableUSD is what we owe the supplier for this lot to date.
func (lot *CreditLot) PayableUSD() float64 {
	return lot.ConsumedUSD * lot.AcquisitionRate
}

// Live reports whether the lot occupies its channel binding.
func (lot *CreditLot) Live() bool {
	switch lot.Status {
	case CreditLotStatusPending, CreditLotStatusActive, CreditLotStatusSuspended:
		return true
	}
	return false
}

func ValidateCreditLot(lot *CreditLot, now int64) error {
	lot.Vendor = strings.ToLower(strings.TrimSpace(lot.Vendor))
	lot.Note = strings.TrimSpace(lot.Note)
	if lot.Status == "" {
		lot.Status = CreditLotStatusPending
	}
	if lot.Source == "" {
		lot.Source = CreditLotSourceAdmin
	}
	if lot.SupplierId <= 0 {
		return errors.New("a lot belongs to a supplier")
	}
	if !creditLotVendorPattern.MatchString(lot.Vendor) {
		return errors.New("vendor must be a lowercase slug such as openai or anthropic")
	}
	if lot.ChannelId < 0 {
		return errors.New("channel id cannot be negative")
	}
	if lot.FaceValueUSD <= 0 {
		return errors.New("face value must be greater than 0")
	}
	if lot.AcquisitionRate <= 0 || lot.AcquisitionRate > 1 {
		return errors.New("acquisition rate must be in (0, 1]: we pay at most face value for credits")
	}
	if lot.LowWaterUSD < 0 || lot.LowWaterUSD > lot.FaceValueUSD {
		return errors.New("low-water mark must be between 0 and the face value")
	}
	if lot.ExpiresAt < 0 || (lot.ExpiresAt != 0 && lot.ExpiresAt <= now) {
		return errors.New("expiry must be in the future, or 0 for none")
	}
	switch lot.Status {
	case CreditLotStatusPending, CreditLotStatusActive:
	default:
		return errors.New("a lot is created pending or active; other statuses are reached by transition")
	}
	if lot.Source != CreditLotSourceAdmin && lot.Source != CreditLotSourceSupplier {
		return errors.New("source must be admin or supplier")
	}
	if lot.Status == CreditLotStatusActive && lot.ChannelId == 0 {
		return ErrCreditLotNeedsChannel
	}
	if textLooksLikeProviderSecret(lot.Note) {
		return ErrCreditLotSecretInText
	}
	return nil
}

// ensureChannelFree refuses a binding while another live lot holds the channel,
// and refuses a channel that does not exist.
func ensureChannelFree(tx *gorm.DB, channelId int, exceptLotId int) error {
	if channelId == 0 {
		return nil
	}
	var channelCount int64
	if err := tx.Model(&Channel{}).Where("id = ?", channelId).Count(&channelCount).Error; err != nil {
		return err
	}
	if channelCount == 0 {
		return fmt.Errorf("channel #%d does not exist", channelId)
	}
	var lotCount int64
	err := tx.Model(&CreditLot{}).
		Where("channel_id = ? AND id <> ? AND status IN ?", channelId, exceptLotId,
			[]string{CreditLotStatusPending, CreditLotStatusActive, CreditLotStatusSuspended}).
		Count(&lotCount).Error
	if err != nil {
		return err
	}
	if lotCount > 0 {
		return ErrCreditLotChannelBound
	}
	return nil
}

// applyLotCostRatio writes the lot's acquisition rate into the bound channel's
// purchasing ratio. This is the one place the pool touches pricing.
func applyLotCostRatio(lot *CreditLot, actor string) error {
	if lot.ChannelId == 0 {
		return nil
	}
	ratios := ratio_setting.GetChannelCostRatioCopy()
	if ratios == nil {
		ratios = map[string]float64{}
	}
	ratios[strconv.Itoa(lot.ChannelId)] = lot.AcquisitionRate
	encoded, err := common.Marshal(ratios)
	if err != nil {
		return err
	}
	return UpdateOptionAs("ChannelCostRatio", string(encoded), actor)
}

func CreateCreditLot(lot *CreditLot, actor string) error {
	now := common.GetTimestamp()
	if err := ValidateCreditLot(lot, now); err != nil {
		return err
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		var supplierCount int64
		if err := tx.Model(&CreditSupplier{}).Where("id = ?", lot.SupplierId).Count(&supplierCount).Error; err != nil {
			return err
		}
		if supplierCount == 0 {
			return fmt.Errorf("supplier #%d does not exist", lot.SupplierId)
		}
		if err := ensureChannelFree(tx, lot.ChannelId, 0); err != nil {
			return err
		}
		if lot.AttestedAt == 0 {
			// An operator entering a lot is recording the supplier's assurance
			// on their behalf; the record says so.
			lot.AttestationVersion = CreditLotAttestationVersion
			lot.AttestedAt = now
			lot.AttestedBy = actor
		}
		if lot.Status == CreditLotStatusActive {
			lot.ApprovedBy = actor
			lot.ApprovedAt = now
		}
		if err := tx.Create(lot).Error; err != nil {
			return err
		}
		message := fmt.Sprintf("%s lot: $%.2f face at %s, %.0f%% acquisition rate", lot.Source, lot.FaceValueUSD, lot.Vendor, lot.AcquisitionRate*100)
		return appendCreditLotEvent(tx, lot.Id, actor, "created", "", lot.Status, message)
	})
	if err != nil {
		return err
	}
	invalidateChannelLot(lot.ChannelId)
	invalidateChannelSupplierIndex()
	if lot.Status == CreditLotStatusActive {
		return applyLotCostRatio(lot, actor)
	}
	return nil
}

// UpdateCreditLot replaces the commercial fields. Status is not editable here;
// use TransitionCreditLot, so every status change goes through the same guard.
func UpdateCreditLot(id int, patch *CreditLot, actor string) error {
	now := common.GetTimestamp()
	var updated CreditLot
	var previousChannel int
	err := DB.Transaction(func(tx *gorm.DB) error {
		var existing CreditLot
		if err := tx.First(&existing, "id = ?", id).Error; err != nil {
			return err
		}
		previousChannel = existing.ChannelId
		candidate := existing
		candidate.Vendor = patch.Vendor
		candidate.ChannelId = patch.ChannelId
		candidate.FaceValueUSD = patch.FaceValueUSD
		candidate.AcquisitionRate = patch.AcquisitionRate
		candidate.LowWaterUSD = patch.LowWaterUSD
		candidate.ExpiresAt = patch.ExpiresAt
		candidate.Note = patch.Note
		// Validation is written for creation, where only pending/active exist.
		// Run it against a pending shape so a retired lot can still be edited
		// (raising its face value is how it gets reactivated).
		shape := candidate
		shape.Status = CreditLotStatusPending
		if err := ValidateCreditLot(&shape, now); err != nil {
			return err
		}
		candidate.Vendor = shape.Vendor
		candidate.Note = shape.Note
		if candidate.Live() && candidate.ChannelId != existing.ChannelId {
			if err := ensureChannelFree(tx, candidate.ChannelId, id); err != nil {
				return err
			}
		}
		if candidate.Status == CreditLotStatusActive && candidate.ChannelId == 0 {
			return ErrCreditLotNeedsChannel
		}
		if candidate.FaceValueUSD > existing.FaceValueUSD && candidate.RemainingUSD() > candidate.LowWaterUSD {
			// Topping a lot up re-arms the low-water alert.
			candidate.LowWaterNotifiedAt = 0
		}
		if err := tx.Save(&candidate).Error; err != nil {
			return err
		}
		updated = candidate
		message := fmt.Sprintf("face $%.2f -> $%.2f, rate %.0f%% -> %.0f%%, channel #%d -> #%d, expiry %d -> %d",
			existing.FaceValueUSD, candidate.FaceValueUSD, existing.AcquisitionRate*100, candidate.AcquisitionRate*100,
			existing.ChannelId, candidate.ChannelId, existing.ExpiresAt, candidate.ExpiresAt)
		return appendCreditLotEvent(tx, id, actor, "edited", existing.Status, existing.Status, message)
	})
	if err != nil {
		return err
	}
	invalidateChannelLot(previousChannel)
	invalidateChannelLot(updated.ChannelId)
	invalidateChannelSupplierIndex()
	if updated.Status == CreditLotStatusActive {
		return applyLotCostRatio(&updated, actor)
	}
	return nil
}

// CreditLotTransition is an operator's decision about a lot.
type CreditLotTransition struct {
	To    string
	Actor string
	// Reason is required when rejecting or suspending; it is what the supplier
	// reads.
	Reason string
	// TransferRightsConfirmed must be true to approve a pending lot.
	TransferRightsConfirmed bool
}

// TransitionCreditLot moves a lot between operator-driven statuses. The
// automatic ones (exhausted, expired) are reached only from the consume path.
//
//	pending   -> active | rejected
//	active    -> suspended
//	suspended -> active
//	exhausted -> active   (after the face value was raised)
//	expired   -> active   (after the expiry was moved or cleared)
func TransitionCreditLot(id int, req CreditLotTransition) (*CreditLot, error) {
	to := req.To
	actor := req.Actor
	reason := strings.TrimSpace(req.Reason)
	if (to == CreditLotStatusRejected || to == CreditLotStatusSuspended) && reason == "" {
		return nil, ErrCreditLotReasonRequired
	}
	if textLooksLikeProviderSecret(reason) {
		return nil, ErrCreditLotSecretInText
	}
	now := common.GetTimestamp()
	var lot CreditLot
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&lot, "id = ?", id).Error; err != nil {
			return err
		}
		from := lot.Status
		allowed := false
		switch lot.Status {
		case CreditLotStatusPending:
			allowed = to == CreditLotStatusActive || to == CreditLotStatusRejected
		case CreditLotStatusActive:
			allowed = to == CreditLotStatusSuspended
		case CreditLotStatusSuspended:
			allowed = to == CreditLotStatusActive
		case CreditLotStatusExhausted:
			allowed = to == CreditLotStatusActive && lot.RemainingUSD() > 0
		case CreditLotStatusExpired:
			allowed = to == CreditLotStatusActive && (lot.ExpiresAt == 0 || lot.ExpiresAt > now)
		}
		if !allowed {
			return fmt.Errorf("%w: %s -> %s", ErrCreditLotTransition, lot.Status, to)
		}
		if to == CreditLotStatusActive {
			if lot.ChannelId == 0 {
				return ErrCreditLotNeedsChannel
			}
			if from == CreditLotStatusPending && !req.TransferRightsConfirmed {
				return ErrCreditLotApprovalNeedsConfirmation
			}
			if err := ensureChannelFree(tx, lot.ChannelId, lot.Id); err != nil {
				return err
			}
			if lot.ExpiresAt != 0 && lot.ExpiresAt <= now {
				return errors.New("the lot has expired; move or clear the expiry first")
			}
			lot.RetiredAt = 0
			lot.StatusReason = ""
			if from == CreditLotStatusPending {
				lot.ApprovedBy = actor
				lot.ApprovedAt = now
			}
		} else {
			lot.StatusReason = reason
		}
		lot.Status = to
		if err := tx.Save(&lot).Error; err != nil {
			return err
		}
		return appendCreditLotEvent(tx, lot.Id, actor, "transition", from, to, reason)
	})
	if err != nil {
		return nil, err
	}
	invalidateChannelLot(lot.ChannelId)
	invalidateChannelSupplierIndex()

	// The channel follows the lot: a lot that is not active must not serve, and
	// an activated lot's channel must. Both go through UpdateChannelStatus so
	// the ability table and channel cache stay coherent.
	channelReason := fmt.Sprintf("credit lot #%d %s: %s", lot.Id, lot.Status, reason)
	switch to {
	case CreditLotStatusActive:
		UpdateChannelStatus(lot.ChannelId, "", common.ChannelStatusEnabled, "")
		if err := applyLotCostRatio(&lot, actor); err != nil {
			return &lot, err
		}
	case CreditLotStatusSuspended, CreditLotStatusRejected:
		if lot.ChannelId != 0 {
			UpdateChannelStatus(lot.ChannelId, "", common.ChannelStatusManuallyDisabled, channelReason)
		}
	}
	return &lot, nil
}

type CreditLotFilter struct {
	SupplierId int
	Status     string
}

func GetCreditLots(filter CreditLotFilter) ([]*CreditLot, error) {
	query := DB.Model(&CreditLot{})
	if filter.SupplierId > 0 {
		query = query.Where("supplier_id = ?", filter.SupplierId)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	var lots []*CreditLot
	err := query.Order("id desc").Find(&lots).Error
	return lots, err
}

func GetCreditLotById(id int) (*CreditLot, error) {
	var lot CreditLot
	if err := DB.First(&lot, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &lot, nil
}

// GetCreditLotUsage returns the last `days` calendar days of draw-down for one
// lot, oldest first. Days with no traffic are absent, not zero-filled; the
// screen fills them.
func GetCreditLotUsage(lotId int, days int) ([]CreditLotUsage, error) {
	if days <= 0 {
		days = 30
	}
	since := common.GetTimestamp() - int64(days)*86400
	sinceDay := creditSupplyDay(since)
	var rows []CreditLotUsage
	err := DB.Where("lot_id = ? AND day >= ?", lotId, sinceDay).Order("day asc").Find(&rows).Error
	return rows, err
}

// CreditSupplyVendorTotals is one vendor's slice of the pool.
type CreditSupplyVendorTotals struct {
	Vendor       string  `json:"vendor"`
	Lots         int     `json:"lots"`
	FaceUSD      float64 `json:"face_usd"`
	ConsumedUSD  float64 `json:"consumed_usd"`
	RemainingUSD float64 `json:"remaining_usd"`
	PayableUSD   float64 `json:"payable_usd"`
}

// CreditSupplyOverview is the headline the admin screen opens on.
type CreditSupplyOverview struct {
	Suppliers    int                        `json:"suppliers"`
	LotsByStatus map[string]int             `json:"lots_by_status"`
	FaceUSD      float64                    `json:"face_usd"`
	ConsumedUSD  float64                    `json:"consumed_usd"`
	RemainingUSD float64                    `json:"remaining_usd"`
	PayableUSD   float64                    `json:"payable_usd"`
	UnpricedLots int                        `json:"unpriced_lots"`
	ByVendor     []CreditSupplyVendorTotals `json:"by_vendor"`
	// Attention lists live lots that need a human: pending approval, at or
	// below low water, or expiring within seven days.
	Attention []*CreditLot `json:"attention"`
}

func GetCreditSupplyOverview() (*CreditSupplyOverview, error) {
	var supplierCount int64
	if err := DB.Model(&CreditSupplier{}).Count(&supplierCount).Error; err != nil {
		return nil, err
	}
	lots, err := GetCreditLots(CreditLotFilter{})
	if err != nil {
		return nil, err
	}
	now := common.GetTimestamp()
	overview := &CreditSupplyOverview{
		Suppliers:    int(supplierCount),
		LotsByStatus: map[string]int{},
		Attention:    []*CreditLot{},
	}
	byVendor := map[string]*CreditSupplyVendorTotals{}
	for _, lot := range lots {
		overview.LotsByStatus[lot.Status]++
		if lot.Status == CreditLotStatusRejected {
			continue
		}
		overview.FaceUSD += lot.FaceValueUSD
		overview.ConsumedUSD += lot.ConsumedUSD
		overview.RemainingUSD += lot.RemainingUSD()
		overview.PayableUSD += lot.PayableUSD()
		if lot.UnpricedRequests > 0 {
			overview.UnpricedLots++
		}
		totals := byVendor[lot.Vendor]
		if totals == nil {
			totals = &CreditSupplyVendorTotals{Vendor: lot.Vendor}
			byVendor[lot.Vendor] = totals
		}
		totals.Lots++
		totals.FaceUSD += lot.FaceValueUSD
		totals.ConsumedUSD += lot.ConsumedUSD
		totals.RemainingUSD += lot.RemainingUSD()
		totals.PayableUSD += lot.PayableUSD()

		needsAttention := lot.Status == CreditLotStatusPending ||
			(lot.Status == CreditLotStatusActive && lot.LowWaterUSD > 0 && lot.RemainingUSD() <= lot.LowWaterUSD) ||
			(lot.Live() && lot.ExpiresAt != 0 && lot.ExpiresAt-now <= 7*86400)
		if needsAttention {
			overview.Attention = append(overview.Attention, lot)
		}
	}
	overview.ByVendor = make([]CreditSupplyVendorTotals, 0, len(byVendor))
	for _, totals := range byVendor {
		overview.ByVendor = append(overview.ByVendor, *totals)
	}
	sortVendorTotals(overview.ByVendor)
	return overview, nil
}

func sortVendorTotals(totals []CreditSupplyVendorTotals) {
	for i := 1; i < len(totals); i++ {
		for j := i; j > 0; j-- {
			left, right := totals[j-1], totals[j]
			if left.FaceUSD > right.FaceUSD || (left.FaceUSD == right.FaceUSD && left.Vendor <= right.Vendor) {
				break
			}
			totals[j-1], totals[j] = right, left
		}
	}
}
