package setting

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const goodKey = "uuMAzHfMkidM7MATrq3KVgVpECAF7YwBjxFMBkQz4coxRkpIYXsNsgfw6lYyWfHw"

func TestNormalizeBinancePayPlatform(t *testing.T) {
	for _, raw := range []string{"", "binance.com", "BINANCE.COM", "nonsense", "binance_pay"} {
		assert.Equal(t, BinancePayPlatformGlobal, NormalizeBinancePayPlatform(raw), raw)
	}
	for _, raw := range []string{"binance.us", " Binance.US ", "us", "api.binance.us", "binance_pay_us"} {
		assert.Equal(t, BinancePayPlatformUS, NormalizeBinancePayPlatform(raw), raw)
	}
}

func TestBinancePayAccountsResolveFromOptions(t *testing.T) {
	prev := []string{BinancePayApiKey, BinancePaySecretKey, BinancePayReceiverId, BinancePayDepositAddresses, BinancePayUSApiKey, BinancePayUSSecretKey, BinancePayUSDepositAddresses}
	prevEnabled, prevUSEnabled := BinancePayEnabled, BinancePayUSEnabled
	t.Cleanup(func() {
		BinancePayApiKey, BinancePaySecretKey, BinancePayReceiverId, BinancePayDepositAddresses, BinancePayUSApiKey, BinancePayUSSecretKey, BinancePayUSDepositAddresses = prev[0], prev[1], prev[2], prev[3], prev[4], prev[5], prev[6]
		BinancePayEnabled, BinancePayUSEnabled = prevEnabled, prevUSEnabled
	})

	BinancePayEnabled, BinancePayApiKey, BinancePaySecretKey, BinancePayReceiverId = true, " "+goodKey+" ", goodKey, "34355667"
	BinancePayUSEnabled, BinancePayUSApiKey, BinancePayUSSecretKey, BinancePayUSDepositAddresses = true, goodKey, goodKey, `[{"network":"eth","address":"0xabc"}]`

	accounts := BinancePayAccounts()
	require.Len(t, accounts, 2)
	com, us := accounts[0], accounts[1]

	assert.Equal(t, BinancePayPlatformGlobal, com.Platform)
	assert.Equal(t, BinancePayMethodGlobal, com.PaymentMethod())
	assert.Equal(t, "https://api.binance.com", com.ApiBaseURL())
	assert.True(t, com.SupportsPayTransfers())
	assert.True(t, com.HasCredentials(), "surrounding whitespace is trimmed")
	assert.Equal(t, "34355667", com.PayIdForPayers())
	assert.True(t, com.Configured())
	assert.True(t, strings.Contains(com.Label(), "binance.com"))

	assert.Equal(t, BinancePayPlatformUS, us.Platform)
	assert.Equal(t, BinancePayMethodUS, us.PaymentMethod())
	assert.Equal(t, "https://api.binance.us", us.ApiBaseURL())
	assert.False(t, us.SupportsPayTransfers())
	assert.Equal(t, "", us.PayIdForPayers(), "no Pay ID on Binance.US")
	assert.Equal(t, []BinancePayDepositAddress{{Network: "ETH", Address: "0xabc"}}, us.Addresses())
	assert.True(t, us.Configured())
	assert.True(t, strings.Contains(us.Label(), "Binance.US"))

	byMethod, ok := BinancePayAccountForMethod("binance_pay_us")
	require.True(t, ok)
	assert.Equal(t, BinancePayPlatformUS, byMethod.Platform)
	_, ok = BinancePayAccountForMethod("stripe")
	assert.False(t, ok)
}

// A placeholder typed to get past a form is not a credential, and an account
// with no way to pay is not configured -- so the wallet never advertises a
// tile that would send money into the void.
func TestBinancePayAccountConfiguredRequiresRealCredentialsAndAWayToPay(t *testing.T) {
	base := BinancePayAccount{Platform: BinancePayPlatformGlobal, Enabled: true, ApiKey: goodKey, SecretKey: goodKey, ReceiverId: "34355667"}
	assert.True(t, base.Configured())

	placeholder := base
	placeholder.ApiKey, placeholder.SecretKey = "1", "1"
	assert.False(t, placeholder.HasCredentials())
	assert.False(t, placeholder.Configured())

	badPayId := base
	badPayId.ReceiverId = "1"
	assert.Equal(t, "", badPayId.PayIdForPayers())
	assert.False(t, badPayId.Configured(), "Pay ID '1' is not a Pay ID")
	badPayId.DepositAddresses = `[{"network":"TRX","address":"T1"}]`
	assert.True(t, badPayId.Configured(), "a deposit address is a way to pay")

	disabled := base
	disabled.Enabled = false
	assert.False(t, disabled.Configured())

	usNoAddress := BinancePayAccount{Platform: BinancePayPlatformUS, Enabled: true, ApiKey: goodKey, SecretKey: goodKey, ReceiverId: "34355667"}
	assert.False(t, usNoAddress.Configured(), "a Pay ID is useless on Binance.US")
}

func TestParseBinancePayDepositAddressesDropsBlankAndBadRows(t *testing.T) {
	got := ParseBinancePayDepositAddresses(`[{"network":"trx","address":" TXYZ "},{"network":"","address":"x"},{"network":"BSC"}]`)
	assert.Equal(t, []BinancePayDepositAddress{{Network: "TRX", Address: "TXYZ"}}, got)
	assert.Nil(t, ParseBinancePayDepositAddresses("not json"))
	assert.Nil(t, ParseBinancePayDepositAddresses(""))
	assert.Nil(t, ParseBinancePayDepositAddresses(`[{"network":"BSC"}]`))
}
