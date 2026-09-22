/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withBinanceAccounts points Payment Settings at whichever accounts a test
// needs and puts them back afterwards. The settings are process-wide, so a
// test that changed them must not decide what the next one reads.
func withBinanceAccounts(t *testing.T, global bool, us bool, networks string) {
	t.Helper()
	prev := []any{
		setting.BinancePayEnabled, setting.BinancePayApiKey, setting.BinancePaySecretKey,
		setting.BinancePayReceiverId, setting.BinancePayUSEnabled, setting.BinancePayUSApiKey,
		setting.BinancePayUSSecretKey, setting.BinancePayUSDepositNetworks, setting.BinancePayCurrency,
	}
	t.Cleanup(func() {
		setting.BinancePayEnabled = prev[0].(bool)
		setting.BinancePayApiKey = prev[1].(string)
		setting.BinancePaySecretKey = prev[2].(string)
		setting.BinancePayReceiverId = prev[3].(string)
		setting.BinancePayUSEnabled = prev[4].(bool)
		setting.BinancePayUSApiKey = prev[5].(string)
		setting.BinancePayUSSecretKey = prev[6].(string)
		setting.BinancePayUSDepositNetworks = prev[7].(string)
		setting.BinancePayCurrency = prev[8].(string)
	})
	// A key has to have the shape Payment Settings accepts (64 alphanumerics)
	// or the account counts as unconfigured and the rail is never offered.
	const fakeKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	setting.BinancePayCurrency = "USDT"
	setting.BinancePayEnabled = global
	setting.BinancePayApiKey, setting.BinancePaySecretKey, setting.BinancePayReceiverId = "", "", ""
	if global {
		setting.BinancePayApiKey, setting.BinancePaySecretKey = fakeKey, fakeKey
		setting.BinancePayReceiverId = "123456789"
	}
	setting.BinancePayUSEnabled = us
	setting.BinancePayUSApiKey, setting.BinancePayUSSecretKey = "", ""
	setting.BinancePayUSDepositNetworks = networks
	if us {
		setting.BinancePayUSApiKey, setting.BinancePayUSSecretKey = fakeKey, fakeKey
	}
}

func railById(t *testing.T, rails []PayoutRail, id string) PayoutRail {
	t.Helper()
	for _, rail := range rails {
		if rail.Id == id {
			return rail
		}
	}
	t.Fatalf("rail %q is not in the list", id)
	return PayoutRail{}
}

// The whole point of the change: the seller's list is Payment Settings, not a
// list invented for this feature. Switching an account off on the paying-in
// side must stop offering it on the paying-out side, with no second toggle.
func TestPayoutRailsFollowPaymentSettings(t *testing.T) {
	withBinanceAccounts(t, false, false, "")
	rails := PayoutRails()
	assert.False(t, railById(t, rails, PayoutRailBinancePay).Available)
	assert.False(t, railById(t, rails, PayoutRailBinancePayUS).Available)
	assert.Contains(t, railById(t, rails, PayoutRailBinancePayUS).Reason, "Payment Settings")
	// The two rails that need no gateway are always there.
	assert.True(t, railById(t, rails, PayoutRailPlatformCredit).Available)
	assert.True(t, railById(t, rails, PayoutRailBankTransfer).Available)

	withBinanceAccounts(t, true, false, "")
	rails = PayoutRails()
	assert.True(t, railById(t, rails, PayoutRailBinancePay).Available)
	assert.False(t, railById(t, rails, PayoutRailBinancePayUS).Available)

	withBinanceAccounts(t, false, true, `["TRX","BSC"]`)
	rails = PayoutRails()
	assert.False(t, railById(t, rails, PayoutRailBinancePay).Available)
	usRail := railById(t, rails, PayoutRailBinancePayUS)
	assert.True(t, usRail.Available)
	// The chains offered are the ones the operator actually named, not a
	// hard-coded list that could promise a chain nobody works with.
	assert.Equal(t, []string{"TRX", "BSC"}, usRail.Networks)
	assert.Equal(t, "USDT", usRail.Currency)
}

// Stripe is the one rail that is listed and never available. A seller who
// looks for it has to find the reason, not an empty space.
func TestStripeIsListedAsUnavailableWithItsReason(t *testing.T) {
	withBinanceAccounts(t, true, true, `["TRX"]`)
	stripe := railById(t, PayoutRails(), PayoutRailStripe)
	assert.False(t, stripe.Available)
	assert.Contains(t, stripe.Reason, "Stripe Connect")

	setupCreditSupplyTestDB(t)
	supplier := seedPayoutSupplier(t, 0)
	_, err := SetSupplierPayoutAccount(supplier.Id, SupplierPayoutAccount{
		Method: PayoutRailStripe, Holder: "Acme Ltd", Details: "acme@example.com",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Stripe Connect")
}

func seedPayoutSupplier(t *testing.T, userId int) *CreditSupplier {
	t.Helper()
	supplier := &CreditSupplier{Name: "Acme Labs", Code: "acme", UserId: userId, Status: CreditSupplierStatusActive}
	require.NoError(t, DB.Create(supplier).Error)
	return supplier
}

func TestASellerCannotPickARailWeHaveNoAccountFor(t *testing.T) {
	setupCreditSupplyTestDB(t)
	withBinanceAccounts(t, false, false, "")
	supplier := seedPayoutSupplier(t, 0)

	_, err := SetSupplierPayoutAccount(supplier.Id, SupplierPayoutAccount{
		Method: PayoutRailBinancePayUS, Holder: "Acme Ltd",
		Details: "TRX:TQn9Y2khDD95J42FQtQTdwVVRZqqArtzBb",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Payment Settings")

	// Bank transfer needs no gateway, so it is never blocked.
	updated, err := SetSupplierPayoutAccount(supplier.Id, SupplierPayoutAccount{
		Method: PayoutRailBankTransfer, Holder: "Acme Ltd",
		Details: "Example Bank, IBAN GB33BUKB20201555555555, SWIFT BUKBGB22", Currency: "eur",
	})
	require.NoError(t, err)
	assert.Equal(t, PayoutRailBankTransfer, updated.PayoutMethod)
	assert.Equal(t, "EUR", updated.PayoutCurrency)
	assert.True(t, updated.HasPayoutAccount())
}

// A seller who is already being paid over a rail must not lose it because an
// operator switched that gateway off: editing the address would otherwise
// throw away the rail as a side effect.
func TestARailSwitchedOffStaysUsableForWhoeverAlreadyFiledIt(t *testing.T) {
	setupCreditSupplyTestDB(t)
	withBinanceAccounts(t, false, true, `["TRX"]`)
	supplier := seedPayoutSupplier(t, 0)

	filed, err := SetSupplierPayoutAccount(supplier.Id, SupplierPayoutAccount{
		Method: PayoutRailBinancePayUS, Holder: "Acme Ltd",
		Details: "trc20:TQn9Y2khDD95J42FQtQTdwVVRZqqArtzBb",
	})
	require.NoError(t, err)
	// The chain's many spellings collapse to one canonical row.
	assert.Equal(t, "TRX:TQn9Y2khDD95J42FQtQTdwVVRZqqArtzBb", filed.PayoutDetails)
	assert.Equal(t, "USDT", filed.PayoutCurrency)

	withBinanceAccounts(t, false, false, "")
	assert.True(t, filed.HasPayoutAccount(), "an account already on file is still an account")

	// Same rail, new address: allowed, because it is the rail they are on.
	moved, err := SetSupplierPayoutAccount(supplier.Id, SupplierPayoutAccount{
		Method: PayoutRailBinancePayUS, Holder: "Acme Ltd",
		Details: "TRX:TJmmqjb1DK9FSBfhZCPXkmUbrbBGQFCrtd",
	})
	require.NoError(t, err)
	assert.Equal(t, "TRX:TJmmqjb1DK9FSBfhZCPXkmUbrbBGQFCrtd", moved.PayoutDetails)

	// But a rail they are NOT on is still refused.
	_, err = SetSupplierPayoutAccount(supplier.Id, SupplierPayoutAccount{
		Method: PayoutRailBinancePay, Holder: "Acme Ltd", Details: "987654321",
	})
	require.Error(t, err)
}

func TestOnChainPayoutDetailsAreCheckedBeforeMoneyMoves(t *testing.T) {
	setupCreditSupplyTestDB(t)
	withBinanceAccounts(t, false, true, `["TRX","BSC"]`)
	supplier := seedPayoutSupplier(t, 0)

	for name, details := range map[string]string{
		"no network at all":       "TQn9Y2khDD95J42FQtQTdwVVRZqqArtzBb",
		"a chain we cannot send":  "SOL:TQn9Y2khDD95J42FQtQTdwVVRZqqArtzBb",
		"a sentence not a wallet": "TRX:send it to my usual address please",
		"an address with spaces":  "TRX:TQn9Y2khDD95J42 FQtQTdwVVRZqqArtzBb",
		"nothing after the chain": "TRX:",
	} {
		_, err := SetSupplierPayoutAccount(supplier.Id, SupplierPayoutAccount{
			Method: PayoutRailBinancePayUS, Holder: "Acme Ltd", Details: details,
		})
		assert.Error(t, err, name)
	}

	ok, err := SetSupplierPayoutAccount(supplier.Id, SupplierPayoutAccount{
		Method: PayoutRailBinancePayUS, Holder: "Acme Ltd",
		Details: "BSC:0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed",
	})
	require.NoError(t, err)
	network, address := SplitOnChainPayoutDetails(ok.PayoutDetails)
	assert.Equal(t, "BSC", network)
	assert.Equal(t, "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed", address)
}

// An account filed before the rails were aligned with Payment Settings must
// keep working. Losing where somebody is paid is the one outcome worth more
// care than a tidy list.
func TestAccountsFiledUnderTheOldMethodNamesStillRead(t *testing.T) {
	setupCreditSupplyTestDB(t)
	withBinanceAccounts(t, false, false, "")

	// "bank" was always the same wire as "bank_transfer".
	assert.Equal(t, PayoutRailBankTransfer, NormalizePayoutMethod("bank"))
	assert.Equal(t, "Bank transfer", PayoutMethodLabel("bank"))

	for _, legacy := range []string{"paypal", "wise", "crypto"} {
		rail, ok := PayoutRailFor(legacy)
		require.True(t, ok, legacy)
		assert.True(t, rail.Legacy, legacy)
		assert.False(t, rail.Available, legacy)
		assert.Contains(t, strings.ToLower(rail.Label), strings.ToLower(legacy))

		// Still a filed account, so selling is not blocked on it...
		supplier := &CreditSupplier{
			Name: "Old " + legacy, Code: "old-" + legacy, Status: CreditSupplierStatusActive,
			PayoutMethod: legacy, PayoutHolder: "Acme Ltd", PayoutDetails: "acme@example.com",
		}
		require.NoError(t, DB.Create(supplier).Error)
		assert.True(t, supplier.HasPayoutAccount(), legacy)

		// ...and it is not offered to anyone who has not already got it.
		fresh := seedPayoutSupplier(t, 0)
		fresh.Code = "fresh-" + legacy
		_, err := SetSupplierPayoutAccount(fresh.Id, SupplierPayoutAccount{
			Method: legacy, Holder: "Acme Ltd", Details: "acme@example.com",
		})
		assert.Error(t, err, legacy)
		require.NoError(t, DB.Delete(fresh).Error)
	}
	// Nothing recognises this one, so it is not an account.
	assert.Equal(t, "carrier-pigeon", PayoutMethodLabel("carrier-pigeon"))
	_, ok := PayoutRailFor("carrier-pigeon")
	assert.False(t, ok)
}

func TestPlatformCreditNeedsALoginAndNoDetails(t *testing.T) {
	setupCreditSupplyTestDB(t)
	withBinanceAccounts(t, false, false, "")

	noLogin := seedPayoutSupplier(t, 0)
	_, err := SetSupplierPayoutAccount(noLogin.Id, SupplierPayoutAccount{Method: PayoutRailPlatformCredit})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "login")

	withLogin := &CreditSupplier{Name: "Beta", Code: "beta", UserId: 7, Status: CreditSupplierStatusActive}
	require.NoError(t, DB.Create(withLogin).Error)
	saved, err := SetSupplierPayoutAccount(withLogin.Id, SupplierPayoutAccount{
		Method: PayoutRailPlatformCredit, Holder: "ignored", Details: "ignored",
	})
	require.NoError(t, err)
	// The login is the account, so nothing else is kept.
	assert.Empty(t, saved.PayoutHolder)
	assert.Empty(t, saved.PayoutDetails)
	assert.True(t, saved.HasPayoutAccount())
	assert.Equal(t, CreditLotPayoutPlatformCredit, saved.ExternalPayoutMethod())
}

// The guard that stopped a seller pasting an upstream key into the free-text
// details field has to survive the rewrite of the validator around it.
func TestAKeyPastedIntoThePayoutDetailsIsStillRefused(t *testing.T) {
	setupCreditSupplyTestDB(t)
	withBinanceAccounts(t, false, false, "")
	supplier := seedPayoutSupplier(t, 0)
	_, err := SetSupplierPayoutAccount(supplier.Id, SupplierPayoutAccount{
		Method: PayoutRailBankTransfer, Holder: "Acme Ltd",
		Details: "wire to the account behind sk-ant-api03-not-a-bank-account",
	})
	require.ErrorIs(t, err, ErrCreditLotSecretInText)
}

func TestTheOperatorTableReadsRailNamesNotIds(t *testing.T) {
	setupCreditSupplyTestDB(t)
	withBinanceAccounts(t, false, true, `["TRX"]`)
	supplier := seedPayoutSupplier(t, 0)
	_, err := SetSupplierPayoutAccount(supplier.Id, SupplierPayoutAccount{
		Method: PayoutRailBinancePayUS, Holder: "Acme Ltd",
		Details: "TRX:TQn9Y2khDD95J42FQtQTdwVVRZqqArtzBb",
	})
	require.NoError(t, err)

	suppliers, err := GetCreditSuppliers()
	require.NoError(t, err)
	require.Len(t, suppliers, 1)
	assert.Equal(t, "Binance.US", suppliers[0].PayoutRailLabel)
	// And the details are still masked for the list, tail only.
	assert.Equal(t, "••••••tzBb", suppliers[0].MaskedPayoutDetails())
}
