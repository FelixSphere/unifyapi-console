package setting

// UNIFYAPI-FORK: Binance Pay as a top-up gateway into the operator's own
// *personal* Binance account(s).
//
// There is no merchant contract. The operator's account receives the money
// (a Binance Pay transfer to their Pay ID, or a stablecoin deposit to one of
// their wallet addresses), and the console reconciles pending orders against
// the account's own history through a read-only API key. Nothing here can
// move funds: the key never needs trade or withdrawal permission.
//
// Two receiving accounts are supported side by side, because Binance
// (binance.com) and Binance.US are separate companies with separate API hosts
// and user bases: a binance.com customer cannot Binance-Pay a Binance.US
// account. Each account is its own payment method in the wallet, labelled
// with the platform, and is reconciled with its own key.
//
// Because a personal transfer carries no order reference, an order is
// identified by its exact amount: every pending order gets a unique fractional
// suffix (see model.ReserveBinancePayMoney), and the reconciler matches an
// incoming transaction to the one pending order with that amount.

import (
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

const (
	BinancePayPlatformGlobal = "binance.com"
	BinancePayPlatformUS     = "binance.us"

	// Payment-method identifiers, one per receiving account. They are what a
	// TopUp row carries in PaymentMethod and what the wallet uses as a tile
	// type. Mirrored as model.PaymentMethodBinancePay* (model may import
	// setting; not the other way round).
	BinancePayMethodGlobal = "binance_pay"
	BinancePayMethodUS     = "binance_pay_us"
)

// ---------------------------------------------------------------------------
// Shared settings (both accounts)
// ---------------------------------------------------------------------------

var (
	// Stablecoin the orders are denominated in. Must be a Binance asset code.
	BinancePayCurrency string = "USDT"
	// 1 USD of credit costs this many BinancePayCurrency.
	BinancePayUnitPrice float64 = 1.0
	BinancePayMinTopUp  int     = 1
	// A pending order that has not been paid within this window is expired,
	// which also frees its unique amount for reuse.
	BinancePayOrderTTLMinutes int = 60
	// Show the Binance tiles first, marked "Recommended", to users whose group
	// is a Partnership Program customer (the reseller channel).
	BinancePayRecommendForPartners bool = true
	// A payer who sends slightly MORE than the unique amount is still paying
	// this order; accept it automatically when the overpayment is within this
	// percentage and no other pending order fits. 0 disables.
	BinancePayOverpayTolerancePercent float64 = 5
)

// ---------------------------------------------------------------------------
// Account: Binance (binance.com). Option keys are the original ones, so an
// existing deployment keeps its configuration.
// ---------------------------------------------------------------------------

var (
	BinancePayEnabled          bool
	BinancePayApiKey           string
	BinancePaySecretKey        string
	BinancePayReceiverId       string // Pay ID (Binance UID) shown to payers
	BinancePayReceiverNickname string
	// Networks the account accepts on-chain deposits on, JSON ["TRX","BSC"].
	// The ADDRESSES are not typed by hand: they are read from Binance for the
	// account the key belongs to (see controller.refreshBinancePayAddresses)
	// and cached in BinancePayDepositAddresses. A hand-typed address that
	// belonged to a different account is exactly how a customer's transfer
	// once landed where the console could not see it.
	BinancePayDepositNetworks  string
	BinancePayDepositAddresses string // resolved snapshot, JSON [{"network","address"}]
)

// ---------------------------------------------------------------------------
// Account: Binance.US. No Binance Pay there, so no Pay ID: on-chain only.
// ---------------------------------------------------------------------------

var (
	BinancePayUSEnabled          bool
	BinancePayUSApiKey           string
	BinancePayUSSecretKey        string
	BinancePayUSReceiverNickname string
	BinancePayUSDepositNetworks  string
	BinancePayUSDepositAddresses string // resolved snapshot
)

type BinancePayDepositAddress struct {
	Network string `json:"network"`
	Address string `json:"address"`
}

// BinancePayAccount is one receiving account, resolved from the option
// variables at call time so a settings save takes effect immediately.
type BinancePayAccount struct {
	Platform         string
	Enabled          bool
	ApiKey           string
	SecretKey        string
	ReceiverId       string
	ReceiverNickname string
	DepositNetworks  string
	DepositAddresses string
}

// binanceNetworkAliases maps what people type to Binance's network codes.
var binanceNetworkAliases = map[string]string{
	"TRON": "TRX", "TRC20": "TRX", "TRX": "TRX",
	"BEP20": "BSC", "BNB": "BSC", "BSC": "BSC", "BNB SMART CHAIN": "BSC",
	"ERC20": "ETH", "ETH": "ETH", "ETHEREUM": "ETH",
	"SOL": "SOL", "SOLANA": "SOL",
	"MATIC": "MATIC", "POLYGON": "MATIC",
	"ARBITRUM": "ARBITRUM", "ARB": "ARBITRUM",
	"OPTIMISM": "OPTIMISM", "OP": "OPTIMISM",
	"AVAXC": "AVAXC", "AVAX": "AVAXC",
	"TON": "TON", "APT": "APT", "BASE": "BASE",
}

// NormalizeBinanceNetwork turns "TRC20" / "Tron" / "trx" into "TRX".
func NormalizeBinanceNetwork(raw string) string {
	code := strings.ToUpper(strings.TrimSpace(raw))
	if v, ok := binanceNetworkAliases[code]; ok {
		return v
	}
	return code
}

// ParseBinancePayNetworks parses the JSON list (or a comma-separated
// string) of network codes, normalised and de-duplicated.
func ParseBinancePayNetworks(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return nil
	}
	var list []string
	if strings.HasPrefix(raw, "[") {
		if err := common.UnmarshalJsonStr(raw, &list); err != nil {
			return nil
		}
	} else {
		list = strings.Split(raw, ",")
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(list))
	for _, n := range list {
		code := NormalizeBinanceNetwork(n)
		if code == "" {
			continue
		}
		if _, dup := seen[code]; dup {
			continue
		}
		seen[code] = struct{}{}
		out = append(out, code)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// BinancePayAccounts returns both accounts, binance.com first.
func BinancePayAccounts() []BinancePayAccount {
	return []BinancePayAccount{
		{
			Platform:         BinancePayPlatformGlobal,
			Enabled:          BinancePayEnabled,
			ApiKey:           strings.TrimSpace(BinancePayApiKey),
			SecretKey:        strings.TrimSpace(BinancePaySecretKey),
			ReceiverId:       strings.TrimSpace(BinancePayReceiverId),
			ReceiverNickname: strings.TrimSpace(BinancePayReceiverNickname),
			DepositNetworks:  BinancePayDepositNetworks,
			DepositAddresses: BinancePayDepositAddresses,
		},
		{
			Platform:         BinancePayPlatformUS,
			Enabled:          BinancePayUSEnabled,
			ApiKey:           strings.TrimSpace(BinancePayUSApiKey),
			SecretKey:        strings.TrimSpace(BinancePayUSSecretKey),
			ReceiverNickname: strings.TrimSpace(BinancePayUSReceiverNickname),
			DepositNetworks:  BinancePayUSDepositNetworks,
			DepositAddresses: BinancePayUSDepositAddresses,
		},
	}
}

// BinancePayAccountForPlatform looks an account up by platform id.
func BinancePayAccountForPlatform(platform string) (BinancePayAccount, bool) {
	platform = NormalizeBinancePayPlatform(platform)
	for _, a := range BinancePayAccounts() {
		if a.Platform == platform {
			return a, true
		}
	}
	return BinancePayAccount{}, false
}

// BinancePayAccountForMethod looks an account up by the payment method a
// TopUp row carries.
func BinancePayAccountForMethod(method string) (BinancePayAccount, bool) {
	switch strings.TrimSpace(method) {
	case BinancePayMethodGlobal:
		return BinancePayAccountForPlatform(BinancePayPlatformGlobal)
	case BinancePayMethodUS:
		return BinancePayAccountForPlatform(BinancePayPlatformUS)
	}
	return BinancePayAccount{}, false
}

// NormalizeBinancePayPlatform maps the spellings people type to a platform
// id. Anything unrecognised is binance.com.
func NormalizeBinancePayPlatform(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case BinancePayPlatformUS, "us", "binance-us", "binance_us", "api.binance.us", BinancePayMethodUS:
		return BinancePayPlatformUS
	default:
		return BinancePayPlatformGlobal
	}
}

// PaymentMethod is the identifier TopUp rows and wallet tiles use.
func (a BinancePayAccount) PaymentMethod() string {
	if a.Platform == BinancePayPlatformUS {
		return BinancePayMethodUS
	}
	return BinancePayMethodGlobal
}

// Label is the wallet tile name. It says which company, because a payer who
// holds an account on the other one cannot pay here.
func (a BinancePayAccount) Label() string {
	if a.Platform == BinancePayPlatformUS {
		return "Binance.US (on-chain)"
	}
	return "Binance Pay (binance.com)"
}

// ApiBaseURL is the REST host for the account's platform.
func (a BinancePayAccount) ApiBaseURL() string {
	if a.Platform == BinancePayPlatformUS {
		return "https://api.binance.us"
	}
	return "https://api.binance.com"
}

// SupportsPayTransfers reports whether the platform has Binance Pay
// (user-to-user transfers by Pay ID). Binance.US does not.
func (a BinancePayAccount) SupportsPayTransfers() bool {
	return a.Platform == BinancePayPlatformGlobal
}

// Networks are the deposit networks the operator selected. When none are
// selected yet but a legacy hand-typed address snapshot exists, its networks
// are used, so the next refresh replaces those addresses with the account's
// real ones instead of leaving the account unconfigured.
func (a BinancePayAccount) Networks() []string {
	if nets := ParseBinancePayNetworks(a.DepositNetworks); len(nets) > 0 {
		return nets
	}
	seen := map[string]struct{}{}
	var out []string
	for _, addr := range ParseBinancePayDepositAddresses(a.DepositAddresses) {
		code := NormalizeBinanceNetwork(addr.Network)
		if _, dup := seen[code]; dup || code == "" {
			continue
		}
		seen[code] = struct{}{}
		out = append(out, code)
	}
	return out
}

// Addresses are the account's deposit addresses as last read from Binance
// (the resolved snapshot), one per selected network.
func (a BinancePayAccount) Addresses() []BinancePayDepositAddress {
	return ParseBinancePayDepositAddresses(a.DepositAddresses)
}

// NetworksOptionKey / AddressesOptionKey name the option rows the resolver
// writes for this account.
func (a BinancePayAccount) NetworksOptionKey() string {
	if a.Platform == BinancePayPlatformUS {
		return "BinancePayUSDepositNetworks"
	}
	return "BinancePayDepositNetworks"
}

func (a BinancePayAccount) AddressesOptionKey() string {
	if a.Platform == BinancePayPlatformUS {
		return "BinancePayUSDepositAddresses"
	}
	return "BinancePayDepositAddresses"
}

// binanceApiKeyShape is what a Binance HMAC API key or secret looks like on
// both platforms: 64 alphanumeric characters. A placeholder like "1" typed
// to get past a form is not a key, and must not count as "configured".
var binanceApiKeyShape = regexp.MustCompile(`^[A-Za-z0-9]{64}$`)

// binancePayIdShape: Binance UIDs / Pay IDs are 6 to 20 digits.
var binancePayIdShape = regexp.MustCompile(`^[0-9]{6,20}$`)

// HasCredentials reports whether key and secret are plausible Binance keys.
func (a BinancePayAccount) HasCredentials() bool {
	return binanceApiKeyShape.MatchString(a.ApiKey) && binanceApiKeyShape.MatchString(a.SecretKey)
}

// PayIdForPayers is the Pay ID payers may send to: only on a platform with
// Binance Pay, and only when it looks like one.
func (a BinancePayAccount) PayIdForPayers() string {
	if !a.SupportsPayTransfers() || !binancePayIdShape.MatchString(a.ReceiverId) {
		return ""
	}
	return a.ReceiverId
}

// Configured reports whether the account can take payments: enabled,
// credible credentials, and at least one way to pay (a valid Pay ID on
// binance.com, or a deposit address).
func (a BinancePayAccount) Configured() bool {
	if !a.Enabled || !a.HasCredentials() {
		return false
	}
	return a.PayIdForPayers() != "" || len(a.Addresses()) > 0
}

// ParseBinancePayDepositAddresses parses the JSON list, dropping blank or
// partial rows; malformed input yields nil.
func ParseBinancePayDepositAddresses(raw string) []BinancePayDepositAddress {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return nil
	}
	var parsed []BinancePayDepositAddress
	if err := common.UnmarshalJsonStr(raw, &parsed); err != nil {
		return nil
	}
	out := make([]BinancePayDepositAddress, 0, len(parsed))
	for _, a := range parsed {
		a.Network = NormalizeBinanceNetwork(a.Network)
		a.Address = strings.TrimSpace(a.Address)
		if a.Network == "" || a.Address == "" {
			continue
		}
		out = append(out, a)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// GetBinancePayDepositAddresses is the binance.com account's addresses (kept
// for callers that predate the second account).
func GetBinancePayDepositAddresses() []BinancePayDepositAddress {
	return ParseBinancePayDepositAddresses(BinancePayDepositAddresses)
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
