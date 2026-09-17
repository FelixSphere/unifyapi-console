package controller

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setTolerance(t *testing.T, pct float64) {
	t.Helper()
	prev := setting.BinancePayOverpayTolerancePercent
	setting.BinancePayOverpayTolerancePercent = pct
	t.Cleanup(func() { setting.BinancePayOverpayTolerancePercent = prev })
}

// A payer who rounds UP is still paying this order: within tolerance and with
// only one order that fits, it is credited automatically at the order's
// amount, and the ledger keeps what was actually received.
func TestReconcileAcceptsUnambiguousOverpayment(t *testing.T) {
	setupBinancePayControllerDB(t)
	setTolerance(t, 5)
	insertBinanceOrder(t, "BNP-A", 20, 20.0137, time.Minute)
	insertBinanceOrder(t, "BNP-B", 50, 50.4321, time.Minute)

	reader := &fakeBinanceReader{pay: []service.BinancePayTransaction{
		payRow("M_P_round", "20.02", "111", testBinanceReceiver, "C2C"), // +0.0063 over BNP-A
	}}
	require.NoError(t, reconcileBinancePay(context.Background(), reader, "test"))

	assert.Equal(t, common.TopUpStatusSuccess, binanceStatusOf(t, "BNP-A"))
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-B"))
	assert.Equal(t, 20*500_000, binanceQuotaOf(t, 1), "credited the order amount, not the overpayment")

	var ledger []model.BinancePayTransaction
	require.NoError(t, model.DB.Find(&ledger).Error)
	require.Len(t, ledger, 1)
	assert.Equal(t, "20.02", ledger[0].Amount)
}

// Two pending orders inside the same tolerance band cannot be told apart, so
// neither is credited; the operator's matching view gets it.
func TestReconcileLeavesAmbiguousOverpaymentToTheOperator(t *testing.T) {
	setupBinancePayControllerDB(t)
	setTolerance(t, 5)
	insertBinanceOrder(t, "BNP-A", 20, 20.0137, time.Minute)
	insertBinanceOrder(t, "BNP-B", 20, 20.0512, time.Minute)

	reader := &fakeBinanceReader{pay: []service.BinancePayTransaction{
		payRow("M_P_1", "20.10", "111", testBinanceReceiver, "C2C"), // fits both
	}}
	require.NoError(t, reconcileBinancePay(context.Background(), reader, "test"))
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-A"))
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-B"))
	assert.Equal(t, 0, binanceQuotaOf(t, 1))
}

// Exact beats over: when one order matches exactly, an overpayment fit on
// another order is irrelevant.
func TestReconcileExactMatchWinsOverOverpaymentFit(t *testing.T) {
	setupBinancePayControllerDB(t)
	setTolerance(t, 5)
	insertBinanceOrder(t, "BNP-A", 20, 20.0137, time.Minute)
	insertBinanceOrder(t, "BNP-B", 20, 20.0500, time.Minute)

	reader := &fakeBinanceReader{pay: []service.BinancePayTransaction{
		payRow("M_P_1", "20.0500", "111", testBinanceReceiver, "C2C"),
	}}
	require.NoError(t, reconcileBinancePay(context.Background(), reader, "test"))
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-A"))
	assert.Equal(t, common.TopUpStatusSuccess, binanceStatusOf(t, "BNP-B"))
}

// Short payments and overpayments beyond tolerance never auto-credit; a zero
// tolerance turns the feature off entirely.
func TestReconcileNeverAutoCreditsShortOrOversizedPayments(t *testing.T) {
	setupBinancePayControllerDB(t)
	setTolerance(t, 5)
	insertBinanceOrder(t, "BNP-A", 20, 20.0137, time.Minute)

	reader := &fakeBinanceReader{pay: []service.BinancePayTransaction{
		payRow("M_P_short", "20.0000", "111", testBinanceReceiver, "C2C"), // 0.0137 short
		payRow("M_P_big", "25.0000", "222", testBinanceReceiver, "C2C"),   // +25%
	}}
	require.NoError(t, reconcileBinancePay(context.Background(), reader, "test"))
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-A"))

	setTolerance(t, 0)
	reader.pay = append(reader.pay, payRow("M_P_over", "20.02", "333", testBinanceReceiver, "C2C"))
	require.NoError(t, reconcileBinancePay(context.Background(), reader, "test"))
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-A"), "tolerance 0 disables overpayment matching")
	assert.Equal(t, 0, binanceQuotaOf(t, 1))
}

// One overpayment must not settle two orders: after it credits the first
// fitting order, it is spent.
func TestReconcileOneTransactionSettlesOneOrder(t *testing.T) {
	setupBinancePayControllerDB(t)
	setTolerance(t, 5)
	insertBinanceOrder(t, "BNP-A", 20, 20.0137, time.Minute)

	reader := &fakeBinanceReader{pay: []service.BinancePayTransaction{
		payRow("M_P_1", "20.0137", "111", testBinanceReceiver, "C2C"),
	}}
	require.NoError(t, reconcileBinancePay(context.Background(), reader, "test"))
	insertBinanceOrder(t, "BNP-C", 20, 20.0100, time.Minute) // M_P_1 would "fit" as +0.0037
	require.NoError(t, reconcileBinancePay(context.Background(), reader, "test"))
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-C"))
	assert.Equal(t, 20*500_000, binanceQuotaOf(t, 1))
}

// Binance.US has no Binance Pay: the reconciler must not even ask for Pay
// history there, only deposits, and the wallet gets no Pay ID to show.
func TestBinanceUSReconcilesDepositsOnlyAndHidesPayId(t *testing.T) {
	setupBinancePayControllerDB(t)
	prev := setting.BinancePayPlatform
	setting.BinancePayPlatform = "binance.us"
	t.Cleanup(func() { setting.BinancePayPlatform = prev })

	insertBinanceOrder(t, "BNP-A", 20, 20.0137, time.Minute)
	reader := &fakeBinanceReader{
		pay: []service.BinancePayTransaction{payRow("M_P_1", "20.0137", "111", testBinanceReceiver, "C2C")},
		deposits: []service.BinanceDeposit{
			{Id: "1", Amount: "20.0137", Coin: "USDT", Network: "TRX", Status: 1, Address: "TXYZ", TxId: "tx-1", InsertTime: time.Now().UnixMilli()},
		},
	}
	require.NoError(t, reconcileBinancePay(context.Background(), reader, "test"))
	assert.Equal(t, 0, reader.calls, "Pay history must not be requested on Binance.US")
	assert.Equal(t, common.TopUpStatusSuccess, binanceStatusOf(t, "BNP-A"))

	view := binancePayOrderView(model.GetTopUpByTradeNo("BNP-A"))
	assert.Equal(t, "", view["receiver_id"])
	assert.Equal(t, "binance.us", view["platform"])
	assert.Equal(t, "https://api.binance.us", setting.BinancePayApiBaseURL())
}

func TestBinancePayEnabledNeedsAWayToPay(t *testing.T) {
	prevEnabled, prevKey, prevSecret, prevReceiver, prevAddrs, prevPlatform :=
		setting.BinancePayEnabled, setting.BinancePayApiKey, setting.BinancePaySecretKey,
		setting.BinancePayReceiverId, setting.BinancePayDepositAddresses, setting.BinancePayPlatform
	t.Cleanup(func() {
		setting.BinancePayEnabled, setting.BinancePayApiKey, setting.BinancePaySecretKey = prevEnabled, prevKey, prevSecret
		setting.BinancePayReceiverId, setting.BinancePayDepositAddresses, setting.BinancePayPlatform = prevReceiver, prevAddrs, prevPlatform
	})
	if !isPaymentComplianceConfirmed() {
		t.Skip("compliance not confirmed in this test process; gate is covered elsewhere")
	}
	setting.BinancePayEnabled, setting.BinancePayApiKey, setting.BinancePaySecretKey = true, "k", "s"

	setting.BinancePayPlatform, setting.BinancePayReceiverId, setting.BinancePayDepositAddresses = "binance.com", "123", ""
	assert.True(t, isBinancePayTopUpEnabled())
	setting.BinancePayPlatform, setting.BinancePayReceiverId, setting.BinancePayDepositAddresses = "binance.us", "123", ""
	assert.False(t, isBinancePayTopUpEnabled(), "a Pay ID alone is useless on Binance.US")
	setting.BinancePayDepositAddresses = `[{"network":"TRX","address":"T1"}]`
	assert.True(t, isBinancePayTopUpEnabled())
}
