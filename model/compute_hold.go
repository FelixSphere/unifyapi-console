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
	"math"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// Compute billing bridge, console side. See docs/compute-bridge.md.
//
// A hold is a wallet pre-consume that outlives one HTTP request: the quota
// leaves the billing entity (tenant wallet, or the user's own for an
// untenanted login) and the token when the hold is created, exactly as the
// relay pre-consumes, and whatever has not been settled when the hold closes
// goes back the same way a relay refund does. Settling moves no balance at
// all -- it only records how much of the held amount is now spent, which is
// what the consume log and used_quota report.
//
// The console never prices compute. It moves the integers it is told to move.

const (
	ComputeHoldStatusActive   = "active"
	ComputeHoldStatusSettled  = "settled"
	ComputeHoldStatusReleased = "released"
	ComputeHoldStatusExpired  = "expired"
)

// MaxComputeQuota is the largest amount one hold can carry. Quota columns on
// users, tenants, tokens and logs are 32-bit, so anything above would wrap.
const MaxComputeQuota = math.MaxInt32

var (
	ErrComputeHoldNotFound         = errors.New("hold_not_found")
	ErrComputeHoldExpired          = errors.New("hold_expired")
	ErrComputeHoldClosed           = errors.New("hold_closed")
	ErrComputeHoldExceeded         = errors.New("hold_exceeded")
	ErrComputeHoldConflict         = errors.New("hold_id_conflict")
	ErrComputeEventConflict        = errors.New("event_id_conflict")
	ErrComputeSettlementRegressed  = errors.New("cumulative_quota_regressed")
	ErrComputeTokenQuotaShort      = errors.New("token_quota_insufficient")
	ErrComputeQuotaOutOfRange      = errors.New("quota_out_of_range")
	errComputeHoldLapsed           = errors.New("hold lapsed")
	errComputeSettlementDuplicated = errors.New("settlement duplicated")
)

type ComputeHold struct {
	HoldId        string `json:"hold_id" gorm:"type:varchar(64);primaryKey"`
	UserId        int    `json:"user_id" gorm:"index"`
	TenantId      int    `json:"tenant_id" gorm:"index"`
	TokenId       int    `json:"token_id" gorm:"index"`
	JobId         string `json:"job_id" gorm:"type:varchar(64);index"`
	Sku           string `json:"sku" gorm:"type:varchar(64)"`
	Quota         int    `json:"quota"`
	SettledQuota  int    `json:"settled_quota" gorm:"default:0"`
	ReleasedQuota int    `json:"released_quota" gorm:"default:0"`
	Status        string `json:"status" gorm:"type:varchar(16);index"`
	ExpiresAt     int64  `json:"expires_at" gorm:"bigint;index"`
	CreatedAt     int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt     int64  `json:"updated_at" gorm:"bigint"`
}

// ComputeSettlement makes settle idempotent: the event id is the primary key,
// so a replayed event cannot move quota twice.
type ComputeSettlement struct {
	EventId         string `json:"event_id" gorm:"type:varchar(64);primaryKey"`
	HoldId          string `json:"hold_id" gorm:"type:varchar(64);index"`
	CumulativeQuota int    `json:"cumulative_quota"`
	QuotaDelta      int    `json:"quota_delta"`
	GpuSeconds      int64  `json:"gpu_seconds" gorm:"bigint"`
	Final           bool   `json:"final"`
	CreatedAt       int64  `json:"created_at" gorm:"bigint"`
}

func (h *ComputeHold) Remaining() int { return h.Quota - h.SettledQuota }

func getComputeHold(tx *gorm.DB, holdId string) (*ComputeHold, error) {
	var hold ComputeHold
	err := tx.Where("hold_id = ?", holdId).First(&hold).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrComputeHoldNotFound
	}
	if err != nil {
		return nil, err
	}
	return &hold, nil
}

func GetComputeHold(holdId string) (*ComputeHold, error) {
	return getComputeHold(DB, holdId)
}

func lockComputeHold(tx *gorm.DB, holdId string) (*ComputeHold, error) {
	return getComputeHold(lockForUpdate(tx), holdId)
}

// adjustComputeTokenQuota mirrors how async task billing touches a token after
// the wallet side has committed: through the token's own cache/batch path,
// resolving the key at run time, and only logging a failure because the
// wallet movement it pairs with cannot be rolled back any more.
func adjustComputeTokenQuota(tokenId int, delta int) {
	if tokenId <= 0 || delta == 0 {
		return
	}
	token, err := GetTokenById(tokenId)
	if err != nil {
		common.SysError("compute bridge: token lookup failed: " + err.Error())
		return
	}
	if delta > 0 {
		err = IncreaseTokenQuota(token.Id, token.Key, delta)
	} else {
		err = DecreaseTokenQuota(token.Id, token.Key, -delta)
	}
	if err != nil {
		common.SysError("compute bridge: token quota adjustment failed: " + err.Error())
	}
}

// CreateComputeHold reserves quota for a job. The token is the caller's
// already-validated token row; it is consulted for its remaining allowance the
// same way PreConsumeTokenQuota does, and then decremented through the token
// path even when unlimited, as the relay does, so the token's used_quota keeps
// telling the truth.
//
// A replay of the same hold id returns the stored row and moves nothing.
func CreateComputeHold(hold ComputeHold, token *Token, now int64) (*ComputeHold, bool, error) {
	if hold.Quota <= 0 || hold.Quota > MaxComputeQuota {
		return nil, false, ErrComputeQuotaOutOfRange
	}
	if existing, err := GetComputeHold(hold.HoldId); err == nil {
		return replayComputeHold(existing, hold)
	} else if !errors.Is(err, ErrComputeHoldNotFound) {
		return nil, false, err
	}
	if token == nil || token.Id != hold.TokenId || token.UserId != hold.UserId {
		return nil, false, errors.New("compute hold needs the validated token it draws on")
	}
	if !token.UnlimitedQuota && token.RemainQuota < hold.Quota {
		return nil, false, ErrComputeTokenQuotaShort
	}

	hold.Status = ComputeHoldStatusActive
	hold.SettledQuota = 0
	hold.ReleasedQuota = 0
	hold.CreatedAt = now
	hold.UpdatedAt = now
	var entity BillingEntity
	err := DB.Transaction(func(tx *gorm.DB) error {
		resolved, err := resolveBillingEntity(tx, hold.UserId)
		if err != nil {
			return err
		}
		hold.TenantId = resolved.TenantId
		if err := tx.Create(&hold).Error; err != nil {
			return err
		}
		entity, err = tryDecreaseUserQuotaWithTx(tx, hold.UserId, hold.Quota)
		return err
	})
	if err != nil {
		if isDuplicateKeyError(err) {
			if existing, getErr := GetComputeHold(hold.HoldId); getErr == nil {
				return replayComputeHold(existing, hold)
			}
		}
		return nil, false, err
	}
	_ = invalidateBillingQuotaCache(entity)
	adjustComputeTokenQuota(hold.TokenId, -hold.Quota)
	return &hold, false, nil
}

// replayComputeHold refuses a reused hold id that names a different
// reservation: answering 200 with somebody else's row would tell the caller a
// reservation exists that it never made. Quota is not compared because an
// extend legitimately grows it after creation.
func replayComputeHold(existing *ComputeHold, requested ComputeHold) (*ComputeHold, bool, error) {
	if existing.UserId != requested.UserId || existing.TokenId != requested.TokenId ||
		existing.JobId != requested.JobId || existing.Sku != requested.Sku {
		return nil, false, ErrComputeHoldConflict
	}
	return existing, true, nil
}

type ComputeSettleResult struct {
	Hold          *ComputeHold
	Delta         int
	ReleasedQuota int
	Replayed      bool
}

// SettleComputeHold records that cumulativeQuota of the hold is now spent.
// Only the increase since the previous settlement is new spend. final also
// closes the hold and returns the unspent remainder to wallet and token.
func SettleComputeHold(holdId string, event ComputeSettlement, final bool, now int64) (*ComputeSettleResult, error) {
	if event.CumulativeQuota < 0 || event.CumulativeQuota > MaxComputeQuota || event.GpuSeconds < 0 {
		return nil, ErrComputeQuotaOutOfRange
	}
	if result, err := replayComputeSettlement(holdId, event.EventId); result != nil || err != nil {
		return result, err
	}

	var result ComputeSettleResult
	var entity BillingEntity
	err := DB.Transaction(func(tx *gorm.DB) error {
		hold, err := lockComputeHold(tx, holdId)
		if err != nil {
			return err
		}
		if err := activeComputeHold(hold, now); err != nil {
			return err
		}
		if event.CumulativeQuota > hold.Quota {
			return ErrComputeHoldExceeded
		}
		if event.CumulativeQuota < hold.SettledQuota {
			return ErrComputeSettlementRegressed
		}
		delta := event.CumulativeQuota - hold.SettledQuota
		released := 0
		updates := map[string]any{
			"settled_quota": event.CumulativeQuota,
			"updated_at":    now,
		}
		if final {
			released = hold.Quota - event.CumulativeQuota
			updates["status"] = ComputeHoldStatusSettled
			updates["released_quota"] = released
		}
		if err := casComputeHold(tx, hold, updates); err != nil {
			return err
		}
		event.HoldId = holdId
		event.QuotaDelta = delta
		event.Final = final
		event.CreatedAt = now
		if err := tx.Create(&event).Error; err != nil {
			if isDuplicateKeyError(err) {
				return errComputeSettlementDuplicated
			}
			return err
		}
		if released > 0 {
			if entity, err = adjustBillingQuotaWithTx(tx, hold.UserId, released); err != nil {
				return err
			}
		}
		hold.SettledQuota = event.CumulativeQuota
		hold.UpdatedAt = now
		if final {
			hold.Status = ComputeHoldStatusSettled
			hold.ReleasedQuota = released
		}
		result = ComputeSettleResult{Hold: hold, Delta: delta, ReleasedQuota: released}
		return nil
	})
	if errors.Is(err, errComputeHoldLapsed) {
		return nil, expireLapsedComputeHold(holdId, now)
	}
	if errors.Is(err, errComputeSettlementDuplicated) {
		return replayComputeSettlement(holdId, event.EventId)
	}
	if err != nil {
		return nil, err
	}
	if result.ReleasedQuota > 0 {
		_ = invalidateBillingQuotaCache(entity)
		adjustComputeTokenQuota(result.Hold.TokenId, result.ReleasedQuota)
	}
	return &result, nil
}

// replayComputeSettlement answers an event that was already applied with the
// hold as it stands now. It returns (nil, nil) for an event it has not seen.
func replayComputeSettlement(holdId string, eventId string) (*ComputeSettleResult, error) {
	var seen ComputeSettlement
	err := DB.Where("event_id = ?", eventId).First(&seen).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if seen.HoldId != holdId {
		return nil, ErrComputeEventConflict
	}
	hold, err := GetComputeHold(holdId)
	if err != nil {
		return nil, err
	}
	return &ComputeSettleResult{Hold: hold, Replayed: true}, nil
}

// ExtendComputeHold reserves more quota on an open hold, refused on the same
// terms as creating one.
func ExtendComputeHold(holdId string, additional int, token *Token, now int64) (*ComputeHold, error) {
	if additional <= 0 || additional > MaxComputeQuota {
		return nil, ErrComputeQuotaOutOfRange
	}
	if !token.UnlimitedQuota && token.RemainQuota < additional {
		return nil, ErrComputeTokenQuotaShort
	}
	var extended *ComputeHold
	var entity BillingEntity
	err := DB.Transaction(func(tx *gorm.DB) error {
		hold, err := lockComputeHold(tx, holdId)
		if err != nil {
			return err
		}
		if err := activeComputeHold(hold, now); err != nil {
			return err
		}
		if hold.TokenId != token.Id {
			return ErrComputeHoldConflict
		}
		if int64(hold.Quota)+int64(additional) > MaxComputeQuota {
			return ErrComputeQuotaOutOfRange
		}
		if err := casComputeHold(tx, hold, map[string]any{
			"quota":      hold.Quota + additional,
			"updated_at": now,
		}); err != nil {
			return err
		}
		if entity, err = tryDecreaseUserQuotaWithTx(tx, hold.UserId, additional); err != nil {
			return err
		}
		hold.Quota += additional
		hold.UpdatedAt = now
		extended = hold
		return nil
	})
	if errors.Is(err, errComputeHoldLapsed) {
		return nil, expireLapsedComputeHold(holdId, now)
	}
	if err != nil {
		return nil, err
	}
	_ = invalidateBillingQuotaCache(entity)
	adjustComputeTokenQuota(extended.TokenId, -additional)
	return extended, nil
}

// ReleaseComputeHold returns the unsettled remainder. Releasing a hold that is
// already released, or that a final settlement closed, answers with what was
// returned then and moves nothing.
func ReleaseComputeHold(holdId string, now int64) (*ComputeHold, error) {
	var released *ComputeHold
	var entity BillingEntity
	moved := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		hold, err := lockComputeHold(tx, holdId)
		if err != nil {
			return err
		}
		switch hold.Status {
		case ComputeHoldStatusReleased, ComputeHoldStatusSettled:
			released = hold
			return nil
		}
		if err := activeComputeHold(hold, now); err != nil {
			return err
		}
		remainder := hold.Remaining()
		if err := casComputeHold(tx, hold, map[string]any{
			"status":         ComputeHoldStatusReleased,
			"released_quota": remainder,
			"updated_at":     now,
		}); err != nil {
			return err
		}
		if remainder > 0 {
			if entity, err = adjustBillingQuotaWithTx(tx, hold.UserId, remainder); err != nil {
				return err
			}
			moved = true
		}
		hold.Status = ComputeHoldStatusReleased
		hold.ReleasedQuota = remainder
		hold.UpdatedAt = now
		released = hold
		return nil
	})
	if errors.Is(err, errComputeHoldLapsed) {
		return nil, expireLapsedComputeHold(holdId, now)
	}
	if err != nil {
		return nil, err
	}
	if moved {
		_ = invalidateBillingQuotaCache(entity)
		adjustComputeTokenQuota(released.TokenId, released.ReleasedQuota)
	}
	return released, nil
}

// ExpireComputeHold closes a hold whose expires_at has passed and returns its
// unsettled remainder. It reports whether it did anything, so a sweeper on
// several nodes can race on the same row without refunding twice.
func ExpireComputeHold(holdId string, now int64) (bool, error) {
	var expired *ComputeHold
	var entity BillingEntity
	err := DB.Transaction(func(tx *gorm.DB) error {
		hold, err := lockComputeHold(tx, holdId)
		if err != nil {
			return err
		}
		if hold.Status != ComputeHoldStatusActive || hold.ExpiresAt > now {
			return nil
		}
		remainder := hold.Remaining()
		if err := casComputeHold(tx, hold, map[string]any{
			"status":         ComputeHoldStatusExpired,
			"released_quota": remainder,
			"updated_at":     now,
		}); err != nil {
			return err
		}
		if remainder > 0 {
			if entity, err = adjustBillingQuotaWithTx(tx, hold.UserId, remainder); err != nil {
				return err
			}
		}
		hold.ReleasedQuota = remainder
		expired = hold
		return nil
	})
	if err != nil || expired == nil {
		return false, err
	}
	if expired.ReleasedQuota > 0 {
		_ = invalidateBillingQuotaCache(entity)
		adjustComputeTokenQuota(expired.TokenId, expired.ReleasedQuota)
	}
	return true, nil
}

// SweepExpiredComputeHolds expires up to limit lapsed holds and reports how
// many it closed.
func SweepExpiredComputeHolds(now int64, limit int) (int, error) {
	var holdIds []string
	if err := DB.Model(&ComputeHold{}).
		Where("status = ? AND expires_at <= ?", ComputeHoldStatusActive, now).
		Order("expires_at").Limit(limit).
		Pluck("hold_id", &holdIds).Error; err != nil {
		return 0, err
	}
	closed := 0
	for _, holdId := range holdIds {
		done, err := ExpireComputeHold(holdId, now)
		if err != nil {
			common.SysError("compute bridge: expiring hold " + holdId + " failed: " + err.Error())
			continue
		}
		if done {
			closed++
		}
	}
	return closed, nil
}

func expireLapsedComputeHold(holdId string, now int64) error {
	if _, err := ExpireComputeHold(holdId, now); err != nil {
		return err
	}
	return ErrComputeHoldExpired
}

func activeComputeHold(hold *ComputeHold, now int64) error {
	switch hold.Status {
	case ComputeHoldStatusExpired:
		return ErrComputeHoldExpired
	case ComputeHoldStatusActive:
	default:
		return ErrComputeHoldClosed
	}
	if hold.ExpiresAt <= now {
		return errComputeHoldLapsed
	}
	return nil
}

// casComputeHold updates the row only if nobody moved it since it was read.
// lockForUpdate already serialises this on MySQL and PostgreSQL; SQLite has no
// row lock, so the guard is what stops two writers both applying.
func casComputeHold(tx *gorm.DB, hold *ComputeHold, updates map[string]any) error {
	result := tx.Model(&ComputeHold{}).
		Where("hold_id = ? AND status = ? AND quota = ? AND settled_quota = ?", hold.HoldId, hold.Status, hold.Quota, hold.SettledQuota).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("compute hold changed concurrently")
	}
	return nil
}
