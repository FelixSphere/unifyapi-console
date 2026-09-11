/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

// UNIFYAPI-FORK: the posted terms on which we buy vendor credits.
//
// There is no negotiation and no application: a supplier sees the rate for
// their vendor before they type anything, submits, and is paid that rate.
// The operator changes the numbers here; a lot snapshots the rate it was
// submitted under, so changing terms never reprices a sale already made.

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

const (
	CreditLotPayoutPlatformCredit = "platform_credit"
	CreditLotPayoutExternal       = "external"

	// CreditShareBasisRevenue splits everything customers paid for traffic the
	// contributed key served. CreditShareBasisMargin splits what is left of
	// that after what we paid for the credits up front, which is the same
	// number whenever nothing was paid up front.
	CreditShareBasisRevenue = "revenue"
	CreditShareBasisMargin  = "margin"
)

type CreditSupplyTerms struct {
	// BuyRates is vendor -> the fraction of face value we pay. 0.2 is "2折":
	// a supplier holding $1,000 of Anthropic credit receives $200.
	BuyRates map[string]float64 `json:"buy_rates"`
	// ChannelPriority is given to every supplier-backed channel on activation
	// so the router drains bought credits before our own accounts.
	ChannelPriority int64 `json:"channel_priority"`
	// MinFaceUSD is the smallest sale we accept.
	MinFaceUSD float64 `json:"min_face_usd"`
	// PlatformCreditBonus is added to the payout when the supplier chooses to
	// be paid in platform credit instead of cash (0.1 = +10%). Zero disables.
	PlatformCreditBonus float64 `json:"platform_credit_bonus"`

	// RevenueShareRates is vendor -> the share of earnings a contributor keeps
	// when they contribute a key instead of selling it outright: 0.5 pays them
	// half of what their key earns, for as long as it earns. A vendor absent
	// from this map is not accepted on those terms; an empty map turns the
	// whole arrangement off.
	RevenueShareRates map[string]float64 `json:"revenue_share_rates"`
	// RevenueShareBasis is what that share is a share OF -- revenue or margin.
	// It is snapshotted onto each lot, so changing it never reprices a key
	// that is already earning.
	RevenueShareBasis string `json:"revenue_share_basis"`
	// MinSharePayoutUSD is the balance a contributor has to reach before we
	// send money. 0 pays any amount.
	MinSharePayoutUSD float64 `json:"min_share_payout_usd"`
}

func DefaultCreditSupplyTerms() CreditSupplyTerms {
	return CreditSupplyTerms{
		BuyRates:          map[string]float64{"anthropic": 0.20, "openai": 0.30, "google": 0.20},
		ChannelPriority:   10,
		MinFaceUSD:        100,
		RevenueShareRates: map[string]float64{"anthropic": 0.50, "openai": 0.50, "google": 0.50},
		RevenueShareBasis: CreditShareBasisMargin,
		MinSharePayoutUSD: 20,
	}
}

var (
	creditSupplyTerms   = DefaultCreditSupplyTerms()
	creditSupplyTermsMu sync.RWMutex
)

func GetCreditSupplyTerms() CreditSupplyTerms {
	creditSupplyTermsMu.RLock()
	defer creditSupplyTermsMu.RUnlock()
	out := creditSupplyTerms
	out.BuyRates = make(map[string]float64, len(creditSupplyTerms.BuyRates))
	for k, v := range creditSupplyTerms.BuyRates {
		out.BuyRates[k] = v
	}
	out.RevenueShareRates = make(map[string]float64, len(creditSupplyTerms.RevenueShareRates))
	for k, v := range creditSupplyTerms.RevenueShareRates {
		out.RevenueShareRates[k] = v
	}
	return out
}

// BuyRate returns the posted rate for a vendor, or false when we do not buy
// that vendor's credits at all.
func (t CreditSupplyTerms) BuyRate(vendor string) (float64, bool) {
	rate, ok := t.BuyRates[strings.ToLower(strings.TrimSpace(vendor))]
	return rate, ok && rate > 0
}

// RevenueShareRate returns the posted share for a vendor, or false when we do
// not take that vendor's credits on revenue-share terms at all.
func (t CreditSupplyTerms) RevenueShareRate(vendor string) (float64, bool) {
	rate, ok := t.RevenueShareRates[strings.ToLower(strings.TrimSpace(vendor))]
	return rate, ok && rate > 0
}

// ShareBasis is the configured basis, defaulted. An unset basis means margin:
// with nothing paid up front the two are identical, and where something was
// paid, netting it off first is the reading that cannot overpay.
func (t CreditSupplyTerms) ShareBasis() string {
	if t.RevenueShareBasis == CreditShareBasisRevenue {
		return CreditShareBasisRevenue
	}
	return CreditShareBasisMargin
}

// PayoutUSD is what a supplier receives for a lot under these terms.
func (t CreditSupplyTerms) PayoutUSD(faceUSD, rate float64, method string) float64 {
	amount := faceUSD * rate
	if method == CreditLotPayoutPlatformCredit && t.PlatformCreditBonus > 0 {
		amount *= 1 + t.PlatformCreditBonus
	}
	return amount
}

func ValidateCreditSupplyTerms(t CreditSupplyTerms) error {
	if len(t.BuyRates) == 0 {
		return errors.New("at least one vendor buy rate is required")
	}
	keys := make([]string, 0, len(t.BuyRates))
	for k := range t.BuyRates {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		rate := t.BuyRates[k]
		if k != strings.ToLower(strings.TrimSpace(k)) || k == "" {
			return fmt.Errorf("vendor %q must be a lower-case key", k)
		}
		if rate <= 0 || rate >= 1 {
			return fmt.Errorf("buy rate for %s must be between 0 and 1 (0.2 pays 20 cents per dollar), got %g", k, rate)
		}
	}
	if t.ChannelPriority < 0 {
		return errors.New("channel priority cannot be negative")
	}
	if t.MinFaceUSD < 0 {
		return errors.New("minimum face value cannot be negative")
	}
	if t.PlatformCreditBonus < 0 || t.PlatformCreditBonus > 1 {
		return errors.New("platform credit bonus must be between 0 and 1")
	}
	shareKeys := make([]string, 0, len(t.RevenueShareRates))
	for k := range t.RevenueShareRates {
		shareKeys = append(shareKeys, k)
	}
	sort.Strings(shareKeys)
	for _, k := range shareKeys {
		share := t.RevenueShareRates[k]
		if k != strings.ToLower(strings.TrimSpace(k)) || k == "" {
			return fmt.Errorf("vendor %q must be a lower-case key", k)
		}
		if share <= 0 || share >= 1 {
			return fmt.Errorf("revenue share for %s must be between 0 and 1 (0.5 keeps half), got %g", k, share)
		}
	}
	switch t.RevenueShareBasis {
	case "", CreditShareBasisRevenue, CreditShareBasisMargin:
	default:
		return fmt.Errorf("revenue share basis must be %q or %q", CreditShareBasisRevenue, CreditShareBasisMargin)
	}
	if t.MinSharePayoutUSD < 0 {
		return errors.New("minimum share payout cannot be negative")
	}
	return nil
}

func CreditSupplyTerms2JSONString() string {
	terms := GetCreditSupplyTerms()
	raw, err := json.Marshal(terms)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func UpdateCreditSupplyTermsByJSONString(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	terms := DefaultCreditSupplyTerms()
	terms.BuyRates = nil
	// Absent means "we do not take keys on those terms", not "keep the
	// defaults": the posted terms are exactly what the operator posted.
	terms.RevenueShareRates = nil
	if err := json.Unmarshal([]byte(raw), &terms); err != nil {
		return err
	}
	if err := ValidateCreditSupplyTerms(terms); err != nil {
		return err
	}
	creditSupplyTermsMu.Lock()
	creditSupplyTerms = terms
	creditSupplyTermsMu.Unlock()
	return nil
}
