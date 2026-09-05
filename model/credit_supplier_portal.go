/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

// UNIFYAPI-FORK: the supplier's own view of the credit pool. Everything here is
// scoped to one supplier; the controller resolves which one from the login.

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
)

var ErrCreditSupplierSuspended = errors.New("this supplier account is suspended; contact the operator")

// SubmitSupplierCreditLot records a supplier's own submission: a new channel
// carrying their upstream key, born disabled, and a pending lot bound to it.
// The channel is inserted first because the lot needs its id; if the lot is
// refused the channel is removed again so a rejected submission leaves no
// orphan key behind.
func SubmitSupplierCreditLot(supplier *CreditSupplier, channel *Channel, lot *CreditLot, actor string) error {
	if supplier.Status != CreditSupplierStatusActive {
		return ErrCreditSupplierSuspended
	}
	channel.Status = common.ChannelStatusManuallyDisabled
	tag := supplier.CounterpartyKey()
	channel.Tag = &tag
	if channel.CreatedTime == 0 {
		channel.CreatedTime = common.GetTimestamp()
	}
	info := channel.GetOtherInfo()
	info["status_reason"] = fmt.Sprintf("submitted by supplier %s; awaiting approval", supplier.Code)
	info["status_time"] = common.GetTimestamp()
	channel.SetOtherInfo(info)
	if err := channel.Insert(); err != nil {
		return err
	}

	lot.SupplierId = supplier.Id
	lot.ChannelId = channel.Id
	lot.Status = CreditLotStatusPending
	lot.Source = CreditLotSourceSupplier
	// The supplier's own attestation, made in the portal moments ago.
	lot.AttestationVersion = CreditLotAttestationVersion
	lot.AttestedAt = common.GetTimestamp()
	lot.AttestedBy = actor
	if err := CreateCreditLot(lot, actor); err != nil {
		if cleanup := channel.Delete(); cleanup != nil {
			common.SysError(fmt.Sprintf("credit pool: could not remove channel %d after a refused submission: %v", channel.Id, cleanup))
		}
		return err
	}
	return nil
}

// SupplierDailyUsage is one day's draw-down across all of a supplier's lots.
type SupplierDailyUsage struct {
	Day      string  `json:"day"`
	Requests int64   `json:"requests"`
	FaceUSD  float64 `json:"face_usd"`
}

func GetSupplierDailyUsage(supplierId int, days int) ([]SupplierDailyUsage, error) {
	if days <= 0 {
		days = 30
	}
	sinceDay := creditSupplyDay(common.GetTimestamp() - int64(days)*86400)
	var rows []SupplierDailyUsage
	err := DB.Table("credit_lot_usages").
		Select("credit_lot_usages.day AS day, SUM(credit_lot_usages.requests) AS requests, SUM(credit_lot_usages.face_usd) AS face_usd").
		Joins("JOIN credit_lots ON credit_lots.id = credit_lot_usages.lot_id").
		Where("credit_lots.supplier_id = ? AND credit_lot_usages.day >= ?", supplierId, sinceDay).
		Group("credit_lot_usages.day").
		Order("credit_lot_usages.day asc").
		Scan(&rows).Error
	return rows, err
}

// ListSupplierSettlements returns every vendor settlement issued to one
// supplier, newest period first. Void ones are included: a supplier who was
// told a figure and then saw it withdrawn should be able to see both.
func ListSupplierSettlements(supplier *CreditSupplier) ([]*Settlement, error) {
	var settlements []*Settlement
	err := DB.Where("kind = ? AND counterparty = ?", "vendor", supplier.CounterpartyKey()).
		Order("period_start desc, id desc").
		Find(&settlements).Error
	return settlements, err
}
