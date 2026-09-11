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
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"gorm.io/gorm"
)

const (
	CreditLotStatusPending = "pending"
	// Verified: the key answered a real request; the sale is awaiting payment.
	// Only PayCreditLot moves a lot out of here, so nothing is consumed before
	// the supplier has been paid.
	CreditLotStatusVerified  = "verified"
	CreditLotStatusActive    = "active"
	CreditLotStatusSuspended = "suspended"
	CreditLotStatusExhausted = "exhausted"
	CreditLotStatusExpired   = "expired"
	CreditLotStatusRejected  = "rejected"

	CreditLotSourceAdmin    = "admin"
	CreditLotSourceSupplier = "supplier"

	// How the contributor is paid.
	//
	// CreditLotDealPurchase buys the credits outright: one payment at
	// activation, at the acquisition rate, and the lot owes nothing after that.
	//
	// CreditLotDealRevenueShare takes the key without buying it and pays a
	// share of what it earns, for as long as it earns -- a dividend rather than
	// a sale. The two can be combined: an acquisition rate above zero on a
	// revenue-share lot is a smaller payment up front plus a share of what is
	// left, which is what the margin basis nets off.
	CreditLotDealPurchase     = "purchase"
	CreditLotDealRevenueShare = "revenue_share"

	// CreditLotAttestationVersion names the wording of the right-to-transfer
	// attestation a lot was recorded under. Bump it when the wording changes so
	// old lots keep pointing at what was actually agreed.
	CreditLotAttestationVersion = "2026-09-credit-supply-v1"

	// creditShareCentThreshold is half a cent: below it a dividend is rounding
	// noise, not a debt, and paying it out would cost more than it is worth.
	creditShareCentThreshold = 0.005
)

var (
	ErrCreditLotChannelBound = errors.New("that channel already backs another live credit lot")
	ErrCreditLotNeedsChannel = errors.New("bind the lot to a channel before activating it")
	ErrCreditLotTransition   = errors.New("that status change is not allowed from the lot's current status")
	// ErrCreditLotApprovalNeedsConfirmation is enforced here, not only in the
	// screen: approval is the one moment the right-to-transfer question can be
	// asked, so the server refuses to activate a submission without the answer.
	ErrCreditLotNeedsPayment              = errors.New("a verified sale is activated by paying the supplier, not by approving it")
	ErrCreditLotApprovalNeedsConfirmation = errors.New("approval requires confirming the supplier's right to transfer these credits")
	ErrCreditLotReasonRequired            = errors.New("a reason is required when rejecting or suspending a lot")
	ErrCreditLotPaidFaceValue             = errors.New("this sale has been paid for; record more credit as a new sale rather than enlarging it")
	ErrCreditLotPaidRate                  = errors.New("this sale has been paid for at its rate; that rate is what its traffic was costed with")
	ErrCreditLotNothingToPay              = errors.New("this key was contributed, not sold: there is nothing to pay up front, accept it instead")
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
	// AcquisitionRate is what we pay per $1 of face value, in (0, 1]. It
	// becomes the bound channel's ChannelCostRatio on activation. A
	// revenue-share lot may set it to 0: nothing is bought up front.
	//
	// The column default is 0, not 1: GORM leaves a zero-valued field out of
	// an INSERT when the column has a default, so a default of 1 would quietly
	// turn every contributed key into a purchase at full face value. Nothing
	// relies on the default -- ValidateCreditLot refuses a rate of 0 on a lot
	// that is being bought.
	AcquisitionRate float64 `json:"acquisition_rate" gorm:"not null;default:0"`

	// DealType is purchase (bought outright) or revenue_share (a dividend on
	// what the key earns). See the constants above.
	DealType string `json:"deal_type" gorm:"type:varchar(16);not null;default:'purchase'"`
	// RevenueSharePct is the contributor's cut, frozen at submission so that
	// re-posting the terms never reprices a key that is already earning.
	RevenueSharePct float64 `json:"revenue_share_pct" gorm:"not null;default:0"`
	// RevenueShareBasis is the snapshot of what that cut is a cut of.
	RevenueShareBasis string `json:"revenue_share_basis" gorm:"type:varchar(16)"`
	// ShareRevenueUSD is everything customers paid for traffic this lot served
	// and ShareCostUSD what that traffic cost us up front (drawn face value at
	// the acquisition rate). Both accrue on the consume path, in the same
	// statement as the draw-down, and only on revenue-share lots. The share
	// itself is derived from them rather than accrued per request, so a
	// loss-making request cannot be rounded up to zero and paid for anyway.
	ShareRevenueUSD float64 `json:"share_revenue_usd" gorm:"column:share_revenue_usd;not null;default:0"`
	ShareCostUSD    float64 `json:"share_cost_usd" gorm:"column:share_cost_usd;not null;default:0"`
	// PaidShareUSD is the dividend paid out to date; see credit_share_payout.go.
	PaidShareUSD float64 `json:"paid_share_usd" gorm:"column:paid_share_usd;not null;default:0"`
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

	// Verification: the automatic request made against the submitted key.
	VerifiedAt       int64  `json:"verified_at" gorm:"not null;default:0"`
	VerificationNote string `json:"verification_note" gorm:"type:varchar(500)"`

	// The purchase. We pay first and consume afterwards, so these are filled
	// exactly once, when the operator settles the sale.
	PayoutMethod  string `json:"payout_method" gorm:"type:varchar(24)"`
	PayoutAccount string `json:"payout_account" gorm:"type:varchar(255)"`
	// PayoutQuoteMultiplier is (1 + the platform-credit bonus) this sale was
	// quoted under, frozen when it was made: the posted terms are a quote, and
	// a quote the seller accepted must survive the operator editing the terms
	// before the payment is made. 0 marks a lot from before the snapshot
	// existed, which follows the live terms exactly as it always did.
	//
	// The multiplier rather than the bonus, because a zero bonus is the common
	// case and a column whose "not set" value is also a real value cannot tell
	// the two apart -- GORM omits zero-valued fields from an INSERT.
	PayoutQuoteMultiplier float64 `json:"payout_quote_multiplier" gorm:"not null;default:0"`
	PayoutReference       string  `json:"payout_reference" gorm:"type:varchar(191)"`
	PaidUSD               float64 `json:"paid_usd" gorm:"column:paid_usd;not null;default:0"`
	PaidAt                int64   `json:"paid_at" gorm:"not null;default:0"`
	PaidBy                string  `json:"paid_by" gorm:"type:varchar(64)"`
	// RetiredAt is when the lot became exhausted or expired.
	RetiredAt int64 `json:"retired_at" gorm:"not null;default:0"`

	// ChannelStatus is the bound channel's live status, attached on read for
	// the operator screens (not stored): the relay can auto-disable a channel
	// the lot still considers active.
	ChannelStatus int   `json:"channel_status" gorm:"-"`
	CreatedAt     int64 `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt     int64 `json:"updated_at" gorm:"autoUpdateTime"`
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
	// RevenueUSD is what customers paid for that day's traffic. It is the
	// chart behind a contributor's dividend; the money itself is derived from
	// the lot's own running totals, which are the record.
	RevenueUSD float64 `json:"revenue_usd" gorm:"column:revenue_usd;not null;default:0"`
}

// IsRevenueShare reports whether this lot pays its contributor a dividend
// rather than a one-off purchase price.
func (lot *CreditLot) IsRevenueShare() bool {
	return lot.DealType == CreditLotDealRevenueShare
}

// ShareBasisUSD is the amount the contributor's cut is taken from: revenue, or
// revenue net of what we paid up front, never below zero.
func (lot *CreditLot) ShareBasisUSD() float64 {
	if !lot.IsRevenueShare() {
		return 0
	}
	basis := lot.ShareRevenueUSD
	if lot.RevenueShareBasis != CreditShareBasisRevenue {
		basis -= lot.ShareCostUSD
	}
	if basis < 0 {
		return 0
	}
	return basis
}

// EarnedShareUSD is the dividend this lot has earned its contributor in total,
// paid and unpaid.
func (lot *CreditLot) EarnedShareUSD() float64 {
	return lot.ShareBasisUSD() * lot.RevenueSharePct
}

// UnpaidShareUSD is what is owed right now.
func (lot *CreditLot) UnpaidShareUSD() float64 {
	unpaid := lot.EarnedShareUSD() - lot.PaidShareUSD
	if unpaid < creditShareCentThreshold {
		return 0
	}
	return unpaid
}

func (lot *CreditLot) RemainingUSD() float64 {
	remaining := lot.FaceValueUSD - lot.ConsumedUSD
	if remaining < 0 {
		return 0
	}
	return remaining
}

// PayableUSD is what we still owe the supplier for this lot: consumption at
// the acquisition rate, less whatever was already paid.
//
// A sale bought outright is paid in full at activation (PayCreditLot), so it
// owes nothing however much of it is consumed afterwards -- the alternative
// reports the same credits as a debt a second time, on a screen whose whole
// job is to say what we owe. Operator-entered lots that were never paid up
// front still accrue here and are settled outside the system.
func (lot *CreditLot) PayableUSD() float64 {
	outstanding := lot.ConsumedUSD*lot.AcquisitionRate - lot.PaidUSD
	if outstanding < 0 {
		return 0
	}
	return outstanding
}

// Live reports whether the lot occupies its channel binding.
// creditLotLiveStatuses are the statuses in which a lot owns its channel and
// still matters operationally: pending (verifying), verified (awaiting
// payment), active, suspended. Retired and rejected lots are history.
var creditLotLiveStatuses = []string{CreditLotStatusPending, CreditLotStatusVerified, CreditLotStatusActive, CreditLotStatusSuspended}

func (lot *CreditLot) Live() bool {
	for _, status := range creditLotLiveStatuses {
		if lot.Status == status {
			return true
		}
	}
	return false
}

func ValidateCreditLot(lot *CreditLot, now int64) error {
	lot.Vendor = strings.ToLower(strings.TrimSpace(lot.Vendor))
	lot.Note = strings.TrimSpace(lot.Note)
	if lot.DealType == "" {
		lot.DealType = CreditLotDealPurchase
	}
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
	switch lot.DealType {
	case CreditLotDealPurchase:
		if lot.RevenueSharePct != 0 {
			return errors.New("a lot bought outright has no revenue share; contribute it instead")
		}
		if lot.AcquisitionRate <= 0 || lot.AcquisitionRate > 1 {
			return errors.New("acquisition rate must be in (0, 1]: we pay at most face value for credits")
		}
	case CreditLotDealRevenueShare:
		if lot.RevenueSharePct <= 0 || lot.RevenueSharePct > 1 {
			return errors.New("revenue share must be in (0, 1]: a contributed key earns its owner a share of what it makes")
		}
		// Zero is the ordinary case: nothing is paid up front, the whole deal
		// is the dividend.
		if lot.AcquisitionRate < 0 || lot.AcquisitionRate > 1 {
			return errors.New("acquisition rate must be in [0, 1] on a contributed key")
		}
		if lot.RevenueShareBasis != CreditShareBasisRevenue && lot.RevenueShareBasis != CreditShareBasisMargin {
			return fmt.Errorf("revenue share basis must be %q or %q", CreditShareBasisRevenue, CreditShareBasisMargin)
		}
	default:
		return fmt.Errorf("deal type must be %q or %q", CreditLotDealPurchase, CreditLotDealRevenueShare)
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
	lot.PayoutAccount = strings.TrimSpace(lot.PayoutAccount)
	if textLooksLikeProviderSecret(lot.Note, lot.PayoutAccount) {
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
		Where("channel_id = ? AND id <> ? AND status IN ?", channelId, exceptLotId, creditLotLiveStatuses).
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
	if lot.AcquisitionRate <= 0 {
		// A contributed key cost nothing up front, and ChannelCostRatio refuses
		// a zero multiplier because a free upstream makes every margin
		// infinite. Leaving the channel at the default 1 costs its traffic at
		// the vendor's list price, which understates our margin rather than
		// inflating it -- the conservative direction. What the key actually
		// owes its contributor is the dividend, tracked on the lot; it is not
		// expressible as a multiple of list price. See docs/credit-supply.md.
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
		// DealType, RevenueSharePct and the accrued share are deliberately not
		// in this list: the deal a contributor agreed to is not an editable
		// field, and candidate starts as a copy of the stored row.
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
		// A sale that has been paid for is settled history. Raising its face
		// value hands over credits nobody paid for; changing its rate rewrites
		// the cost basis of traffic already reconciled and invoiced.
		if existing.PaidAt != 0 {
			if candidate.FaceValueUSD != existing.FaceValueUSD {
				return ErrCreditLotPaidFaceValue
			}
			if candidate.AcquisitionRate != existing.AcquisitionRate {
				return ErrCreditLotPaidRate
			}
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
		// Locked for the transaction: two operators paying, verifying or
		// transitioning the same lot at once must serialise, or both would
		// pass the status check and the payout would be booked twice.
		if err := lockForUpdate(tx).First(&lot, "id = ?", id).Error; err != nil {
			return err
		}
		from := lot.Status
		allowed := false
		switch lot.Status {
		case CreditLotStatusPending:
			// A supplier's own submission is paid for (PayCreditLot), never
			// approved for free; only operator-entered lots activate directly.
			allowed = (to == CreditLotStatusActive && lot.Source != CreditLotSourceSupplier) || to == CreditLotStatusRejected
		case CreditLotStatusVerified:
			if to == CreditLotStatusActive && lot.PurchasePriceUSD() > 0 {
				// Something is owed up front: the payment is the activation.
				return ErrCreditLotNeedsPayment
			}
			// A contributed key costs nothing up front, so accepting it is the
			// whole decision -- and it still has to be an explicit one, with
			// the right-to-transfer question answered.
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
			if (from == CreditLotStatusPending || from == CreditLotStatusVerified) && !req.TransferRightsConfirmed {
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
			if from == CreditLotStatusPending || from == CreditLotStatusVerified {
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
	case CreditLotStatusSuspended:
		if lot.ChannelId != 0 {
			UpdateChannelStatus(lot.ChannelId, "", common.ChannelStatusManuallyDisabled, channelReason)
		}
	case CreditLotStatusRejected:
		if lot.ChannelId != 0 {
			if lot.Source == CreditLotSourceSupplier {
				// The channel exists only to carry this seller's key. A rejected
				// sale must not leave that key parked in a disabled channel.
				if err := detachAndDeleteLotChannel(&lot); err != nil {
					return &lot, err
				}
			} else {
				UpdateChannelStatus(lot.ChannelId, "", common.ChannelStatusManuallyDisabled, channelReason)
			}
		}
	}
	return &lot, nil
}

// detachAndDeleteLotChannel removes a supplier lot's channel -- and with it
// the key -- keeping the lot as a record with channel_id 0.
func detachAndDeleteLotChannel(lot *CreditLot) error {
	channelId := lot.ChannelId
	if channelId == 0 {
		return nil
	}
	if err := DB.Model(&CreditLot{}).Where("id = ?", lot.Id).Update("channel_id", 0).Error; err != nil {
		return err
	}
	lot.ChannelId = 0
	if channel, err := GetChannelById(channelId, false); err == nil && channel != nil {
		if err := channel.Delete(); err != nil {
			common.SysError(fmt.Sprintf("credit supply: could not remove channel %d of rejected lot %d: %v", channelId, lot.Id, err))
		}
	}
	invalidateChannelLot(channelId)
	return nil
}

// PurchasePriceUSD is what the sale costs us under the rate the lot was
// submitted with. It is what PayCreditLot pays, before any platform-credit
// bonus.
func (lot *CreditLot) PurchasePriceUSD() float64 {
	return lot.FaceValueUSD * lot.AcquisitionRate
}

// PayoutUSD is what the supplier receives for this sale: the purchase price
// under the rate the lot was submitted with, plus the platform-credit bonus
// the sale was quoted under. terms supplies the bonus only for lots that
// predate the snapshot.
func (lot *CreditLot) PayoutUSD(terms CreditSupplyTerms) float64 {
	amount := lot.PurchasePriceUSD()
	if lot.PayoutMethod != CreditLotPayoutPlatformCredit {
		return amount
	}
	multiplier := lot.PayoutQuoteMultiplier
	if multiplier <= 0 {
		multiplier = 1 + terms.PlatformCreditBonus
	}
	return amount * multiplier
}

// MarkCreditLotVerified records that the submitted key answered a real
// request. The lot now waits for payment; the channel stays disabled.
// verificationUSD is the list-price cost of the verification request, booked
// against the lot's face value: the seller's vendor balance really did go
// down by that much, so the remaining figure must not overstate it.
func MarkCreditLotVerified(id int, actor, note string, verificationUSD float64) (*CreditLot, error) {
	var lot CreditLot
	err := DB.Transaction(func(tx *gorm.DB) error {
		// Locked for the transaction: two operators paying, verifying or
		// transitioning the same lot at once must serialise, or both would
		// pass the status check and the payout would be booked twice.
		if err := lockForUpdate(tx).First(&lot, "id = ?", id).Error; err != nil {
			return err
		}
		if lot.Status != CreditLotStatusPending {
			return fmt.Errorf("%w: %s -> %s", ErrCreditLotTransition, lot.Status, CreditLotStatusVerified)
		}
		lot.Status = CreditLotStatusVerified
		lot.VerifiedAt = common.GetTimestamp()
		lot.VerificationNote = strings.TrimSpace(note)
		if verificationUSD > 0 {
			lot.ConsumedUSD += verificationUSD
		}
		if err := tx.Save(&lot).Error; err != nil {
			return err
		}
		return appendCreditLotEvent(tx, lot.Id, actor, "verified", CreditLotStatusPending, CreditLotStatusVerified, note)
	})
	if err != nil {
		return nil, err
	}
	invalidateChannelLot(lot.ChannelId)
	return &lot, nil
}

// CreditLotPayment is the operator settling a verified sale.
type CreditLotPayment struct {
	Actor string
	// Method overrides the supplier's choice only when set.
	Method string
	// Reference is required for an external transfer: the bank/PayPal id the
	// supplier can look up. Platform credit needs none; the ledger is it.
	Reference string
}

// PayCreditLot pays the supplier and activates the lot in one step. Payment
// in platform credit is booked into the supplier's own wallet inside the same
// transaction, so a lot can never be live without its payment or vice versa.
// External payment is recorded, not moved: the operator has already sent it.
func PayCreditLot(id int, payment CreditLotPayment) (*CreditLot, error) {
	reference := strings.TrimSpace(payment.Reference)
	if textLooksLikeProviderSecret(reference) {
		return nil, ErrCreditLotSecretInText
	}
	terms := GetCreditSupplyTerms()
	now := common.GetTimestamp()
	var lot CreditLot
	var paidQuota int
	var supplier CreditSupplier
	var wallet BillingEntity
	err := DB.Transaction(func(tx *gorm.DB) error {
		// Locked for the transaction: two operators paying, verifying or
		// transitioning the same lot at once must serialise, or both would
		// pass the status check and the payout would be booked twice.
		if err := lockForUpdate(tx).First(&lot, "id = ?", id).Error; err != nil {
			return err
		}
		if lot.Status != CreditLotStatusVerified {
			return fmt.Errorf("%w: only a verified sale can be paid, this lot is %s", ErrCreditLotTransition, lot.Status)
		}
		if lot.ChannelId == 0 {
			return ErrCreditLotNeedsChannel
		}
		if lot.PurchasePriceUSD() <= 0 {
			return ErrCreditLotNothingToPay
		}
		if lot.ExpiresAt != 0 && lot.ExpiresAt <= now {
			return errors.New("the credits have already expired; reject the sale instead")
		}
		if err := tx.First(&supplier, "id = ?", lot.SupplierId).Error; err != nil {
			return err
		}
		if err := ensureChannelFree(tx, lot.ChannelId, lot.Id); err != nil {
			return err
		}
		method := lot.PayoutMethod
		if payment.Method != "" {
			method = payment.Method
		}
		switch method {
		case CreditLotPayoutPlatformCredit:
			if supplier.UserId <= 0 {
				return errors.New("this supplier has no login to credit; pay externally instead")
			}
		case CreditLotPayoutExternal:
			if reference == "" {
				return errors.New("record the transfer reference the supplier can look up")
			}
		default:
			return fmt.Errorf("unknown payout method %q", method)
		}
		lot.PayoutMethod = method
		lot.PayoutReference = reference
		lot.PaidUSD = lot.PayoutUSD(terms)
		lot.PaidAt = now
		lot.PaidBy = payment.Actor
		lot.ApprovedBy = payment.Actor
		lot.ApprovedAt = now
		lot.Status = CreditLotStatusActive
		lot.StatusReason = ""
		lot.RetiredAt = 0
		if err := tx.Save(&lot).Error; err != nil {
			return err
		}
		if method == CreditLotPayoutPlatformCredit {
			// Through the billing entity: a login inside a tenant is paid
			// into the tenant wallet it actually spends from.
			paidQuota = int(lot.PaidUSD * common.QuotaPerUnit)
			entity, err := IncreaseUserQuotaWithTx(tx, supplier.UserId, paidQuota)
			if err != nil {
				return err
			}
			wallet = entity
		}
		message := fmt.Sprintf("paid %.2f USD via %s %s", lot.PaidUSD, method, reference)
		return appendCreditLotEvent(tx, lot.Id, payment.Actor, "paid", CreditLotStatusVerified, CreditLotStatusActive, message)
	})
	if err != nil {
		return nil, err
	}
	if paidQuota > 0 {
		RecordLog(supplier.UserId, LogTypeTopup, fmt.Sprintf("credit supply: sale of lot #%d paid in platform credit, %s", lot.Id, logger.LogQuota(paidQuota)))
		_ = invalidateBillingQuotaCache(wallet)
	}
	invalidateChannelLot(lot.ChannelId)
	invalidateChannelSupplierIndex()
	UpdateChannelStatus(lot.ChannelId, "", common.ChannelStatusEnabled, "")
	if err := applyLotCostRatio(&lot, payment.Actor); err != nil {
		return &lot, err
	}
	return &lot, nil
}

// attachChannelStatus fills the transient ChannelStatus of every lot that is
// bound to a channel, in one query.
func attachChannelStatus(lots []*CreditLot) error {
	ids := make([]int, 0, len(lots))
	for _, lot := range lots {
		if lot.ChannelId != 0 {
			ids = append(ids, lot.ChannelId)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	var rows []struct {
		Id     int
		Status int
	}
	if err := DB.Model(&Channel{}).Select("id, status").Where("id IN ?", ids).Scan(&rows).Error; err != nil {
		return err
	}
	status := make(map[int]int, len(rows))
	for _, row := range rows {
		status[row.Id] = row.Status
	}
	for _, lot := range lots {
		lot.ChannelStatus = status[lot.ChannelId]
	}
	return nil
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
	if err := query.Order("id desc").Find(&lots).Error; err != nil {
		return nil, err
	}
	if err := attachChannelStatus(lots); err != nil {
		return nil, err
	}
	return lots, nil
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
	// ShareUnpaidUSD is the dividend owed on this vendor's contributed keys.
	ShareRevenueUSD float64 `json:"share_revenue_usd"`
	ShareUnpaidUSD  float64 `json:"share_unpaid_usd"`
}

// CreditSupplyOverview is the headline the admin screen opens on.
type CreditSupplyOverview struct {
	Suppliers    int            `json:"suppliers"`
	LotsByStatus map[string]int `json:"lots_by_status"`
	FaceUSD      float64        `json:"face_usd"`
	ConsumedUSD  float64        `json:"consumed_usd"`
	RemainingUSD float64        `json:"remaining_usd"`
	PayableUSD   float64        `json:"payable_usd"`
	// AwaitingPaymentUSD is what verified sales will cost us when settled;
	// PaidUSD is what has been paid out to date.
	AwaitingPaymentUSD float64 `json:"awaiting_payment_usd"`
	PaidUSD            float64 `json:"paid_usd"`
	UnpricedLots       int     `json:"unpriced_lots"`
	// The dividend side: what contributed keys have earned their owners, what
	// has been paid, and what is owed now. Unlike a purchase, this is an
	// ongoing liability that grows with traffic.
	Share             CreditShareTotals          `json:"share"`
	MinSharePayoutUSD float64                    `json:"min_share_payout_usd"`
	ByVendor          []CreditSupplyVendorTotals `json:"by_vendor"`
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
	terms := GetCreditSupplyTerms()
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
		if lot.Status == CreditLotStatusVerified {
			overview.AwaitingPaymentUSD += lot.PayoutUSD(terms)
		}
		overview.PaidUSD += lot.PaidUSD
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
		totals.ShareRevenueUSD += lot.ShareRevenueUSD
		totals.ShareUnpaidUSD += lot.UnpaidShareUSD()

		needsAttention := lot.Status == CreditLotStatusPending ||
			lot.Status == CreditLotStatusVerified ||
			(lot.Status == CreditLotStatusActive && lot.LowWaterUSD > 0 && lot.RemainingUSD() <= lot.LowWaterUSD) ||
			(lot.Live() && lot.ExpiresAt != 0 && lot.ExpiresAt-now <= 7*86400) ||
			// An active lot whose channel the relay auto-disabled (bad key,
			// vendor says no balance) looks live in this ledger while nothing
			// flows through it. The face value was probably overstated.
			(lot.Status == CreditLotStatusActive && lot.ChannelStatus != 0 && lot.ChannelStatus != common.ChannelStatusEnabled) ||
			// A contributor is owed enough to be paid. Nothing chases this on
			// its own, so it belongs on the list the operator already reads.
			(lot.UnpaidShareUSD() >= terms.MinSharePayoutUSD && lot.UnpaidShareUSD() > 0)
		if needsAttention {
			overview.Attention = append(overview.Attention, lot)
		}
	}
	overview.Share = SumCreditShare(lots)
	overview.MinSharePayoutUSD = terms.MinSharePayoutUSD
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
