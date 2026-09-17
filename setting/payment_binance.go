package setting

// UNIFYAPI-FORK: Binance Pay as a top-up gateway for a *personal* Binance
// account.
//
// There is no merchant contract here. The operator's own Binance account
// receives the money (a Binance Pay transfer to their Pay ID, or a stablecoin
// deposit to one of their wallet addresses), and the console reconciles the
// pending orders against the account's own history through a read-only API
// key. Nothing in this file can move funds: the key never needs trade or
// withdrawal permission and the settings UI says so.
//
// Because a personal transfer carries no order reference, an order is
// identified by its exact amount: every pending order gets a unique fractional
// suffix (see model.ReserveBinancePayMoney), and the reconciler matches an
// incoming transaction to the one pending order with that amount.

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
)

var (
	BinancePayEnabled bool
	// Read-only Binance API key/secret of the receiving account. Only
	// "Enable Reading" is required; the settings page tells the operator to
	// grant nothing else.
	BinancePayApiKey    string
	BinancePaySecretKey string
	// Pay ID (Binance UID) the payer sends to. Also used to make sure a
	// matched transaction was received by this account, not sent from it.
	BinancePayReceiverId string
	// Display name of the receiving account, so the payer can confirm the
	// recipient Binance shows them.
	BinancePayReceiverNickname string
	// Stablecoin the order is denominated in. Must be a Binance asset code.
	BinancePayCurrency string = "USDT"
	// 1 USD of credit costs this many BinancePayCurrency.
	BinancePayUnitPrice float64 = 1.0
	BinancePayMinTopUp  int     = 1
	// A pending order that has not been paid within this window is expired,
	// which also frees its unique amount for reuse.
	BinancePayOrderTTLMinutes int = 60
	// Optional on-chain deposit addresses of the same account, as JSON
	// [{"network":"TRX","address":"T..."}]. When set, a matching deposit is
	// accepted as payment too, so non-Binance wallets can pay.
	BinancePayDepositAddresses string
	// Show Binance Pay first, marked "Recommended", to users whose group is a
	// Partnership Program customer (the reseller channel).
	BinancePayRecommendForPartners bool = true
	// Which Binance the receiving account lives on. Binance.US is a separate
	// company with its own API host and no Binance Pay: there, payments can
	// only arrive on-chain to a deposit address.
	BinancePayPlatform string = BinancePayPlatformGlobal
	// A payer who sends slightly MORE than the unique amount is still paying
	// this order; accept it automatically when the overpayment is within this
	// percentage and no other pending order fits. 0 disables.
	BinancePayOverpayTolerancePercent float64 = 5
)

const (
	BinancePayPlatformGlobal = "binance.com"
	BinancePayPlatformUS     = "binance.us"
)

// GetBinancePayPlatform normalises the configured platform.
func GetBinancePayPlatform() string {
	switch strings.ToLower(strings.TrimSpace(BinancePayPlatform)) {
	case BinancePayPlatformUS, "us", "binance-us", "api.binance.us":
		return BinancePayPlatformUS
	default:
		return BinancePayPlatformGlobal
	}
}

// BinancePayApiBaseURL is the REST host for the configured platform.
func BinancePayApiBaseURL() string {
	if GetBinancePayPlatform() == BinancePayPlatformUS {
		return "https://api.binance.us"
	}
	return "https://api.binance.com"
}

// BinancePaySupportsPayTransfers reports whether the platform has Binance Pay
// (user-to-user transfers by Pay ID). Binance.US does not.
func BinancePaySupportsPayTransfers() bool {
	return GetBinancePayPlatform() == BinancePayPlatformGlobal
}

type BinancePayDepositAddress struct {
	Network string `json:"network"`
	Address string `json:"address"`
}

// GetBinancePayDepositAddresses parses BinancePayDepositAddresses, dropping
// blank rows. A malformed value yields no addresses rather than an error: the
// gateway keeps working with Pay-ID transfers only.
func GetBinancePayDepositAddresses() []BinancePayDepositAddress {
	raw := strings.TrimSpace(BinancePayDepositAddresses)
	if raw == "" || raw == "[]" {
		return nil
	}
	var parsed []BinancePayDepositAddress
	if err := common.UnmarshalJsonStr(raw, &parsed); err != nil {
		return nil
	}
	out := make([]BinancePayDepositAddress, 0, len(parsed))
	for _, a := range parsed {
		a.Network = strings.ToUpper(strings.TrimSpace(a.Network))
		a.Address = strings.TrimSpace(a.Address)
		if a.Network == "" || a.Address == "" {
			continue
		}
		out = append(out, a)
	}
	return out
}

// GetBinancePayCurrency returns the configured asset code, upper-cased, with
// USDT as the fallback.
func GetBinancePayCurrency() string {
	c := strings.ToUpper(strings.TrimSpace(BinancePayCurrency))
	if c == "" {
		return "USDT"
	}
	return c
}
