package setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBinancePayPlatformResolution(t *testing.T) {
	prev := BinancePayPlatform
	t.Cleanup(func() { BinancePayPlatform = prev })

	for _, raw := range []string{"", "binance.com", "BINANCE.COM", "nonsense"} {
		BinancePayPlatform = raw
		assert.Equal(t, BinancePayPlatformGlobal, GetBinancePayPlatform(), raw)
		assert.Equal(t, "https://api.binance.com", BinancePayApiBaseURL(), raw)
		assert.True(t, BinancePaySupportsPayTransfers(), raw)
	}
	for _, raw := range []string{"binance.us", " Binance.US ", "us", "api.binance.us"} {
		BinancePayPlatform = raw
		assert.Equal(t, BinancePayPlatformUS, GetBinancePayPlatform(), raw)
		assert.Equal(t, "https://api.binance.us", BinancePayApiBaseURL(), raw)
		assert.False(t, BinancePaySupportsPayTransfers(), raw)
	}
}

func TestGetBinancePayDepositAddressesDropsBlankAndBadRows(t *testing.T) {
	prev := BinancePayDepositAddresses
	t.Cleanup(func() { BinancePayDepositAddresses = prev })

	BinancePayDepositAddresses = `[{"network":"trx","address":" TXYZ "},{"network":"","address":"x"},{"network":"BSC"}]`
	got := GetBinancePayDepositAddresses()
	assert.Equal(t, []BinancePayDepositAddress{{Network: "TRX", Address: "TXYZ"}}, got)

	BinancePayDepositAddresses = "not json"
	assert.Nil(t, GetBinancePayDepositAddresses())
	BinancePayDepositAddresses = ""
	assert.Nil(t, GetBinancePayDepositAddresses())
}
