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
	"path"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	CreditPoolStatusEnabled  = 1
	CreditPoolStatusDisabled = 2

	CreditPoolSourceFree        = "free"
	CreditPoolSourcePurchased   = "purchased"
	CreditPoolSourceContributed = "contributed"

	CreditReservationPending  = "pending"
	CreditReservationSettled  = "settled"
	CreditReservationRefunded = "refunded"
)

var (
	ErrCreditPoolUnavailable       = errors.New("credit pool is unavailable")
	ErrCreditGrantInsufficient     = errors.New("promotional credit grant is insufficient")
	ErrCreditPoolInventoryShortage = errors.New("credit pool inventory is insufficient")
	ErrCreditReservationClosed     = errors.New("credit pool reservation is already closed")
)

// CreditPool is a logical provider group. RoutingGroup points at the existing
// channel group used by the distributor, so pool routing does not introduce a
// second channel-selection system.
type CreditPool struct {
	Id           int    `json:"id"`
	Name         string `json:"name" gorm:"type:varchar(120);not null"`
	RoutingGroup string `json:"routing_group" gorm:"type:varchar(64);uniqueIndex;not null"`
	Models       string `json:"models" gorm:"type:text"`
	Status       int    `json:"status" gorm:"type:int;default:1;index"`
	CreatedAt    int64  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt    int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

// CreditPoolLot records where capacity came from.
//
// Ratios are plain float64 columns. They were decimal(10,6)/decimal(12,8)
// until the SQLite migrator proved unable to re-parse that DDL on a restart
// ("invalid DDL, unbalanced brackets"), which killed the process on its
// second start. See TestMigrationIsIdempotentOnSQLite. Quota is provider face-value
// quota, not what a discounted customer would have paid.
type CreditPoolLot struct {
	Id                  int     `json:"id"`
	PoolId              int     `json:"pool_id" gorm:"index;not null"`
	ChannelId           int     `json:"channel_id" gorm:"index;default:0"`
	SourceType          string  `json:"source_type" gorm:"type:varchar(24);index;not null"`
	ContributorTenantId int     `json:"contributor_tenant_id" gorm:"index;default:0"`
	Label               string  `json:"label" gorm:"type:varchar(160)"`
	OriginalQuota       int64   `json:"original_quota" gorm:"not null"`
	RemainingQuota      int64   `json:"remaining_quota" gorm:"index;not null"`
	AcquisitionRatio    float64 `json:"acquisition_ratio" gorm:"default:0"`
	ExpiresAt           int64   `json:"expires_at" gorm:"index;default:0"`
	Status              int     `json:"status" gorm:"type:int;default:1;index"`
	CreatedAt           int64   `json:"created_at" gorm:"autoCreateTime"`
	AccruedPayableQuota float64 `json:"accrued_payable_quota" gorm:"-"`
}

// TenantCreditGrant is the promotional balance promised to one customer.
// It stays separate from tenants.quota so a free grant can never be invoiced
// or mistaken for cash the customer deposited.
type TenantCreditGrant struct {
	Id             int    `json:"id"`
	TenantId       int    `json:"tenant_id" gorm:"index;not null"`
	PoolId         int    `json:"pool_id" gorm:"index;not null"`
	Name           string `json:"name" gorm:"type:varchar(160)"`
	OriginalQuota  int64  `json:"original_quota" gorm:"not null"`
	RemainingQuota int64  `json:"remaining_quota" gorm:"index;not null"`
	StartsAt       int64  `json:"starts_at" gorm:"index;default:0"`
	ExpiresAt      int64  `json:"expires_at" gorm:"index;default:0"`
	Priority       int    `json:"priority" gorm:"default:0"`
	Status         int    `json:"status" gorm:"type:int;default:1;index"`
	CreatedAt      int64  `json:"created_at" gorm:"autoCreateTime"`
}

// CreditPoolReservation is the idempotency boundary for one relay request.
// Customer quota and pool quota are deliberately separate meters.
type CreditPoolReservation struct {
	Id               int     `json:"id"`
	RequestId        string  `json:"request_id" gorm:"type:varchar(64);uniqueIndex;not null"`
	GrantId          int     `json:"grant_id" gorm:"index;not null"`
	TenantId         int     `json:"tenant_id" gorm:"index;not null"`
	UserId           int     `json:"user_id" gorm:"index;not null"`
	PoolId           int     `json:"pool_id" gorm:"index;not null"`
	ChannelId        int     `json:"channel_id" gorm:"index;default:0"`
	CustomerQuota    int64   `json:"customer_quota" gorm:"not null"`
	PoolQuota        int64   `json:"pool_quota" gorm:"not null"`
	CustomerRatio    float64 `json:"customer_ratio" gorm:"not null"`
	ChannelCostRatio float64 `json:"channel_cost_ratio" gorm:"not null"`
	Status           string  `json:"status" gorm:"type:varchar(16);index;not null"`
	CreatedAt        int64   `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt        int64   `json:"updated_at" gorm:"autoUpdateTime"`
}

type CreditPoolReservationLot struct {
	Id            int   `json:"id"`
	ReservationId int   `json:"reservation_id" gorm:"uniqueIndex:idx_credit_reservation_lot;index;not null"`
	LotId         int   `json:"lot_id" gorm:"uniqueIndex:idx_credit_reservation_lot;index;not null"`
	Quota         int64 `json:"quota" gorm:"not null"`
}

type CreditPoolRoute struct {
	PoolId              int    `json:"pool_id"`
	GrantId             int    `json:"grant_id"`
	RoutingGroup        string `json:"routing_group"`
	GrantRemainingQuota int64  `json:"grant_remaining_quota"`
	PoolRemainingQuota  int64  `json:"pool_remaining_quota"`
}

type CreditPoolSummary struct {
	CreditPool
	OriginalQuota       int64   `json:"original_quota"`
	RemainingQuota      int64   `json:"remaining_quota"`
	GrantedQuota        int64   `json:"granted_quota"`
	GrantRemainingQuota int64   `json:"grant_remaining_quota"`
	AccruedPayableQuota float64 `json:"accrued_payable_quota"`
	Lots                int64   `json:"lots"`
	Grants              int64   `json:"grants"`
}

func normalizeCreditSource(source string) (string, error) {
	source = strings.ToLower(strings.TrimSpace(source))
	switch source {
	case CreditPoolSourceFree, CreditPoolSourcePurchased, CreditPoolSourceContributed:
		return source, nil
	default:
		return "", fmt.Errorf("unsupported credit source %q", source)
	}
}

func CreateCreditPool(pool *CreditPool) error {
	pool.Name = strings.TrimSpace(pool.Name)
	pool.RoutingGroup = strings.TrimSpace(pool.RoutingGroup)
	pool.Models = strings.TrimSpace(pool.Models)
	if pool.Name == "" || pool.RoutingGroup == "" {
		return errors.New("pool name and routing group are required")
	}
	if pool.Status == 0 {
		pool.Status = CreditPoolStatusEnabled
	}
	return DB.Create(pool).Error
}

func AddCreditPoolLot(lot *CreditPoolLot) error {
	return addCreditPoolLot(DB, lot)
}

func addCreditPoolLot(tx *gorm.DB, lot *CreditPoolLot) error {
	if lot.PoolId <= 0 || lot.OriginalQuota <= 0 {
		return errors.New("pool and positive quota are required")
	}
	source, err := normalizeCreditSource(lot.SourceType)
	if err != nil {
		return err
	}
	lot.SourceType = source
	var pool CreditPool
	if err := tx.Where("id = ? AND status = ?", lot.PoolId, CreditPoolStatusEnabled).First(&pool).Error; err != nil {
		return errors.New("active credit pool not found")
	}
	if source == CreditPoolSourceContributed && lot.ContributorTenantId <= 0 {
		return errors.New("contributed credits require a contributor tenant")
	}
	if source != CreditPoolSourceContributed && lot.ContributorTenantId != 0 {
		return errors.New("only contributed credits may name a contributor tenant")
	}
	if lot.AcquisitionRatio < 0 || lot.AcquisitionRatio > 1 {
		return errors.New("acquisition ratio must be between 0 and 1")
	}
	if source == CreditPoolSourceFree && lot.AcquisitionRatio != 0 {
		return errors.New("free credits must have a zero acquisition ratio")
	}
	if source != CreditPoolSourceFree && lot.AcquisitionRatio == 0 {
		return errors.New("purchased and contributed credits require a positive acquisition ratio")
	}
	if lot.ChannelId > 0 {
		var count int64
		if err := tx.Model(&Channel{}).Where("id = ?", lot.ChannelId).Count(&count).Error; err != nil || count != 1 {
			return errors.New("channel not found")
		}
	}
	if lot.ContributorTenantId > 0 {
		var count int64
		if err := tx.Model(&Tenant{}).Where("id = ?", lot.ContributorTenantId).Count(&count).Error; err != nil || count != 1 {
			return errors.New("contributor tenant not found")
		}
	}
	if lot.RemainingQuota == 0 {
		lot.RemainingQuota = lot.OriginalQuota
	}
	if lot.RemainingQuota < 0 || lot.RemainingQuota > lot.OriginalQuota {
		return errors.New("remaining quota must be between zero and original quota")
	}
	if lot.Status == 0 {
		lot.Status = CreditPoolStatusEnabled
	}
	return tx.Create(lot).Error
}

func CreateTenantCreditGrant(grant *TenantCreditGrant) error {
	if grant.TenantId <= 0 || grant.PoolId <= 0 || grant.OriginalQuota <= 0 {
		return errors.New("tenant, pool, and positive quota are required")
	}
	if grant.RemainingQuota == 0 {
		grant.RemainingQuota = grant.OriginalQuota
	}
	if grant.RemainingQuota < 0 || grant.RemainingQuota > grant.OriginalQuota {
		return errors.New("remaining quota must be between zero and original quota")
	}
	if grant.ExpiresAt > 0 && grant.StartsAt > grant.ExpiresAt {
		return errors.New("grant start must be before expiry")
	}
	var poolCount, tenantCount int64
	if err := DB.Model(&CreditPool{}).Where("id = ? AND status = ?", grant.PoolId, CreditPoolStatusEnabled).Count(&poolCount).Error; err != nil || poolCount != 1 {
		return errors.New("active credit pool not found")
	}
	if err := DB.Model(&Tenant{}).Where("id = ?", grant.TenantId).Count(&tenantCount).Error; err != nil || tenantCount != 1 {
		return errors.New("tenant not found")
	}
	if grant.Status == 0 {
		grant.Status = CreditPoolStatusEnabled
	}
	return DB.Create(grant).Error
}

func creditPoolMatchesModel(patterns, modelName string) bool {
	patterns = strings.TrimSpace(patterns)
	if patterns == "" || patterns == "*" {
		return true
	}
	for _, pattern := range strings.Split(patterns, ",") {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if matched, err := path.Match(pattern, modelName); err == nil && matched {
			return true
		}
	}
	return false
}

func activeCreditLotQuery(tx *gorm.DB, poolId, channelId int, now int64) *gorm.DB {
	query := tx.Model(&CreditPoolLot{}).
		Where("pool_id = ? AND status = ? AND remaining_quota > 0", poolId, CreditPoolStatusEnabled).
		Where("expires_at = 0 OR expires_at > ?", now)
	if channelId > 0 {
		query = query.Where("channel_id = 0 OR channel_id = ?", channelId)
	}
	return query
}

func creditPoolRemaining(tx *gorm.DB, poolId, channelId int, now int64) (int64, error) {
	var remaining int64
	err := activeCreditLotQuery(tx, poolId, channelId, now).Select("COALESCE(SUM(remaining_quota), 0)").Scan(&remaining).Error
	return remaining, err
}

func ResolveCreditPoolRoute(userId int, modelName string) (*CreditPoolRoute, error) {
	entity, err := ResolveBillingEntity(userId)
	if err != nil || !entity.IsTenant() {
		return nil, err
	}
	now := time.Now().Unix()
	var grants []TenantCreditGrant
	err = DB.Where("tenant_id = ? AND status = ? AND remaining_quota > 0", entity.TenantId, CreditPoolStatusEnabled).
		Where("starts_at = 0 OR starts_at <= ?", now).
		Where("expires_at = 0 OR expires_at > ?", now).
		Order("priority desc, CASE WHEN expires_at = 0 THEN 1 ELSE 0 END, expires_at asc, id asc").
		Find(&grants).Error
	if err != nil {
		return nil, err
	}
	for _, grant := range grants {
		var pool CreditPool
		if err := DB.Where("id = ? AND status = ?", grant.PoolId, CreditPoolStatusEnabled).First(&pool).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return nil, err
		}
		if !creditPoolMatchesModel(pool.Models, modelName) {
			continue
		}
		remaining, err := creditPoolRemaining(DB, pool.Id, 0, now)
		if err != nil {
			return nil, err
		}
		if remaining > 0 {
			return &CreditPoolRoute{PoolId: pool.Id, GrantId: grant.Id, RoutingGroup: pool.RoutingGroup, GrantRemainingQuota: grant.RemainingQuota, PoolRemainingQuota: remaining}, nil
		}
	}
	return nil, nil
}

func CreditPoolChannelHasInventory(poolId, channelId int) (bool, error) {
	remaining, err := creditPoolRemaining(DB, poolId, channelId, time.Now().Unix())
	return remaining > 0, err
}

func CreditPoolCostQuota(customerQuota int64, customerRatio, channelCostRatio float64) int64 {
	if customerQuota <= 0 {
		return 0
	}
	if customerRatio <= 0 || math.IsNaN(customerRatio) || math.IsInf(customerRatio, 0) {
		customerRatio = 1
	}
	if channelCostRatio <= 0 || math.IsNaN(channelCostRatio) || math.IsInf(channelCostRatio, 0) {
		channelCostRatio = 1
	}
	quota := int64(math.Round(float64(customerQuota) / customerRatio * channelCostRatio))
	if quota < 1 {
		return 1
	}
	return quota
}

func allocateCreditLots(tx *gorm.DB, reservationId, poolId, channelId int, amount int64, allowOverdraft bool) error {
	if amount <= 0 {
		return nil
	}
	now := time.Now().Unix()
	var lots []CreditPoolLot
	if err := lockForUpdate(activeCreditLotQuery(tx, poolId, channelId, now)).
		Order(clause.Expr{SQL: "CASE WHEN channel_id = ? THEN 0 ELSE 1 END", Vars: []any{channelId}}).
		Order("CASE WHEN expires_at = 0 THEN 1 ELSE 0 END, expires_at asc, acquisition_ratio asc, id asc").
		Find(&lots).Error; err != nil {
		return err
	}
	remaining := amount
	for i := range lots {
		if remaining <= 0 {
			break
		}
		take := lots[i].RemainingQuota
		if take > remaining {
			take = remaining
		}
		if err := tx.Model(&CreditPoolLot{}).Where("id = ?", lots[i].Id).
			Update("remaining_quota", gorm.Expr("remaining_quota - ?", take)).Error; err != nil {
			return err
		}
		result := tx.Model(&CreditPoolReservationLot{}).
			Where("reservation_id = ? AND lot_id = ?", reservationId, lots[i].Id).
			Update("quota", gorm.Expr("quota + ?", take))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			allocation := CreditPoolReservationLot{ReservationId: reservationId, LotId: lots[i].Id, Quota: take}
			if err := tx.Create(&allocation).Error; err != nil {
				return err
			}
		}
		remaining -= take
	}
	if remaining <= 0 {
		return nil
	}
	if !allowOverdraft {
		return ErrCreditPoolInventoryShortage
	}
	// The request has already succeeded upstream. Attribute unavoidable
	// estimate overrun to the last eligible or previously allocated lot instead
	// of charging cash. The latter matters when the estimate exhausted the lot.
	var last CreditPoolLot
	if len(lots) > 0 {
		last = lots[len(lots)-1]
	} else {
		if err := tx.Table("credit_pool_lots AS l").
			Select("l.*").
			Joins("JOIN credit_pool_reservation_lots a ON a.lot_id = l.id").
			Where("a.reservation_id = ?", reservationId).
			Order("a.id desc").First(&last).Error; err != nil {
			return ErrCreditPoolInventoryShortage
		}
	}
	if err := tx.Model(&CreditPoolLot{}).Where("id = ?", last.Id).
		Update("remaining_quota", gorm.Expr("remaining_quota - ?", remaining)).Error; err != nil {
		return err
	}
	return tx.Model(&CreditPoolReservationLot{}).
		Where("reservation_id = ? AND lot_id = ?", reservationId, last.Id).
		Update("quota", gorm.Expr("quota + ?", remaining)).Error
}

func restoreCreditAllocations(tx *gorm.DB, reservationId int) error {
	var allocations []CreditPoolReservationLot
	if err := lockForUpdate(tx.Where("reservation_id = ?", reservationId)).Find(&allocations).Error; err != nil {
		return err
	}
	for _, allocation := range allocations {
		if err := tx.Model(&CreditPoolLot{}).Where("id = ?", allocation.LotId).
			Update("remaining_quota", gorm.Expr("remaining_quota + ?", allocation.Quota)).Error; err != nil {
			return err
		}
	}
	return tx.Where("reservation_id = ?", reservationId).Delete(&CreditPoolReservationLot{}).Error
}

func reduceCreditAllocations(tx *gorm.DB, reservationId int, amount int64) error {
	if amount <= 0 {
		return nil
	}
	var allocations []CreditPoolReservationLot
	if err := lockForUpdate(tx.Where("reservation_id = ?", reservationId).Order("id desc")).Find(&allocations).Error; err != nil {
		return err
	}
	remaining := amount
	for _, allocation := range allocations {
		if remaining <= 0 {
			break
		}
		giveBack := allocation.Quota
		if giveBack > remaining {
			giveBack = remaining
		}
		if err := tx.Model(&CreditPoolLot{}).Where("id = ?", allocation.LotId).
			Update("remaining_quota", gorm.Expr("remaining_quota + ?", giveBack)).Error; err != nil {
			return err
		}
		if giveBack == allocation.Quota {
			if err := tx.Delete(&CreditPoolReservationLot{}, allocation.Id).Error; err != nil {
				return err
			}
		} else if err := tx.Model(&CreditPoolReservationLot{}).Where("id = ?", allocation.Id).
			Update("quota", gorm.Expr("quota - ?", giveBack)).Error; err != nil {
			return err
		}
		remaining -= giveBack
	}
	if remaining > 0 {
		return errors.New("credit allocation refund exceeds reservation")
	}
	return nil
}

func ReserveCreditPool(requestId string, userId, grantId, poolId, channelId int, customerQuota, poolQuota int64, customerRatio, costRatio float64) (*CreditPoolReservation, error) {
	if strings.TrimSpace(requestId) == "" || customerQuota <= 0 || poolQuota <= 0 {
		return nil, errors.New("request id and positive quota are required")
	}
	var reservation CreditPoolReservation
	err := DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Where("request_id = ?", requestId).Find(&reservation)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 1 {
			if reservation.Status == CreditReservationRefunded {
				return ErrCreditReservationClosed
			}
			if reservation.UserId != userId || reservation.GrantId != grantId || reservation.PoolId != poolId ||
				reservation.ChannelId != channelId || reservation.CustomerQuota != customerQuota || reservation.PoolQuota != poolQuota ||
				math.Abs(reservation.CustomerRatio-customerRatio) > 0.00000001 || math.Abs(reservation.ChannelCostRatio-costRatio) > 0.00000001 {
				return errors.New("request id is already used by a different credit reservation")
			}
			return nil
		}
		entity, err := resolveBillingEntity(tx, userId)
		if err != nil {
			return err
		}
		if !entity.IsTenant() {
			return ErrCreditPoolUnavailable
		}
		now := time.Now().Unix()
		var grant TenantCreditGrant
		if err := lockForUpdate(tx).Where("id = ? AND tenant_id = ? AND pool_id = ? AND status = ?", grantId, entity.TenantId, poolId, CreditPoolStatusEnabled).
			Where("(starts_at = 0 OR starts_at <= ?) AND (expires_at = 0 OR expires_at > ?)", now, now).
			First(&grant).Error; err != nil {
			return ErrCreditPoolUnavailable
		}
		updateResult := tx.Model(&TenantCreditGrant{}).Where("id = ? AND remaining_quota >= ?", grant.Id, customerQuota).
			Update("remaining_quota", gorm.Expr("remaining_quota - ?", customerQuota))
		if updateResult.Error != nil {
			return updateResult.Error
		}
		if updateResult.RowsAffected != 1 {
			return ErrCreditGrantInsufficient
		}
		reservation = CreditPoolReservation{
			RequestId: requestId, GrantId: grant.Id, TenantId: entity.TenantId, UserId: userId,
			PoolId: poolId, ChannelId: channelId, CustomerQuota: customerQuota, PoolQuota: poolQuota,
			CustomerRatio: customerRatio, ChannelCostRatio: costRatio, Status: CreditReservationPending,
		}
		if err := tx.Create(&reservation).Error; err != nil {
			return err
		}
		return allocateCreditLots(tx, reservation.Id, poolId, channelId, poolQuota, false)
	})
	return &reservation, err
}

func settleCreditPoolReservation(tx *gorm.DB, reservation *CreditPoolReservation, channelId int, customerQuota, poolQuota int64, channelCostRatio float64) error {
	if reservation.Status == CreditReservationRefunded {
		return ErrCreditReservationClosed
	}
	if channelId <= 0 {
		channelId = reservation.ChannelId
	}
	if channelCostRatio <= 0 || math.IsNaN(channelCostRatio) || math.IsInf(channelCostRatio, 0) {
		channelCostRatio = reservation.ChannelCostRatio
	}
	if channelId != reservation.ChannelId {
		if err := restoreCreditAllocations(tx, reservation.Id); err != nil {
			return err
		}
		if err := allocateCreditLots(tx, reservation.Id, reservation.PoolId, channelId, poolQuota, true); err != nil {
			return err
		}
	} else {
		deltaPool := poolQuota - reservation.PoolQuota
		if deltaPool > 0 {
			if err := allocateCreditLots(tx, reservation.Id, reservation.PoolId, channelId, deltaPool, true); err != nil {
				return err
			}
		} else if deltaPool < 0 {
			if err := reduceCreditAllocations(tx, reservation.Id, -deltaPool); err != nil {
				return err
			}
		}
	}
	deltaCustomer := customerQuota - reservation.CustomerQuota
	if deltaCustomer != 0 {
		if err := tx.Model(&TenantCreditGrant{}).Where("id = ?", reservation.GrantId).
			Update("remaining_quota", gorm.Expr("remaining_quota - ?", deltaCustomer)).Error; err != nil {
			return err
		}
	}
	return tx.Model(reservation).Updates(map[string]any{
		"channel_id": channelId, "customer_quota": customerQuota, "pool_quota": poolQuota,
		"channel_cost_ratio": channelCostRatio, "status": CreditReservationSettled,
	}).Error
}

// ResizePendingCreditPoolReservation changes an estimate before the upstream
// request is complete. Unlike settlement, growth still requires available
// grant and inventory and the reservation remains pending.
func ResizePendingCreditPoolReservation(reservationId int, customerQuota, poolQuota int64) error {
	if customerQuota <= 0 || poolQuota <= 0 {
		return errors.New("pending credit reservation requires positive quota")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var reservation CreditPoolReservation
		if err := lockForUpdate(tx).First(&reservation, reservationId).Error; err != nil {
			return err
		}
		if reservation.Status != CreditReservationPending {
			return ErrCreditReservationClosed
		}
		deltaCustomer := customerQuota - reservation.CustomerQuota
		if deltaCustomer > 0 {
			result := tx.Model(&TenantCreditGrant{}).
				Where("id = ? AND remaining_quota >= ?", reservation.GrantId, deltaCustomer).
				Update("remaining_quota", gorm.Expr("remaining_quota - ?", deltaCustomer))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrCreditGrantInsufficient
			}
		} else if deltaCustomer < 0 {
			if err := tx.Model(&TenantCreditGrant{}).Where("id = ?", reservation.GrantId).
				Update("remaining_quota", gorm.Expr("remaining_quota + ?", -deltaCustomer)).Error; err != nil {
				return err
			}
		}
		deltaPool := poolQuota - reservation.PoolQuota
		if deltaPool > 0 {
			if err := allocateCreditLots(tx, reservation.Id, reservation.PoolId, reservation.ChannelId, deltaPool, false); err != nil {
				return err
			}
		} else if deltaPool < 0 {
			if err := reduceCreditAllocations(tx, reservation.Id, -deltaPool); err != nil {
				return err
			}
		}
		return tx.Model(&reservation).Updates(map[string]any{
			"customer_quota": customerQuota,
			"pool_quota":     poolQuota,
		}).Error
	})
}

func SettleCreditPoolReservation(reservationId, channelId int, customerQuota, poolQuota int64) error {
	return SettleCreditPoolReservationAtCost(reservationId, channelId, customerQuota, poolQuota, 0)
}

// SettleCreditPoolReservationAtCost records the successful channel's cost
// ratio. Retries may finish on a different channel than the estimate used.
func SettleCreditPoolReservationAtCost(reservationId, channelId int, customerQuota, poolQuota int64, channelCostRatio float64) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var reservation CreditPoolReservation
		if err := lockForUpdate(tx).First(&reservation, reservationId).Error; err != nil {
			return err
		}
		if channelId <= 0 {
			channelId = reservation.ChannelId
		}
		if reservation.Status == CreditReservationSettled {
			if reservation.CustomerQuota == customerQuota && reservation.PoolQuota == poolQuota && reservation.ChannelId == channelId {
				return nil
			}
			return ErrCreditReservationClosed
		}
		return settleCreditPoolReservation(tx, &reservation, channelId, customerQuota, poolQuota, channelCostRatio)
	})
}

func RefundCreditPoolReservation(reservationId int) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var reservation CreditPoolReservation
		if err := lockForUpdate(tx).First(&reservation, reservationId).Error; err != nil {
			return err
		}
		if reservation.Status == CreditReservationRefunded {
			return nil
		}
		if err := tx.Model(&TenantCreditGrant{}).Where("id = ?", reservation.GrantId).
			Update("remaining_quota", gorm.Expr("remaining_quota + ?", reservation.CustomerQuota)).Error; err != nil {
			return err
		}
		if err := restoreCreditAllocations(tx, reservation.Id); err != nil {
			return err
		}
		return tx.Model(&reservation).Update("status", CreditReservationRefunded).Error
	})
}

func AdjustSettledCreditPoolReservation(reservationId int, deltaCustomer int64) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var reservation CreditPoolReservation
		if err := lockForUpdate(tx).First(&reservation, reservationId).Error; err != nil {
			return err
		}
		if reservation.Status != CreditReservationSettled {
			return ErrCreditReservationClosed
		}
		targetCustomer := reservation.CustomerQuota + deltaCustomer
		if targetCustomer < 0 {
			return errors.New("credit pool adjustment exceeds settled quota")
		}
		targetPool := CreditPoolCostQuota(targetCustomer, reservation.CustomerRatio, reservation.ChannelCostRatio)
		return settleCreditPoolReservation(tx, &reservation, reservation.ChannelId, targetCustomer, targetPool, reservation.ChannelCostRatio)
	})
}

func ListCreditPools() ([]CreditPoolSummary, error) {
	var pools []CreditPool
	if err := DB.Order("id desc").Find(&pools).Error; err != nil {
		return nil, err
	}
	out := make([]CreditPoolSummary, 0, len(pools))
	now := time.Now().Unix()
	for _, pool := range pools {
		summary := CreditPoolSummary{CreditPool: pool}
		if err := DB.Model(&CreditPoolLot{}).Where("pool_id = ?", pool.Id).
			Select("COALESCE(SUM(original_quota),0) AS original_quota, COUNT(*) AS lots").Scan(&summary).Error; err != nil {
			return nil, err
		}
		if err := activeCreditLotQuery(DB, pool.Id, 0, now).
			Select("COALESCE(SUM(remaining_quota),0)").Scan(&summary.RemainingQuota).Error; err != nil {
			return nil, err
		}
		if err := DB.Model(&TenantCreditGrant{}).Where("pool_id = ?", pool.Id).
			Select("COALESCE(SUM(original_quota),0) AS granted_quota, COUNT(*) AS grants").Scan(&summary).Error; err != nil {
			return nil, err
		}
		if err := DB.Model(&TenantCreditGrant{}).
			Where("pool_id = ? AND status = ? AND remaining_quota > 0", pool.Id, CreditPoolStatusEnabled).
			Where("starts_at = 0 OR starts_at <= ?", now).
			Where("expires_at = 0 OR expires_at > ?", now).
			Select("COALESCE(SUM(remaining_quota),0)").Scan(&summary.GrantRemainingQuota).Error; err != nil {
			return nil, err
		}
		var payable float64
		err := DB.Table("credit_pool_reservation_lots AS a").
			Select("COALESCE(SUM(a.quota * l.acquisition_ratio),0)").
			Joins("JOIN credit_pool_lots l ON l.id = a.lot_id").
			Joins("JOIN credit_pool_reservations r ON r.id = a.reservation_id").
			Where("r.pool_id = ? AND r.status = ? AND l.source_type = ?", pool.Id, CreditReservationSettled, CreditPoolSourceContributed).
			Scan(&payable).Error
		if err != nil {
			return nil, err
		}
		summary.AccruedPayableQuota = payable
		out = append(out, summary)
	}
	return out, nil
}

func GetTenantCreditGrants(tenantId int) ([]TenantCreditGrant, error) {
	var grants []TenantCreditGrant
	err := DB.Where("tenant_id = ?", tenantId).
		Order("status asc, priority desc, CASE WHEN expires_at = 0 THEN 1 ELSE 0 END, expires_at asc, id asc").Find(&grants).Error
	return grants, err
}

func GetCreditPoolLots(poolId int) ([]CreditPoolLot, error) {
	var lots []CreditPoolLot
	err := DB.Where("pool_id = ?", poolId).
		Order("status asc, CASE WHEN expires_at = 0 THEN 1 ELSE 0 END, expires_at asc, id asc").Find(&lots).Error
	if err != nil {
		return nil, err
	}
	for i := range lots {
		if lots[i].SourceType != CreditPoolSourceContributed {
			continue
		}
		err = DB.Table("credit_pool_reservation_lots AS a").
			Select("COALESCE(SUM(a.quota * ?),0)", lots[i].AcquisitionRatio).
			Joins("JOIN credit_pool_reservations r ON r.id = a.reservation_id").
			Where("a.lot_id = ? AND r.status = ?", lots[i].Id, CreditReservationSettled).
			Scan(&lots[i].AccruedPayableQuota).Error
		if err != nil {
			return nil, err
		}
	}
	return lots, err
}

func GetCreditPoolGrants(poolId int) ([]TenantCreditGrant, error) {
	var grants []TenantCreditGrant
	err := DB.Where("pool_id = ?", poolId).
		Order("status asc, priority desc, CASE WHEN expires_at = 0 THEN 1 ELSE 0 END, expires_at asc, id asc").Find(&grants).Error
	return grants, err
}

func GetUserCreditGrants(userId int) ([]TenantCreditGrant, error) {
	entity, err := ResolveBillingEntity(userId)
	if err != nil {
		return nil, err
	}
	if !entity.IsTenant() {
		return []TenantCreditGrant{}, nil
	}
	now := time.Now().Unix()
	var grants []TenantCreditGrant
	err = DB.Where("tenant_id = ? AND status = ? AND remaining_quota > 0", entity.TenantId, CreditPoolStatusEnabled).
		Where("starts_at = 0 OR starts_at <= ?", now).
		Where("expires_at = 0 OR expires_at > ?", now).
		Order("priority desc, CASE WHEN expires_at = 0 THEN 1 ELSE 0 END, expires_at asc, id asc").
		Find(&grants).Error
	return grants, err
}
