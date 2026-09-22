/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

// UNIFYAPI-FORK: the rails a seller's share can be paid over.
//
// These used to be a list invented for this feature alone -- bank, PayPal,
// Wise, crypto -- none of which the platform has any relationship with. A
// seller would pick "Wise", and an operator would then have to find a Wise
// account to send from. Worse, the names did not match anything the same
// person sees on the wallet page.
//
// So the list is derived from Payment Settings instead: a rail is offered only
// where the operator has already configured the account that would send the
// money, and it carries the same name the payer side uses. Two consequences
// are deliberate and are the whole point:
//
//   - Turning Binance.US off in Payment Settings stops offering it as a payout
//     rail. One switch, both directions.
//   - Stripe is listed and permanently unavailable. Stripe moves money IN.
//     Paying a seller through it needs Stripe Connect and a connected account
//     per seller, which does not exist here. Listing it with the reason is
//     honest; leaving it out looks like an oversight, and offering it would
//     take a commitment we cannot keep.
//
// Legacy rails stay readable so an account filed before this change is never
// silently rewritten, but they cannot be newly chosen.

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/setting"
)

const (
	// PayoutRailPlatformCredit needs no external rail at all: the seller's own
	// UnifyAPI balance is the account.
	PayoutRailPlatformCredit = CreditLotPayoutPlatformCredit
	// PayoutRailBankTransfer is a wire the operator sends by hand. It needs no
	// gateway, so it is always available.
	PayoutRailBankTransfer = "bank_transfer"
	// PayoutRailBinancePay and PayoutRailBinancePayUS mirror the wallet's
	// payment-method ids exactly (setting.BinancePayMethod*), so the seller
	// reads the same two names on both sides of the platform.
	PayoutRailBinancePay   = setting.BinancePayMethodGlobal
	PayoutRailBinancePayUS = setting.BinancePayMethodUS
	// PayoutRailStripe is listed so its absence is explained rather than
	// looking like an omission. It is never available; see the file comment.
	PayoutRailStripe = "stripe"
)

// stripePayoutUnavailableReason is shown wherever Stripe appears. It names the
// missing piece, because "unavailable" on its own invites someone to go
// looking for a setting that does not exist.
const stripePayoutUnavailableReason = "Stripe takes payments in, it cannot send them out. Paying you through Stripe needs Stripe Connect, which this platform is not set up for."

// PayoutRail is one way a share can be paid, as both screens render it.
type PayoutRail struct {
	Id    string `json:"id"`
	Label string `json:"label"`
	Hint  string `json:"hint"`
	// NeedsAccount is whether the seller must name a holder and the account
	// itself. Platform credit does not: the login is the account.
	NeedsAccount bool `json:"needs_account"`
	// Networks is non-empty on an on-chain rail, and lists the chains the
	// operator's Payment Settings name. The seller picks one; the address is
	// stored as "<NETWORK>:<address>".
	Networks []string `json:"networks,omitempty"`
	// Currency, when set, is fixed by the rail and not the seller's to choose
	// -- a Binance transfer settles in the stablecoin Payment Settings names.
	Currency string `json:"currency,omitempty"`
	// Available is whether a seller may choose this rail right now.
	Available bool   `json:"available"`
	Reason    string `json:"unavailable_reason,omitempty"`
	// Legacy marks a rail that only exists because some account already
	// carries it. Never offered; never hidden from whoever filed it.
	Legacy bool `json:"legacy,omitempty"`
}

// OnChain reports whether this rail sends to a wallet address.
func (r PayoutRail) OnChain() bool { return len(r.Networks) > 0 }

// legacyPayoutRails are the invented rails this file replaced. They are kept
// because a supplier row may already carry one, and losing where somebody is
// paid is worse than carrying four dead strings.
var legacyPayoutRails = map[string]string{
	"bank":   "Bank transfer",
	"paypal": "PayPal (no longer offered)",
	"wise":   "Wise (no longer offered)",
	"crypto": "Crypto wallet (no longer offered)",
}

// NormalizePayoutMethod folds spelling and the one legacy id that has an exact
// modern equivalent. Everything else is returned lower-cased and unchanged.
func NormalizePayoutMethod(raw string) string {
	id := strings.ToLower(strings.TrimSpace(raw))
	if id == "bank" {
		// "bank" and "bank_transfer" were always the same wire.
		return PayoutRailBankTransfer
	}
	return id
}

// payoutNetworks are the chains an on-chain payout may use: the ones the
// operator named in Payment Settings for that account, so the rail cannot
// promise a chain the operator does not actually work with. An account with
// none configured falls back to the chains the platform knows at all.
func payoutNetworks(method string) []string {
	if account, ok := setting.BinancePayAccountForMethod(method); ok {
		if networks := setting.ParseBinancePayNetworks(account.DepositNetworks); len(networks) > 0 {
			return networks
		}
	}
	return setting.KnownBinanceNetworks()
}

// binancePayoutCurrency is the asset a Binance transfer settles in, taken from
// Payment Settings rather than typed by the seller.
func binancePayoutCurrency() string {
	currency := strings.ToUpper(strings.TrimSpace(setting.BinancePayCurrency))
	if currency == "" {
		return "USDT"
	}
	return currency
}

// binanceRailAvailability answers whether the operator has the account that
// would send the money: the Payment Settings toggle plus its credentials.
//
// Deliberately NOT setting.BinancePayAccount.Configured(), which additionally
// wants a Pay ID or a resolved deposit address. Those are what a payer needs
// in order to send money TO us. Sending money out needs neither -- the address
// is the seller's -- so requiring them would switch a working payout rail off
// for a reason that belongs to the other direction.
func binanceRailAvailability(method string) (bool, string) {
	account, ok := setting.BinancePayAccountForMethod(method)
	if ok && account.Enabled && account.HasCredentials() {
		return true, ""
	}
	return false, fmt.Sprintf("%s is not configured in Payment Settings, so there is no account to send from.", binanceRailLabel(method))
}

func binanceRailLabel(method string) string {
	if method == PayoutRailBinancePayUS {
		return "Binance.US"
	}
	return "Binance Pay"
}

// PayoutRails is the ordered list both the seller's dialog and the operator's
// table render. Availability is resolved at call time, so a Payment Settings
// save takes effect on the next page load with nothing to restart.
func PayoutRails() []PayoutRail {
	binanceOk, binanceWhy := binanceRailAvailability(PayoutRailBinancePay)
	binanceUSOk, binanceUSWhy := binanceRailAvailability(PayoutRailBinancePayUS)
	return []PayoutRail{
		{
			Id:        PayoutRailPlatformCredit,
			Label:     "Platform credit (added to your UnifyAPI balance)",
			Hint:      "Paid straight into the balance of this account. Nothing to file, and it lands the moment we settle.",
			Available: true,
		},
		{
			Id:           PayoutRailBankTransfer,
			Label:        "Bank transfer",
			Hint:         "Bank name, IBAN or account number, SWIFT/BIC, and your address if your bank asks for it.",
			NeedsAccount: true,
			Available:    true,
		},
		{
			Id:           PayoutRailBinancePay,
			Label:        "Binance Pay",
			Hint:         "Your Binance Pay ID, as binance.com shows it. Same account type we accept payments from.",
			NeedsAccount: true,
			Currency:     binancePayoutCurrency(),
			Available:    binanceOk,
			Reason:       binanceWhy,
		},
		{
			Id:           PayoutRailBinancePayUS,
			Label:        "Binance.US",
			Hint:         "Binance.US has no Pay ID, so this is an on-chain transfer: pick the network and give the receiving address.",
			NeedsAccount: true,
			Networks:     payoutNetworks(PayoutRailBinancePayUS),
			Currency:     binancePayoutCurrency(),
			Available:    binanceUSOk,
			Reason:       binanceUSWhy,
		},
		{
			Id:           PayoutRailStripe,
			Label:        "Stripe",
			NeedsAccount: true,
			Available:    false,
			Reason:       stripePayoutUnavailableReason,
		},
	}
}

// PayoutRailFor resolves any id a supplier row may carry, including the legacy
// ones that are never offered.
func PayoutRailFor(id string) (PayoutRail, bool) {
	id = NormalizePayoutMethod(id)
	for _, rail := range PayoutRails() {
		if rail.Id == id {
			return rail, true
		}
	}
	if label, ok := legacyPayoutRails[id]; ok {
		return PayoutRail{
			Id: id, Label: label, NeedsAccount: true, Legacy: true,
			Available: false,
			Reason:    "This payout method is no longer offered. It is kept because it is what you filed; pick another one to change it.",
		}, true
	}
	return PayoutRail{}, false
}

// PayoutMethodLabel names a rail for a screen. An id nothing recognises is
// returned as-is rather than blanked, so a surprise is visible.
func PayoutMethodLabel(id string) string {
	if id == "" {
		return ""
	}
	if rail, ok := PayoutRailFor(id); ok {
		return rail.Label
	}
	return id
}

// payoutAddressPattern is deliberately loose: every chain spells an address
// differently, and rejecting a valid one is as bad as accepting a typo. It
// catches the mistakes that actually happen -- a sentence, an empty string, a
// pasted URL, a value with spaces in it.
var payoutAddressPattern = regexp.MustCompile(`^[A-Za-z0-9]{20,120}$`)

// SplitOnChainPayoutDetails reads back the "<NETWORK>:<address>" an on-chain
// rail stores.
func SplitOnChainPayoutDetails(details string) (network, address string) {
	details = strings.TrimSpace(details)
	if network, address, found := strings.Cut(details, ":"); found {
		return strings.ToUpper(strings.TrimSpace(network)), strings.TrimSpace(address)
	}
	return "", details
}

// normalizeOnChainPayoutDetails canonicalises what the seller typed so the row
// always reads "<NETWORK>:<address>", whichever of the chain's many spellings
// they used.
func normalizeOnChainPayoutDetails(rail PayoutRail, details string) (string, error) {
	network, address := SplitOnChainPayoutDetails(details)
	if network == "" {
		return "", errors.New("name the network to send on, e.g. TRX:<address>")
	}
	network = setting.NormalizeBinanceNetwork(network)
	allowed := false
	for _, candidate := range rail.Networks {
		if candidate == network {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", fmt.Errorf("%s is not a network we can send %s on; choose one of %s",
			network, rail.Currency, strings.Join(rail.Networks, ", "))
	}
	if !payoutAddressPattern.MatchString(address) {
		// Naming the chain matters: the reader has to check the address
		// against the wallet it came from, not against this message.
		return "", fmt.Errorf("that does not look like a %s address -- paste the receiving address exactly as your wallet shows it", network)
	}
	return network + ":" + address, nil
}
