/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

// UNIFYAPI-FORK: paying a contributor their share of what their key earned.
//
// A lot bought outright is paid once, at activation, and that is the end of it
// (credit_lot.go, PayCreditLot). A CONTRIBUTED key is never bought: it earns
// its owner a share of the revenue it serves, for as long as it serves any, and
// that share is paid out repeatedly. This file is the second kind of payment.
//
// The money is derived, never accumulated by hand: each lot carries the revenue
// and the cost it has accrued on the consume path, the share falls out of them
// (CreditLot.EarnedShareUSD), and a payout is the difference between what has
// been earned and what has already been paid. There is therefore no running
// "balance" column that can drift away from the traffic that produced it -- the
// only stored figure is PaidShareUSD, and it only ever goes up, by exactly what
// was paid.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"gorm.io/gorm"
)

var (
	ErrNoShareToPay        = errors.New("this contributor has nothing owing right now")
	ErrShareBelowMinimum   = errors.New("the balance owing is below the posted minimum payout")
	ErrShareNeedsWallet    = errors.New("this contributor has no login to credit; pay externally instead")
	ErrShareNeedsReference = errors.New("record the transfer reference the contributor can look up")
)

// CreditSharePayout is one dividend payment for one lot. A single operator
// action pays every lot a contributor is owed for and writes one row per lot,
// all sharing a Batch: per-lot rows keep the audit trail on the lot where the
// traffic happened, the batch keeps one payment recognisable as one payment.
type CreditSharePayout struct {
	Id         int    `json:"id" gorm:"primaryKey"`
	SupplierId int    `json:"supplier_id" gorm:"not null;index"`
	LotId      int    `json:"lot_id" gorm:"not null;index"`
	Batch      string `json:"batch" gorm:"type:varchar(40);not null;index"`
	// AmountUSD is what this payment settled for this lot.
	AmountUSD float64 `json:"amount_usd" gorm:"column:amount_usd;not null;default:0"`
	// EarnedToDateUSD and RevenueToDateUSD are the lot's cumulative figures at
	// the moment of payment. They are what makes a historic payment
	// checkable: the amount is the difference between two of them.
	EarnedToDateUSD  float64 `json:"earned_to_date_usd" gorm:"column:earned_to_date_usd;not null;default:0"`
	RevenueToDateUSD float64 `json:"revenue_to_date_usd" gorm:"column:revenue_to_date_usd;not null;default:0"`
	SharePct         float64 `json:"share_pct" gorm:"not null;default:0"`
	Method           string  `json:"method" gorm:"type:varchar(24)"`
	Reference        string  `json:"reference" gorm:"type:varchar(191)"`
	Actor            string  `json:"actor" gorm:"type:varchar(64)"`
	CreatedAt        int64   `json:"created_at" gorm:"autoCreateTime"`
}

// SharePayoutRequest is the operator settling everything a contributor is owed.
type SharePayoutRequest struct {
	Actor string
	// Method is platform_credit (booked into their wallet here) or external
	// (already sent; Reference is how they find it).
	Method    string
	Reference string
	// Force pays a balance below the posted minimum. The minimum is a promise
	// to the contributor about when we will pay, not a reason to refuse an
	// operator who has decided to.
	Force bool
}

// SharePayoutResult is what one payout action came to.
type SharePayoutResult struct {
	Batch     string               `json:"batch"`
	AmountUSD float64              `json:"amount_usd"`
	Payouts   []*CreditSharePayout `json:"payouts"`
}

// PaySupplierShare pays a contributor everything their keys have earned and not
// yet been paid, across every lot, in one transaction. Platform credit lands in
// their wallet inside it, so a recorded payment and the money moving are the
// same event.
func PaySupplierShare(supplierId int, req SharePayoutRequest) (*SharePayoutResult, error) {
	reference := strings.TrimSpace(req.Reference)
	if textLooksLikeProviderSecret(reference) {
		return nil, ErrCreditLotSecretInText
	}
	terms := GetCreditSupplyTerms()
	batch := common.GetUUID()
	now := common.GetTimestamp()
	result := &SharePayoutResult{Batch: batch}
	var supplier CreditSupplier
	var paidQuota int
	var wallet BillingEntity

	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).First(&supplier, "id = ?", supplierId).Error; err != nil {
			return err
		}
		switch req.Method {
		case CreditLotPayoutPlatformCredit:
			if supplier.UserId <= 0 {
				return ErrShareNeedsWallet
			}
		case CreditLotPayoutExternal:
			if reference == "" {
				return ErrShareNeedsReference
			}
		default:
			return fmt.Errorf("unknown payout method %q", req.Method)
		}

		var lots []*CreditLot
		// Locked for the transaction: two operators paying the same
		// contributor at once would both read the same unpaid balance and pay
		// it twice.
		err := lockForUpdate(tx).
			Where("supplier_id = ? AND deal_type = ? AND status <> ?",
				supplierId, CreditLotDealRevenueShare, CreditLotStatusRejected).
			Order("id asc").Find(&lots).Error
		if err != nil {
			return err
		}
		total := 0.0
		for _, lot := range lots {
			if lot.UnpaidShareUSD() <= 0 {
				continue
			}
			total += lot.UnpaidShareUSD()
		}
		if total <= 0 {
			return ErrNoShareToPay
		}
		if !req.Force && terms.MinSharePayoutUSD > 0 && total < terms.MinSharePayoutUSD {
			return fmt.Errorf("%w: $%.2f owing, minimum is $%.2f", ErrShareBelowMinimum, total, terms.MinSharePayoutUSD)
		}

		for _, lot := range lots {
			amount := lot.UnpaidShareUSD()
			if amount <= 0 {
				continue
			}
			earned := lot.EarnedShareUSD()
			// Conditional on the balance we read: on SQLite, where the row
			// lock above is a no-op, this is what stops a second payment.
			update := tx.Model(&CreditLot{}).
				Where("id = ? AND paid_share_usd = ?", lot.Id, lot.PaidShareUSD).
				Updates(map[string]interface{}{"paid_share_usd": earned, "updated_at": now})
			if update.Error != nil {
				return update.Error
			}
			if update.RowsAffected != 1 {
				return errors.New("this contributor's balance changed while it was being paid; try again")
			}
			payout := &CreditSharePayout{
				SupplierId: supplierId, LotId: lot.Id, Batch: batch, AmountUSD: amount,
				EarnedToDateUSD: earned, RevenueToDateUSD: lot.ShareRevenueUSD,
				SharePct: lot.RevenueSharePct, Method: req.Method, Reference: reference,
				Actor: req.Actor,
			}
			if err := tx.Create(payout).Error; err != nil {
				return err
			}
			message := fmt.Sprintf("revenue share paid: $%.2f of $%.2f earned on $%.2f of revenue, via %s %s",
				amount, earned, lot.ShareRevenueUSD, req.Method, reference)
			if err := appendCreditLotEvent(tx, lot.Id, req.Actor, "share_paid", lot.Status, lot.Status, message); err != nil {
				return err
			}
			result.Payouts = append(result.Payouts, payout)
			result.AmountUSD += amount
		}

		if req.Method == CreditLotPayoutPlatformCredit {
			paidQuota = int(result.AmountUSD * common.QuotaPerUnit)
			if paidQuota > 0 {
				entity, err := IncreaseUserQuotaWithTx(tx, supplier.UserId, paidQuota)
				if err != nil {
					return err
				}
				wallet = entity
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if paidQuota > 0 {
		RecordLog(supplier.UserId, LogTypeTopup, fmt.Sprintf(
			"credit supply: revenue share paid in platform credit, %s", logger.LogQuota(paidQuota)))
		_ = invalidateBillingQuotaCache(wallet)
	}
	common.SysLog(fmt.Sprintf("credit supply: paid %s $%.2f of revenue share (batch %s)", supplier.Code, result.AmountUSD, batch))
	return result, nil
}

// CreditShareFilter narrows a payout listing.
type CreditShareFilter struct {
	SupplierId int
	LotId      int
	Limit      int
}

func ListCreditSharePayouts(filter CreditShareFilter) ([]*CreditSharePayout, error) {
	query := DB.Model(&CreditSharePayout{})
	if filter.SupplierId > 0 {
		query = query.Where("supplier_id = ?", filter.SupplierId)
	}
	if filter.LotId > 0 {
		query = query.Where("lot_id = ?", filter.LotId)
	}
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	var payouts []*CreditSharePayout
	err := query.Order("id desc").Limit(limit).Find(&payouts).Error
	return payouts, err
}

// CreditShareTotals is one contributor's dividend position.
type CreditShareTotals struct {
	Lots       int     `json:"lots"`
	RevenueUSD float64 `json:"revenue_usd"`
	EarnedUSD  float64 `json:"earned_usd"`
	PaidUSD    float64 `json:"paid_usd"`
	UnpaidUSD  float64 `json:"unpaid_usd"`
}

// SumCreditShare adds up the dividend position of a set of lots. Rejected lots
// never earned anything, so they are the caller's to exclude; every other
// status counts, because traffic a retired key served is still owed for.
func SumCreditShare(lots []*CreditLot) CreditShareTotals {
	var totals CreditShareTotals
	for _, lot := range lots {
		if !lot.IsRevenueShare() || lot.Status == CreditLotStatusRejected {
			continue
		}
		totals.Lots++
		totals.RevenueUSD += lot.ShareRevenueUSD
		totals.EarnedUSD += lot.EarnedShareUSD()
		totals.PaidUSD += lot.PaidShareUSD
		totals.UnpaidUSD += lot.UnpaidShareUSD()
	}
	return totals
}
