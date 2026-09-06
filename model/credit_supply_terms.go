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
}

func DefaultCreditSupplyTerms() CreditSupplyTerms {
	return CreditSupplyTerms{
		BuyRates:        map[string]float64{"anthropic": 0.20, "openai": 0.30, "google": 0.20},
		ChannelPriority: 10,
		MinFaceUSD:      100,
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
	return out
}

// BuyRate returns the posted rate for a vendor, or false when we do not buy
// that vendor's credits at all.
func (t CreditSupplyTerms) BuyRate(vendor string) (float64, bool) {
	rate, ok := t.BuyRates[strings.ToLower(strings.TrimSpace(vendor))]
	return rate, ok && rate > 0
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
