package model

// UNIFYAPI-FORK: keep the previous value of every billing config row.
//
// Written after a real incident. On 2026-08-30 an agent wrote the price-neutral
// seed into `ModelDiscount` over SSM. types.LoadFromJsonString is
// replace-not-merge, so it did not merge with the discounts the operator had
// configured -- it overwrote them, and the value they had spent time entering
// existed nowhere afterwards. The revert six minutes later removed even the
// seed. From the outside the end state was indistinguishable from "no discounts
// were ever set", which is exactly why it went unnoticed: the check everyone ran
// was "are prices back to official", and official is also what an emptied
// discount table produces.
//
// So: before a billing key is overwritten, its old value is copied here. That
// turns "my settings vanished" from an unanswerable question into a row with a
// timestamp, an actor and a restorable payload.
//
// This is not an audit log. An audit log records that something happened; this
// records WHAT IT WAS, because the point is to be able to put it back.
//
// It cannot see a write that bypasses the application -- raw SQL over SSM runs
// no Go at all. SnapshotBillingConfig covers that case on a schedule, so such a
// change is still recoverable after the fact, just not attributable.

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// billingConfigKeys are the options rows that decide what a customer is charged
// or what we believe we pay. Losing one silently is the failure this guards.
//
// GroupRatio and GroupGroupRatio are here for the same reason as ModelDiscount:
// they multiply into the customer's price, and they are edited by hand.
var billingConfigKeys = map[string]bool{
	"ModelDiscount":     true,
	"ExtraModelPricing": true,
	"ChannelCostRatio":  true,
	"GroupRatio":        true,
	"GroupGroupRatio":   true,
}

// IsBillingConfigKey reports whether a key is snapshotted before overwrite.
func IsBillingConfigKey(key string) bool { return billingConfigKeys[key] }

// BillingConfigKeys lists the guarded keys, for the scheduled snapshot and for
// tests that must not drift from the real set.
func BillingConfigKeys() []string {
	keys := make([]string, 0, len(billingConfigKeys))
	for key := range billingConfigKeys {
		keys = append(keys, key)
	}
	return keys
}

// PricingConfigHistory is one previous value of one billing key.
type PricingConfigHistory struct {
	Id  int    `json:"id"`
	Key string `json:"key" gorm:"type:varchar(64);index:idx_pricing_history,priority:1"`

	// OldValue is the payload as it was BEFORE the change -- the restorable
	// part. NewValue is stored too so a reader can see the shape of what
	// replaced it without diffing against a later row.
	OldValue string `json:"old_value" gorm:"type:text"`
	NewValue string `json:"new_value" gorm:"type:text"`

	// ChangedBy is best-effort. A write over SSM or from a migration has no
	// user attached, and recording "unknown" honestly beats attributing it to
	// whoever happens to be in scope.
	ChangedBy string `json:"changed_by" gorm:"type:varchar(191)"`

	// Reason distinguishes an ordinary save from a scheduled snapshot, so the
	// history reads as a story rather than a pile of rows.
	Reason string `json:"reason" gorm:"type:varchar(64)"`

	CreatedAt int64 `json:"created_at" gorm:"bigint;index:idx_pricing_history,priority:2"`
}

// pricingHistoryRetention bounds how many versions are kept per key. Fifty is
// far more than anyone edits a discount table in a year, and small enough that
// the table never needs thinking about.
const pricingHistoryRetention = 50

// RecordPricingConfigChange stores the previous value of a billing key.
//
// Deliberately non-fatal: a failure here must not block the change the operator
// asked for. Losing the safety net is bad; refusing a legitimate pricing edit
// because the safety net is broken is worse, and would be the kind of coupling
// that gets the whole mechanism ripped out later.
func RecordPricingConfigChange(key, oldValue, newValue, changedBy, reason string) {
	if !billingConfigKeys[key] {
		return
	}
	// A no-op save is not history. Without this the table fills with identical
	// rows every time someone opens the page and clicks save.
	if oldValue == newValue {
		return
	}
	if changedBy == "" {
		changedBy = "unknown"
	}

	entry := &PricingConfigHistory{
		Key:       key,
		OldValue:  oldValue,
		NewValue:  newValue,
		ChangedBy: changedBy,
		Reason:    reason,
		CreatedAt: common.GetTimestamp(),
	}
	if err := DB.Create(entry).Error; err != nil {
		common.SysError(fmt.Sprintf(
			"pricing history: failed to snapshot %s before overwrite: %s", key, err.Error()))
		return
	}
	prunePricingConfigHistory(key)
}

// prunePricingConfigHistory keeps the newest pricingHistoryRetention rows.
func prunePricingConfigHistory(key string) {
	var ids []int
	err := DB.Model(&PricingConfigHistory{}).
		Where("key = ?", key).
		Order("created_at desc, id desc").
		Offset(pricingHistoryRetention).
		Limit(1000).
		Pluck("id", &ids).Error
	if err != nil || len(ids) == 0 {
		return
	}
	if err := DB.Where("id IN ?", ids).Delete(&PricingConfigHistory{}).Error; err != nil {
		common.SysError("pricing history: prune failed: " + err.Error())
	}
}

// ListPricingConfigHistory returns recent versions of a key, newest first.
func ListPricingConfigHistory(key string, limit int) ([]*PricingConfigHistory, error) {
	if limit <= 0 || limit > pricingHistoryRetention {
		limit = pricingHistoryRetention
	}
	tx := DB.Model(&PricingConfigHistory{}).Order("created_at desc, id desc").Limit(limit)
	if key != "" {
		if !billingConfigKeys[key] {
			return nil, fmt.Errorf("%s is not a billing config key; guarded keys are %s",
				key, strings.Join(BillingConfigKeys(), ", "))
		}
		tx = tx.Where("key = ?", key)
	}
	var rows []*PricingConfigHistory
	err := tx.Find(&rows).Error
	return rows, err
}

// SnapshotBillingConfig records the current value of every guarded key.
//
// This is the half that survives a write which never touched Go -- raw SQL over
// SSM, a migration, a restored dump. Such a change is invisible to
// RecordPricingConfigChange, so without a periodic snapshot the last known-good
// value would be whatever the app last wrote, possibly weeks earlier.
//
// A snapshot whose value is unchanged from the newest row for that key is
// skipped, so a daily run on a stable configuration costs nothing.
func SnapshotBillingConfig(reason string) (int, error) {
	recorded := 0
	for _, key := range BillingConfigKeys() {
		current, ok := common.OptionMap[key]
		if !ok {
			continue
		}

		var latest PricingConfigHistory
		err := DB.Where("key = ?", key).Order("created_at desc, id desc").First(&latest).Error
		// Compare against NewValue: it is what the configuration became after
		// the most recent recorded change, i.e. the last state we know of.
		if err == nil && latest.NewValue == current {
			continue
		}

		entry := &PricingConfigHistory{
			Key:       key,
			OldValue:  current,
			NewValue:  current,
			ChangedBy: "scheduled-snapshot",
			Reason:    reason,
			CreatedAt: common.GetTimestamp(),
		}
		if err := DB.Create(entry).Error; err != nil {
			return recorded, err
		}
		recorded++
		prunePricingConfigHistory(key)
	}
	return recorded, nil
}
