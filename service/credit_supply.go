/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package service

// UNIFYAPI-FORK: turns credit-pool state changes into operator notifications.
// The model layer raises the event; this is the only place that knows about
// mail and webhooks. See model/credit_supply_consume.go.

import (
	"fmt"

	"github.com/QuantumNous/new-api/model"
)

func init() {
	model.CreditLotEventHook = notifyCreditLotEvent
}

func notifyCreditLotEvent(lot model.CreditLot, event string) {
	var subject, content string
	switch event {
	case model.CreditLotEventExhausted:
		subject = fmt.Sprintf("Credit lot #%d exhausted (%s)", lot.Id, lot.Vendor)
		content = fmt.Sprintf(
			"Credit lot #%d (%s) consumed $%.2f of its $%.2f face value and has been retired. "+
				"Channel #%d is auto-disabled. Payable to the supplier for this lot: $%.2f.",
			lot.Id, lot.Vendor, lot.ConsumedUSD, lot.FaceValueUSD, lot.ChannelId, lot.PayableUSD())
	case model.CreditLotEventExpired:
		subject = fmt.Sprintf("Credit lot #%d expired (%s)", lot.Id, lot.Vendor)
		content = fmt.Sprintf(
			"Credit lot #%d (%s) reached its expiry with $%.2f of $%.2f unused. "+
				"Channel #%d is auto-disabled. Move or clear the expiry and reactivate if the vendor extended it.",
			lot.Id, lot.Vendor, lot.RemainingUSD(), lot.FaceValueUSD, lot.ChannelId)
	case model.CreditLotEventLowWater:
		subject = fmt.Sprintf("Credit lot #%d running low (%s)", lot.Id, lot.Vendor)
		content = fmt.Sprintf(
			"Credit lot #%d (%s) has $%.2f remaining of $%.2f, at or below its $%.2f low-water mark. "+
				"Channel #%d will auto-disable when it reaches zero.",
			lot.Id, lot.Vendor, lot.RemainingUSD(), lot.FaceValueUSD, lot.LowWaterUSD, lot.ChannelId)
	default:
		return
	}
	NotifyRootUser(fmt.Sprintf("credit_lot_%s_%d", event, lot.Id), subject, content)
}
