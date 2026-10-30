/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

// UNIFYAPI-FORK: the data the pricing-change notice needs.
//
// The notice itself lives in service/pricing_change_notify.go. This file
// answers two questions for it from the database: what the last baseline we
// told customers about was, and who has actually been calling a model.

import (
	"github.com/QuantumNous/new-api/common"
)

// PricingBaselineAnnouncedKey is the options row holding the last list-price
// baseline customers were told about, as JSON. It is compared against the
// catalog on every run; a difference is a price change that has gone live
// without anyone being told yet.
const PricingBaselineAnnouncedKey = "PricingBaselineAnnounced"

// PricingChangeNotifyEnabledKey switches the notice off. Defaults to on: a
// price that changes without a word to the customer is the outcome this
// exists to prevent, so it should not depend on someone remembering a switch.
const PricingChangeNotifyEnabledKey = "PricingChangeNotifyEnabled"

// GetAnnouncedPricingBaseline returns the stored baseline JSON, or "" when no
// baseline has ever been recorded.
func GetAnnouncedPricingBaseline() string {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	return common.OptionMap[PricingBaselineAnnouncedKey]
}

// SaveAnnouncedPricingBaseline persists the baseline that has now been
// announced. Goes through UpdateOption so the row is written and the in-memory
// map every instance reads is updated in the same way as any other option.
func SaveAnnouncedPricingBaseline(jsonValue string) error {
	return UpdateOption(PricingBaselineAnnouncedKey, jsonValue)
}

// PricingChangeNotifyEnabled reads the switch; absent means on.
func PricingChangeNotifyEnabled() bool {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	value, ok := common.OptionMap[PricingChangeNotifyEnabledKey]
	if !ok || value == "" {
		return true
	}
	return value == "true"
}

// ModelUsersSince returns, for each user who has a consume log on one of the
// models since the given timestamp, the models they used. Only consume rows
// count: a refund or a management event is not a customer calling a model.
//
// This is the recipient list for a price notice. A customer who has not called
// a model in a month is not told its price moved; the pricing page is there
// for them, and a notice about a model they never use is noise that trains
// them to ignore the one about a model they do.
func ModelUsersSince(models []string, since int64) (map[int][]string, error) {
	if len(models) == 0 {
		return map[int][]string{}, nil
	}
	var rows []struct {
		UserId    int
		ModelName string
	}
	err := LOG_DB.Table("logs").
		Select("user_id, model_name").
		Where("type = ?", LogTypeConsume).
		Where("created_at >= ?", since).
		Where("model_name IN ?", models).
		Group("user_id, model_name").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := map[int][]string{}
	for _, row := range rows {
		out[row.UserId] = append(out[row.UserId], row.ModelName)
	}
	return out, nil
}

// EnabledUsersByIds loads the enabled users among the given ids, with only the
// columns a notification needs.
func EnabledUsersByIds(ids []int) ([]User, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var users []User
	err := DB.Select("id", "username", "email", "role", "status", "setting").
		Where("id IN ?", ids).
		Where("status = ?", common.UserStatusEnabled).
		Find(&users).Error
	return users, err
}

// EnabledAdminUsers lists every enabled admin and root user. Admins are told
// about every change, whether or not they call the model, because they are the
// people a customer will ask.
func EnabledAdminUsers() ([]User, error) {
	var users []User
	err := DB.Select("id", "username", "email", "role", "status", "setting").
		Where("status = ? AND role >= ?", common.UserStatusEnabled, common.RoleAdminUser).
		Find(&users).Error
	return users, err
}
