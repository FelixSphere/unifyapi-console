/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package service

import (
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// parkRefusedWalletSettlement hands a wallet settlement the database refused
// to the outbox (model/settlement_outbox.go), together with the token
// adjustment that would have followed it. It reports whether it did.
//
// Only the wallet source is parked: its replay is a plain signed delta on the
// billing entity. Subscription and promotional settlements carry their own
// reservation state and keep upstream's behaviour (the error is returned and
// logged); they still get the connection retry inside the model layer.
func parkRefusedWalletSettlement(info *relaycommon.RelayInfo, funding FundingSource, delta int, err error) bool {
	if _, ok := funding.(*WalletFunding); !ok || !model.IsConnectionClassError(err) {
		return false
	}
	tokenDelta := delta
	if info.IsPlayground {
		tokenDelta = 0
	}
	model.ParkChargeSettlement(info.RequestId, info.UserId, delta, info.TokenId, info.TokenKey, tokenDelta, err)
	return true
}

// parkRefusedTokenSettlement parks a token adjustment the database refused
// after the funding source had already settled.
func parkRefusedTokenSettlement(info *relaycommon.RelayInfo, delta int, err error) bool {
	if info.IsPlayground || !model.IsConnectionClassError(err) {
		return false
	}
	model.ParkChargeSettlement(info.RequestId, info.UserId, 0, info.TokenId, info.TokenKey, delta, err)
	return true
}
