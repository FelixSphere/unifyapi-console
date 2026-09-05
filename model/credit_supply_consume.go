/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

// UNIFYAPI-FORK: the credit supply's contact with the relay.
//
// Two lookups run on the hot path and are cached here:
//
//	channel -> live lot        (RecordCreditSupplyConsumption, every consume log)
//	channel -> supplier        (LookupChannelSupplier, every statement row)
//
// Both are small, both change rarely, and both are invalidated by every write
// in credit_lot.go / credit_supplier.go on this instance. Sibling instances
// converge within the TTL; docs/credit-supply.md records the consequence.

import (
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"gorm.io/gorm"
)

const (
	channelLotCacheTTL      = 30 * time.Second
	channelSupplierIndexTTL = 60 * time.Second

	CreditLotEventExhausted = "exhausted"
	CreditLotEventExpired   = "expired"
	CreditLotEventLowWater  = "low_water"
)

// CreditLotEventHook is called after a lot changes state on the consume path.
// The service layer installs a notifier; model stays free of the mail/webhook
// dependency. Called at most once per (lot, event).
var CreditLotEventHook func(lot CreditLot, event string)

type channelLotEntry struct {
	lot       *CreditLot // nil caches "no active lot on this channel"
	fetchedAt time.Time
}

var channelLotCache = struct {
	sync.RWMutex
	entries map[int]channelLotEntry
}{entries: map[int]channelLotEntry{}}

func invalidateChannelLot(channelId int) {
	if channelId == 0 {
		return
	}
	channelLotCache.Lock()
	delete(channelLotCache.entries, channelId)
	channelLotCache.Unlock()
}

func activeLotForChannel(channelId int) (*CreditLot, bool) {
	channelLotCache.RLock()
	entry, ok := channelLotCache.entries[channelId]
	channelLotCache.RUnlock()
	if ok && time.Since(entry.fetchedAt) < channelLotCacheTTL {
		return entry.lot, entry.lot != nil
	}

	var lot CreditLot
	err := DB.Where("channel_id = ? AND status = ?", channelId, CreditLotStatusActive).
		Order("id desc").First(&lot).Error
	entry = channelLotEntry{fetchedAt: time.Now()}
	switch {
	case err == nil:
		entry.lot = &lot
	case err == gorm.ErrRecordNotFound:
	default:
		// A database error must not be cached as "no lot": the next request
		// retries. It also must not fail the request that triggered it.
		common.SysError(fmt.Sprintf("credit supply: lot lookup for channel %d failed: %v", channelId, err))
		return nil, false
	}
	channelLotCache.Lock()
	channelLotCache.entries[channelId] = entry
	channelLotCache.Unlock()
	return entry.lot, entry.lot != nil
}

func creditSupplyDay(timestamp int64) string {
	return time.Unix(timestamp, 0).Format("2006-01-02")
}

// RecordCreditSupplyConsumption draws a lot down by one request. It never returns
// an error because nothing about the customer's request depends on it; a
// failure is logged and the lot is understated until the next request.
func RecordCreditSupplyConsumption(channelId int, modelName string, promptTokens, cachedTokens, completionTokens int) {
	if channelId <= 0 || DB == nil {
		return
	}
	lot, ok := activeLotForChannel(channelId)
	if !ok {
		return
	}
	now := common.GetTimestamp()
	if lot.ExpiresAt != 0 && now >= lot.ExpiresAt {
		retireCreditLot(lot.Id, channelId, CreditLotStatusExpired, now)
		return
	}

	face, priced := ratio_setting.ListPriceUSD(modelName, int64(promptTokens), int64(cachedTokens), int64(completionTokens))
	updates := map[string]interface{}{"updated_at": now}
	if priced {
		updates["consumed_usd"] = gorm.Expr("consumed_usd + ?", face)
	} else {
		face = 0
		updates["unpriced_requests"] = gorm.Expr("unpriced_requests + 1")
	}
	err := DB.Model(&CreditLot{}).
		Where("id = ? AND status = ?", lot.Id, CreditLotStatusActive).
		Updates(updates).Error
	if err != nil {
		common.SysError(fmt.Sprintf("credit supply: draw-down on lot %d failed: %v", lot.Id, err))
		return
	}
	recordCreditLotUsage(lot.Id, creditSupplyDay(now), face)

	var fresh CreditLot
	if err := DB.First(&fresh, "id = ?", lot.Id).Error; err != nil || fresh.Status != CreditLotStatusActive {
		return
	}
	if fresh.ConsumedUSD >= fresh.FaceValueUSD {
		retireCreditLot(fresh.Id, channelId, CreditLotStatusExhausted, now)
		return
	}
	if fresh.LowWaterUSD > 0 && fresh.RemainingUSD() <= fresh.LowWaterUSD && fresh.LowWaterNotifiedAt == 0 {
		result := DB.Model(&CreditLot{}).
			Where("id = ? AND low_water_notified_at = 0", fresh.Id).
			Update("low_water_notified_at", now)
		if result.Error == nil && result.RowsAffected == 1 && CreditLotEventHook != nil {
			fresh.LowWaterNotifiedAt = now
			CreditLotEventHook(fresh, CreditLotEventLowWater)
		}
	}
}

// recordCreditLotUsage adds one request to the lot's daily row. Update-then-
// insert rather than a dialect-specific upsert: this has to run identically on
// SQLite, MySQL and PostgreSQL, and a lost race on the insert is simply
// retried as an update.
func recordCreditLotUsage(lotId int, day string, face float64) {
	for attempt := 0; attempt < 2; attempt++ {
		result := DB.Model(&CreditLotUsage{}).
			Where("lot_id = ? AND day = ?", lotId, day).
			Updates(map[string]interface{}{
				"requests": gorm.Expr("requests + 1"),
				"face_usd": gorm.Expr("face_usd + ?", face),
			})
		if result.Error != nil {
			common.SysError(fmt.Sprintf("credit supply: daily usage update for lot %d failed: %v", lotId, result.Error))
			return
		}
		if result.RowsAffected > 0 {
			return
		}
		err := DB.Create(&CreditLotUsage{LotId: lotId, Day: day, Requests: 1, FaceUSD: face}).Error
		if err == nil {
			return
		}
		// Unique violation from a concurrent first request of the day: loop
		// once more and take the update path.
	}
}

// retireCreditLot ends an active lot and takes its channel out of rotation.
// The conditional UPDATE is the concurrency guard: exactly one caller sees
// RowsAffected == 1 and owns the side effects.
func retireCreditLot(lotId int, channelId int, status string, now int64) {
	result := DB.Model(&CreditLot{}).
		Where("id = ? AND status = ?", lotId, CreditLotStatusActive).
		Updates(map[string]interface{}{"status": status, "retired_at": now, "updated_at": now})
	invalidateChannelLot(channelId)
	invalidateChannelSupplierIndex()
	if result.Error != nil {
		common.SysError(fmt.Sprintf("credit supply: retiring lot %d failed: %v", lotId, result.Error))
		return
	}
	if result.RowsAffected == 0 {
		return
	}
	reason := fmt.Sprintf("credit lot #%d %s", lotId, status)
	UpdateChannelStatus(channelId, "", common.ChannelStatusAutoDisabled, reason)
	common.SysLog(fmt.Sprintf("credit supply: %s; channel %d auto-disabled", reason, channelId))
	if err := appendCreditLotEvent(DB, lotId, "system", "retired", CreditLotStatusActive, status,
		fmt.Sprintf("%s; channel #%d auto-disabled", status, channelId)); err != nil {
		common.SysError(fmt.Sprintf("credit supply: could not record retirement of lot %d: %v", lotId, err))
	}
	if CreditLotEventHook == nil {
		return
	}
	var lot CreditLot
	if err := DB.First(&lot, "id = ?", lotId).Error; err == nil {
		CreditLotEventHook(lot, status)
	}
}

// -- channel -> supplier -----------------------------------------------------

type channelSupplierRef struct {
	Key   string
	Label string
}

var channelSupplierIndex = struct {
	sync.RWMutex
	entries map[int]channelSupplierRef
	builtAt time.Time
}{}

func invalidateChannelSupplierIndex() {
	channelSupplierIndex.Lock()
	channelSupplierIndex.entries = nil
	channelSupplierIndex.Unlock()
}

// LookupChannelSupplier returns the settlement counterparty for a channel that
// is backed by a credit lot. Rejected lots do not count; every other status
// does, because traffic that ran while the lot was live is still owed to the
// supplier after it is exhausted.
func LookupChannelSupplier(channelId int) (key, label string, ok bool) {
	if channelId <= 0 || DB == nil {
		return "", "", false
	}
	channelSupplierIndex.RLock()
	entries := channelSupplierIndex.entries
	fresh := entries != nil && time.Since(channelSupplierIndex.builtAt) < channelSupplierIndexTTL
	channelSupplierIndex.RUnlock()

	if !fresh {
		entries = buildChannelSupplierIndex()
		channelSupplierIndex.Lock()
		channelSupplierIndex.entries = entries
		channelSupplierIndex.builtAt = time.Now()
		channelSupplierIndex.Unlock()
	}
	ref, found := entries[channelId]
	return ref.Key, ref.Label, found
}

func buildChannelSupplierIndex() map[int]channelSupplierRef {
	type row struct {
		ChannelId int
		Code      string
		Name      string
	}
	var rows []row
	err := DB.Table("credit_lots").
		Select("credit_lots.channel_id AS channel_id, credit_suppliers.code AS code, credit_suppliers.name AS name").
		Joins("JOIN credit_suppliers ON credit_suppliers.id = credit_lots.supplier_id").
		Where("credit_lots.channel_id > 0 AND credit_lots.status <> ?", CreditLotStatusRejected).
		Order("credit_lots.id asc").
		Scan(&rows).Error
	entries := map[int]channelSupplierRef{}
	if err != nil {
		// Deployments that predate the pool tables, and tests that never
		// migrated them, must behave exactly as before: no supplier attribution.
		return entries
	}
	for _, r := range rows {
		entries[r.ChannelId] = channelSupplierRef{
			Key:   creditSupplierCounterpartyPrefix + r.Code,
			Label: r.Name,
		}
	}
	return entries
}
