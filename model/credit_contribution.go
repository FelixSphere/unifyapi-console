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
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

const (
	CreditContributionSubmitted        = "submitted"
	CreditContributionNeedsCredentials = "needs_credentials"
	CreditContributionVerifying        = "verifying"
	CreditContributionActive           = "active"
	CreditContributionRevoked          = "revoked"
	CreditContributionRejected         = "rejected"
	CreditContributionCancelled        = "cancelled"

	CreditPayoutDraft    = "draft"
	CreditPayoutApproved = "approved"
	CreditPayoutPaid     = "paid"
	CreditPayoutVoid     = "void"

	CreditContributionAttestationVersion = "2026-09-credits-resale-v1"
)

var (
	ErrContributionOwnerRequired = errors.New("only the tenant owner can manage credit contributions")
	ErrContributionTransition    = errors.New("credit contribution status transition is not allowed")
	ErrPayoutExceedsAvailable    = errors.New("payout exceeds available payable")
)

// CreditContribution is a supplier offer and its current verified cycle. It
// deliberately contains no provider credential: credentials stay in the
// existing root-managed channel boundary.
type CreditContribution struct {
	Id                        int     `json:"id"`
	TenantId                  int     `json:"tenant_id" gorm:"index;not null"`
	SubmittedBy               int     `json:"submitted_by" gorm:"index;not null"`
	Provider                  string  `json:"provider" gorm:"type:varchar(32);index;not null"`
	AccountLabel              string  `json:"account_label" gorm:"type:varchar(120)"`
	Models                    string  `json:"models" gorm:"type:text"`
	RequestedQuota            int64   `json:"requested_quota" gorm:"not null"`
	RequestedAcquisitionRatio float64 `json:"requested_acquisition_ratio" gorm:"type:decimal(10,6);default:0"`
	ApprovedQuota             int64   `json:"approved_quota" gorm:"default:0"`
	AcquisitionRatio          float64 `json:"acquisition_ratio" gorm:"type:decimal(10,6);default:0"`
	PoolId                    int     `json:"pool_id" gorm:"index;default:0"`
	ChannelId                 int     `json:"channel_id" gorm:"index;default:0"`
	CurrentLotId              int     `json:"current_lot_id" gorm:"index;default:0"`
	Cycle                     int     `json:"cycle" gorm:"default:0"`
	ExpiresAt                 int64   `json:"expires_at" gorm:"index;default:0"`
	Status                    string  `json:"status" gorm:"type:varchar(32);index;not null"`
	SupplierNotes             string  `json:"supplier_notes" gorm:"type:varchar(1000)"`
	AdminNotes                string  `json:"-" gorm:"type:varchar(1000)"`
	RejectionReason           string  `json:"rejection_reason,omitempty" gorm:"type:varchar(500)"`
	AttestationVersion        string  `json:"attestation_version" gorm:"type:varchar(64);not null"`
	AttestedAt                int64   `json:"attested_at" gorm:"not null"`
	LastVerifiedAt            int64   `json:"last_verified_at" gorm:"default:0"`
	CreatedAt                 int64   `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt                 int64   `json:"updated_at" gorm:"autoUpdateTime"`
}

type CreditContributionEvent struct {
	Id             int    `json:"id"`
	ContributionId int    `json:"contribution_id" gorm:"index;not null"`
	ActorUserId    int    `json:"-" gorm:"index;not null"`
	EventType      string `json:"event_type" gorm:"type:varchar(40);index;not null"`
	FromStatus     string `json:"from_status" gorm:"type:varchar(32)"`
	ToStatus       string `json:"to_status" gorm:"type:varchar(32)"`
	Message        string `json:"message" gorm:"type:varchar(500)"`
	CreatedAt      int64  `json:"created_at" gorm:"autoCreateTime"`
}

type CreditContributionPayout struct {
	Id                int    `json:"id"`
	ContributionId    int    `json:"contribution_id" gorm:"index;not null"`
	TenantId          int    `json:"tenant_id" gorm:"index;not null"`
	AmountQuota       int64  `json:"amount_quota" gorm:"not null"`
	Status            string `json:"status" gorm:"type:varchar(16);index;not null"`
	ExternalReference string `json:"external_reference" gorm:"type:varchar(160)"`
	Note              string `json:"note" gorm:"type:varchar(500)"`
	CreatedBy         int    `json:"-" gorm:"index;not null"`
	ApprovedAt        int64  `json:"approved_at" gorm:"default:0"`
	PaidAt            int64  `json:"paid_at" gorm:"default:0"`
	CreatedAt         int64  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt         int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

type CreditContributionSummary struct {
	CreditContribution
	EffectiveStatus      string                     `json:"effective_status"`
	InventoryRemaining   int64                      `json:"inventory_remaining"`
	ConsumedQuota        int64                      `json:"consumed_quota"`
	LifetimePayableQuota int64                      `json:"lifetime_payable_quota"`
	CommittedPayoutQuota int64                      `json:"committed_payout_quota"`
	AvailablePayoutQuota int64                      `json:"available_payout_quota"`
	AdminNotes           string                     `json:"admin_notes,omitempty"`
	Events               []CreditContributionEvent  `json:"events,omitempty"`
	Payouts              []CreditContributionPayout `json:"payouts,omitempty"`
}

type CreateCreditContributionInput struct {
	Provider                  string  `json:"provider"`
	AccountLabel              string  `json:"account_label"`
	Models                    string  `json:"models"`
	RequestedQuota            int64   `json:"requested_quota"`
	RequestedAcquisitionRatio float64 `json:"requested_acquisition_ratio"`
	SupplierNotes             string  `json:"supplier_notes"`
	Attested                  bool    `json:"attested"`
}

type ActivateCreditContributionInput struct {
	PoolId           int     `json:"pool_id"`
	ChannelId        int     `json:"channel_id"`
	ApprovedQuota    int64   `json:"approved_quota"`
	AcquisitionRatio float64 `json:"acquisition_ratio"`
	ExpiresAt        int64   `json:"expires_at"`
	AdminNotes       string  `json:"admin_notes"`
}

type ResetCreditContributionInput struct {
	VerifiedQuota int64  `json:"verified_quota"`
	ExpiresAt     int64  `json:"expires_at"`
	Reason        string `json:"reason"`
}

func containsProviderSecret(values ...string) bool {
	for _, value := range values {
		lower := strings.ToLower(value)
		for _, marker := range []string{"sk-ant-", "sk-proj-", "sk-live-", "bearer eyj", "api_key="} {
			if strings.Contains(lower, marker) {
				return true
			}
		}
	}
	return false
}

func normalizeContributionProvider(provider string) (string, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	switch provider {
	case "openai", "anthropic", "other":
		return provider, nil
	default:
		return "", errors.New("provider must be openai, anthropic, or other")
	}
}

func contributionTenantForOwner(tx *gorm.DB, userId int) (*Tenant, error) {
	var user User
	if err := tx.First(&user, userId).Error; err != nil {
		return nil, err
	}
	if user.TenantId <= 0 {
		return nil, ErrContributionOwnerRequired
	}
	var tenant Tenant
	if err := tx.First(&tenant, user.TenantId).Error; err != nil {
		return nil, err
	}
	if tenant.OwnerId != userId {
		return nil, ErrContributionOwnerRequired
	}
	return &tenant, nil
}

func appendContributionEvent(tx *gorm.DB, contributionId, actorUserId int, eventType, fromStatus, toStatus, message string) error {
	event := CreditContributionEvent{
		ContributionId: contributionId, ActorUserId: actorUserId, EventType: eventType,
		FromStatus: fromStatus, ToStatus: toStatus, Message: strings.TrimSpace(message),
	}
	return tx.Create(&event).Error
}

func CreateCreditContribution(userId int, input CreateCreditContributionInput) (*CreditContribution, error) {
	provider, err := normalizeContributionProvider(input.Provider)
	if err != nil {
		return nil, err
	}
	if !input.Attested {
		return nil, errors.New("supplier authorization attestation is required")
	}
	if input.RequestedQuota <= 0 {
		return nil, errors.New("requested quota must be positive")
	}
	if input.RequestedAcquisitionRatio < 0 || input.RequestedAcquisitionRatio > 1 {
		return nil, errors.New("requested acquisition ratio must be between 0 and 1")
	}
	input.AccountLabel = strings.TrimSpace(input.AccountLabel)
	input.Models = strings.TrimSpace(input.Models)
	input.SupplierNotes = strings.TrimSpace(input.SupplierNotes)
	if containsProviderSecret(input.AccountLabel, input.Models, input.SupplierNotes) {
		return nil, errors.New("provider credentials must not be submitted in this form")
	}
	if len(input.AccountLabel) > 120 || len(input.Models) > 1000 || len(input.SupplierNotes) > 1000 {
		return nil, errors.New("contribution details are too long")
	}
	var contribution CreditContribution
	err = DB.Transaction(func(tx *gorm.DB) error {
		tenant, err := contributionTenantForOwner(tx, userId)
		if err != nil {
			return err
		}
		now := time.Now().Unix()
		contribution = CreditContribution{
			TenantId: tenant.Id, SubmittedBy: userId, Provider: provider,
			AccountLabel: input.AccountLabel, Models: input.Models, RequestedQuota: input.RequestedQuota,
			RequestedAcquisitionRatio: input.RequestedAcquisitionRatio, SupplierNotes: input.SupplierNotes,
			Status: CreditContributionSubmitted, AttestationVersion: CreditContributionAttestationVersion, AttestedAt: now,
		}
		if err := tx.Create(&contribution).Error; err != nil {
			return err
		}
		return appendContributionEvent(tx, contribution.Id, userId, "submitted", "", contribution.Status, "Credit offer submitted for review")
	})
	return &contribution, err
}

func CancelCreditContribution(userId, contributionId int) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		tenant, err := contributionTenantForOwner(tx, userId)
		if err != nil {
			return err
		}
		var contribution CreditContribution
		if err := lockForUpdate(tx).Where("id = ? AND tenant_id = ?", contributionId, tenant.Id).First(&contribution).Error; err != nil {
			return err
		}
		if contribution.Status != CreditContributionSubmitted && contribution.Status != CreditContributionNeedsCredentials {
			return ErrContributionTransition
		}
		from := contribution.Status
		if err := tx.Model(&contribution).Update("status", CreditContributionCancelled).Error; err != nil {
			return err
		}
		return appendContributionEvent(tx, contribution.Id, userId, "cancelled", from, CreditContributionCancelled, "Credit offer cancelled by supplier")
	})
}

func ReviewCreditContribution(contributionId, actorUserId int, status, message, adminNotes string) error {
	status = strings.TrimSpace(status)
	if status != CreditContributionNeedsCredentials && status != CreditContributionVerifying && status != CreditContributionRejected {
		return errors.New("review status must be needs_credentials, verifying, or rejected")
	}
	if status == CreditContributionRejected && strings.TrimSpace(message) == "" {
		return errors.New("rejection reason is required")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var contribution CreditContribution
		if err := lockForUpdate(tx).First(&contribution, contributionId).Error; err != nil {
			return err
		}
		if contribution.Status == CreditContributionActive || contribution.Status == CreditContributionRevoked || contribution.Status == CreditContributionRejected || contribution.Status == CreditContributionCancelled {
			return ErrContributionTransition
		}
		from := contribution.Status
		updates := map[string]any{"status": status, "admin_notes": strings.TrimSpace(adminNotes)}
		if status == CreditContributionRejected {
			updates["rejection_reason"] = strings.TrimSpace(message)
		}
		if err := tx.Model(&contribution).Updates(updates).Error; err != nil {
			return err
		}
		publicMessage := strings.TrimSpace(message)
		if publicMessage == "" {
			publicMessage = "Credit offer review status updated"
		}
		return appendContributionEvent(tx, contribution.Id, actorUserId, "reviewed", from, status, publicMessage)
	})
}

func validateContributionChannel(tx *gorm.DB, poolId, channelId int) error {
	var pool CreditPool
	if err := tx.Where("id = ? AND status = ?", poolId, CreditPoolStatusEnabled).First(&pool).Error; err != nil {
		return errors.New("active credit pool not found")
	}
	var channel Channel
	query := ApplyChannelGroupFilter(tx.Model(&Channel{}), pool.RoutingGroup)
	if err := query.Where("id = ? AND status = ?", channelId, common.ChannelStatusEnabled).First(&channel).Error; err != nil {
		return errors.New("enabled channel in the pool routing group not found")
	}
	return nil
}

func ActivateCreditContribution(contributionId, actorUserId int, input ActivateCreditContributionInput) (*CreditContribution, error) {
	if input.PoolId <= 0 || input.ChannelId <= 0 || input.ApprovedQuota <= 0 {
		return nil, errors.New("pool, channel, and positive approved quota are required")
	}
	if input.AcquisitionRatio <= 0 || input.AcquisitionRatio > 1 {
		return nil, errors.New("acquisition ratio must be greater than zero and at most one")
	}
	if input.ExpiresAt > 0 && input.ExpiresAt <= time.Now().Unix() {
		return nil, errors.New("expiry must be in the future")
	}
	var contribution CreditContribution
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).First(&contribution, contributionId).Error; err != nil {
			return err
		}
		if contribution.Status != CreditContributionSubmitted && contribution.Status != CreditContributionNeedsCredentials && contribution.Status != CreditContributionVerifying {
			return ErrContributionTransition
		}
		if contribution.AttestedAt == 0 {
			return errors.New("supplier authorization attestation is missing")
		}
		if err := validateContributionChannel(tx, input.PoolId, input.ChannelId); err != nil {
			return err
		}
		lot := CreditPoolLot{
			PoolId: input.PoolId, ContributionId: contribution.Id, ChannelId: input.ChannelId,
			SourceType: CreditPoolSourceContributed, ContributorTenantId: contribution.TenantId,
			Label:         fmt.Sprintf("%s contribution #%d cycle 1", contribution.Provider, contribution.Id),
			OriginalQuota: input.ApprovedQuota, AcquisitionRatio: input.AcquisitionRatio, ExpiresAt: input.ExpiresAt,
		}
		if err := addCreditPoolLot(tx, &lot); err != nil {
			return err
		}
		from := contribution.Status
		updates := map[string]any{
			"pool_id": input.PoolId, "channel_id": input.ChannelId, "approved_quota": input.ApprovedQuota,
			"acquisition_ratio": input.AcquisitionRatio, "expires_at": input.ExpiresAt,
			"current_lot_id": lot.Id, "cycle": 1, "status": CreditContributionActive,
			"last_verified_at": time.Now().Unix(), "admin_notes": strings.TrimSpace(input.AdminNotes), "rejection_reason": "",
		}
		if err := tx.Model(&contribution).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.First(&contribution, contribution.Id).Error; err != nil {
			return err
		}
		return appendContributionEvent(tx, contribution.Id, actorUserId, "activated", from, contribution.Status, "Credits verified and activated in the pool")
	})
	return &contribution, err
}

// ResetCreditContribution starts a new verified provider-credit cycle. The old
// lot is disabled rather than overwritten so consumption and payable history
// remain immutable.
func ResetCreditContribution(contributionId, actorUserId int, input ResetCreditContributionInput) (*CreditContribution, error) {
	if input.VerifiedQuota <= 0 || strings.TrimSpace(input.Reason) == "" {
		return nil, errors.New("positive verified quota and reset reason are required")
	}
	if input.ExpiresAt > 0 && input.ExpiresAt <= time.Now().Unix() {
		return nil, errors.New("expiry must be in the future")
	}
	var contribution CreditContribution
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).First(&contribution, contributionId).Error; err != nil {
			return err
		}
		if contribution.Status != CreditContributionActive {
			return ErrContributionTransition
		}
		if err := validateContributionChannel(tx, contribution.PoolId, contribution.ChannelId); err != nil {
			return err
		}
		if contribution.CurrentLotId > 0 {
			if err := tx.Model(&CreditPoolLot{}).Where("id = ? AND contribution_id = ?", contribution.CurrentLotId, contribution.Id).
				Update("status", CreditPoolStatusDisabled).Error; err != nil {
				return err
			}
		}
		cycle := contribution.Cycle + 1
		lot := CreditPoolLot{
			PoolId: contribution.PoolId, ContributionId: contribution.Id, ChannelId: contribution.ChannelId,
			SourceType: CreditPoolSourceContributed, ContributorTenantId: contribution.TenantId,
			Label:         fmt.Sprintf("%s contribution #%d cycle %d", contribution.Provider, contribution.Id, cycle),
			OriginalQuota: input.VerifiedQuota, AcquisitionRatio: contribution.AcquisitionRatio, ExpiresAt: input.ExpiresAt,
		}
		if err := addCreditPoolLot(tx, &lot); err != nil {
			return err
		}
		updates := map[string]any{"approved_quota": input.VerifiedQuota, "current_lot_id": lot.Id, "cycle": cycle, "expires_at": input.ExpiresAt, "last_verified_at": time.Now().Unix()}
		if err := tx.Model(&contribution).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.First(&contribution, contribution.Id).Error; err != nil {
			return err
		}
		return appendContributionEvent(tx, contribution.Id, actorUserId, "reset", CreditContributionActive, CreditContributionActive, strings.TrimSpace(input.Reason))
	})
	return &contribution, err
}

func RevokeCreditContribution(contributionId, actorUserId int, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return errors.New("revocation reason is required")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var contribution CreditContribution
		if err := lockForUpdate(tx).First(&contribution, contributionId).Error; err != nil {
			return err
		}
		if contribution.Status != CreditContributionActive {
			return ErrContributionTransition
		}
		if err := tx.Model(&CreditPoolLot{}).Where("contribution_id = ? AND status = ?", contribution.Id, CreditPoolStatusEnabled).
			Update("status", CreditPoolStatusDisabled).Error; err != nil {
			return err
		}
		if err := tx.Model(&contribution).Update("status", CreditContributionRevoked).Error; err != nil {
			return err
		}
		return appendContributionEvent(tx, contribution.Id, actorUserId, "revoked", CreditContributionActive, CreditContributionRevoked, reason)
	})
}

func contributionFinancials(tx *gorm.DB, contributionId int) (remaining, consumed, earned, committed int64, err error) {
	err = tx.Model(&CreditPoolLot{}).Where("contribution_id = ? AND status = ?", contributionId, CreditPoolStatusEnabled).
		Where("expires_at = 0 OR expires_at > ?", time.Now().Unix()).
		Select("COALESCE(SUM(remaining_quota),0)").Scan(&remaining).Error
	if err != nil {
		return
	}
	var payable float64
	err = tx.Table("credit_pool_reservation_lots AS a").
		Select("COALESCE(SUM(a.quota),0), COALESCE(SUM(a.quota * l.acquisition_ratio),0)").
		Joins("JOIN credit_pool_lots l ON l.id = a.lot_id").
		Joins("JOIN credit_pool_reservations r ON r.id = a.reservation_id").
		Where("l.contribution_id = ? AND r.status = ?", contributionId, CreditReservationSettled).
		Row().Scan(&consumed, &payable)
	if err != nil {
		return
	}
	earned = int64(math.Round(payable))
	err = tx.Model(&CreditContributionPayout{}).
		Where("contribution_id = ? AND status <> ?", contributionId, CreditPayoutVoid).
		Select("COALESCE(SUM(amount_quota),0)").Scan(&committed).Error
	return
}

func contributionEffectiveStatus(contribution CreditContribution, remaining int64) string {
	if contribution.Status != CreditContributionActive {
		return contribution.Status
	}
	if contribution.ExpiresAt > 0 && contribution.ExpiresAt <= time.Now().Unix() {
		return "expired"
	}
	if remaining <= 0 {
		return "exhausted"
	}
	return contribution.Status
}

func contributionSummary(tx *gorm.DB, contribution CreditContribution, includeTimeline bool) (CreditContributionSummary, error) {
	remaining, consumed, earned, committed, err := contributionFinancials(tx, contribution.Id)
	if err != nil {
		return CreditContributionSummary{}, err
	}
	summary := CreditContributionSummary{
		CreditContribution: contribution, EffectiveStatus: contributionEffectiveStatus(contribution, remaining),
		InventoryRemaining: remaining, ConsumedQuota: consumed, LifetimePayableQuota: earned,
		CommittedPayoutQuota: committed, AvailablePayoutQuota: earned - committed,
	}
	if includeTimeline {
		if err := tx.Where("contribution_id = ?", contribution.Id).Order("id asc").Find(&summary.Events).Error; err != nil {
			return summary, err
		}
		if err := tx.Where("contribution_id = ?", contribution.Id).Order("id desc").Find(&summary.Payouts).Error; err != nil {
			return summary, err
		}
	}
	return summary, nil
}

func ListUserCreditContributions(userId int) ([]CreditContributionSummary, error) {
	var user User
	if err := DB.First(&user, userId).Error; err != nil {
		return nil, err
	}
	if user.TenantId <= 0 {
		return []CreditContributionSummary{}, nil
	}
	var contributions []CreditContribution
	if err := DB.Where("tenant_id = ?", user.TenantId).Order("id desc").Find(&contributions).Error; err != nil {
		return nil, err
	}
	return summarizeContributions(DB, contributions, true)
}

func ListCreditContributions() ([]CreditContributionSummary, error) {
	var contributions []CreditContribution
	if err := DB.Order("id desc").Find(&contributions).Error; err != nil {
		return nil, err
	}
	summaries, err := summarizeContributions(DB, contributions, true)
	if err != nil {
		return nil, err
	}
	for i := range summaries {
		summaries[i].AdminNotes = contributions[i].AdminNotes
	}
	return summaries, nil
}

func summarizeContributions(tx *gorm.DB, contributions []CreditContribution, includeTimeline bool) ([]CreditContributionSummary, error) {
	out := make([]CreditContributionSummary, 0, len(contributions))
	for _, contribution := range contributions {
		summary, err := contributionSummary(tx, contribution, includeTimeline)
		if err != nil {
			return nil, err
		}
		out = append(out, summary)
	}
	return out, nil
}

func CreateContributionPayout(contributionId, actorUserId int, amount int64, note string) (*CreditContributionPayout, error) {
	if amount <= 0 {
		return nil, errors.New("payout amount must be positive")
	}
	if len(strings.TrimSpace(note)) > 500 {
		return nil, errors.New("payout note is too long")
	}
	var payout CreditContributionPayout
	err := DB.Transaction(func(tx *gorm.DB) error {
		var contribution CreditContribution
		if err := lockForUpdate(tx).First(&contribution, contributionId).Error; err != nil {
			return err
		}
		_, _, earned, committed, err := contributionFinancials(tx, contributionId)
		if err != nil {
			return err
		}
		if amount > earned-committed {
			return ErrPayoutExceedsAvailable
		}
		payout = CreditContributionPayout{
			ContributionId: contribution.Id, TenantId: contribution.TenantId, AmountQuota: amount,
			Status: CreditPayoutDraft, Note: strings.TrimSpace(note), CreatedBy: actorUserId,
		}
		if err := tx.Create(&payout).Error; err != nil {
			return err
		}
		return appendContributionEvent(tx, contribution.Id, actorUserId, "payout_created", contribution.Status, contribution.Status, "Payout draft created")
	})
	return &payout, err
}

func UpdateContributionPayout(payoutId, actorUserId int, status, externalReference string) error {
	status = strings.TrimSpace(status)
	if len(strings.TrimSpace(externalReference)) > 160 {
		return errors.New("external reference is too long")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var payout CreditContributionPayout
		if err := lockForUpdate(tx).First(&payout, payoutId).Error; err != nil {
			return err
		}
		from := payout.Status
		now := time.Now().Unix()
		updates := map[string]any{}
		switch status {
		case CreditPayoutApproved:
			if from != CreditPayoutDraft {
				return ErrContributionTransition
			}
			updates["status"], updates["approved_at"] = status, now
		case CreditPayoutPaid:
			if from != CreditPayoutApproved || strings.TrimSpace(externalReference) == "" {
				return errors.New("approved payout and external reference are required")
			}
			updates["status"], updates["paid_at"], updates["external_reference"] = status, now, strings.TrimSpace(externalReference)
		case CreditPayoutVoid:
			if from == CreditPayoutPaid || from == CreditPayoutVoid {
				return ErrContributionTransition
			}
			updates["status"] = status
		default:
			return errors.New("payout status must be approved, paid, or void")
		}
		if err := tx.Model(&payout).Updates(updates).Error; err != nil {
			return err
		}
		var contribution CreditContribution
		if err := tx.First(&contribution, payout.ContributionId).Error; err != nil {
			return err
		}
		return appendContributionEvent(tx, payout.ContributionId, actorUserId, "payout_"+status, contribution.Status, contribution.Status, "Payout status updated to "+status)
	})
}
