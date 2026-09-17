/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api (Copyright (C) 2023-2026
QuantumNous), distributed under the GNU Affero General Public License v3.
See BRANDING.md for the relationship between this fork and its upstream.

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
package model

import (
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// AdjustQuotaByOperator applies an operator's add / subtract / override to a
// login's balance and records it.
//
// UNIFYAPI-BRAND: an operator moving a balance by hand is money arriving with
// no gateway, and until now it left no row anywhere a statement could read --
// about 51M quota of one customer's funding was invisible to every receipts
// view in production. It is recorded in top_ups like a payment, under provider
// "admin", with the raw quota delta in Amount (creditedQuota reads that
// provider as quota units). The row goes in pending, the balance moves, then
// the row is marked -- the lifecycle a gateway payment has -- so a failed
// apply leaves a failed row rather than a phantom credit. Returns the delta
// actually applied.
func AdjustQuotaByOperator(userId int, mode string, value int) (int, error) {
	var delta int
	switch mode {
	case "add":
		delta = value
	case "subtract":
		delta = -value
	case "override":
		current, err := GetUserQuota(userId, true)
		if err != nil {
			return 0, err
		}
		delta = value - current
	default:
		return 0, errors.New("unknown quota adjustment mode")
	}
	if delta == 0 {
		return 0, nil
	}
	now := time.Now().Unix()
	entry := &TopUp{
		UserId: userId, Amount: int64(delta), TradeNo: "grant-" + common.GetUUID(),
		PaymentMethod: PaymentMethodAdmin, PaymentProvider: PaymentProviderAdmin,
		CreateTime: now, Status: common.TopUpStatusPending,
	}
	if err := entry.Insert(); err != nil {
		return 0, err
	}
	var applied error
	switch mode {
	case "add":
		applied = IncreaseUserQuota(userId, value, true)
	case "subtract":
		applied = DecreaseUserQuota(userId, value, true)
	case "override":
		applied = SetUserQuota(userId, value)
	}
	status := common.TopUpStatusSuccess
	if applied != nil {
		status = common.TopUpStatusFailed
	}
	if err := DB.Model(&TopUp{}).Where("id = ?", entry.Id).
		Updates(map[string]any{"status": status, "complete_time": time.Now().Unix()}).Error; err != nil {
		common.SysError("operator adjustment " + entry.TradeNo + " applied but its row could not be marked: " + err.Error())
	}
	if applied != nil {
		return 0, applied
	}
	return delta, nil
}
