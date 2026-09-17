package controller

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const testBinanceReceiver = "34355667"

func setupBinancePayControllerDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Tenant{}, &model.User{}, &model.TopUp{}, &model.Log{}, &model.BinancePayTransaction{}))

	previousDB, previousLogDB := model.DB, model.LOG_DB
	model.DB, model.LOG_DB = db, db
	t.Cleanup(func() { model.DB, model.LOG_DB = previousDB, previousLogDB })

	previousQPU := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = previousQPU })

	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedisEnabled })

	prevReceiver, prevCurrency, prevTTL, prevAddrs := setting.BinancePayReceiverId, setting.BinancePayCurrency, setting.BinancePayOrderTTLMinutes, setting.BinancePayDepositAddresses
	setting.BinancePayReceiverId = testBinanceReceiver
	setting.BinancePayCurrency = "USDT"
	setting.BinancePayOrderTTLMinutes = 60
	setting.BinancePayDepositAddresses = `[{"network":"TRX","address":"TXYZ"}]`
	t.Cleanup(func() {
		setting.BinancePayReceiverId, setting.BinancePayCurrency, setting.BinancePayOrderTTLMinutes, setting.BinancePayDepositAddresses = prevReceiver, prevCurrency, prevTTL, prevAddrs
	})

	require.NoError(t, db.Create(&model.User{Id: 1, Username: "payer", Status: common.UserStatusEnabled}).Error)
}

func insertBinanceOrder(t *testing.T, tradeNo string, amount int64, money float64, createdAgo time.Duration) {
	t.Helper()
	row := &model.TopUp{
		UserId: 1, Amount: amount, Money: money, TradeNo: tradeNo,
		PaymentMethod: model.PaymentMethodBinancePay, PaymentProvider: model.PaymentProviderBinancePay,
		CreateTime: time.Now().Add(-createdAgo).Unix(), Status: common.TopUpStatusPending,
	}
	require.NoError(t, row.Insert())
}

type fakeBinanceReader struct {
	pay      []service.BinancePayTransaction
	deposits []service.BinanceDeposit
	calls    int
}

func (f *fakeBinanceReader) PayTransactions(context.Context, int64, int64) ([]service.BinancePayTransaction, error) {
	f.calls++
	return f.pay, nil
}

func (f *fakeBinanceReader) DepositHistory(context.Context, string, int64, int64) ([]service.BinanceDeposit, error) {
	return f.deposits, nil
}

func payRow(id, amount, payer, receiver string, orderType string) service.BinancePayTransaction {
	return service.BinancePayTransaction{
		OrderType: orderType, TransactionId: service.BinanceFlexString(id), TransactionTime: time.Now().UnixMilli(),
		Amount: service.BinanceFlexString(amount), Currency: "USDT",
		PayerInfo:    service.BinancePayParty{BinanceId: service.BinanceFlexString(payer)},
		ReceiverInfo: service.BinancePayParty{BinanceId: service.BinanceFlexString(receiver)},
	}
}

func binanceQuotaOf(t *testing.T, id int) int {
	t.Helper()
	var u model.User
	require.NoError(t, model.DB.First(&u, id).Error)
	return u.Quota
}

func binanceStatusOf(t *testing.T, tradeNo string) string {
	t.Helper()
	row := model.GetTopUpByTradeNo(tradeNo)
	require.NotNil(t, row)
	return row.Status
}

// TestReconcileBinancePayMatchesExactAmountOnce: the amount is the reference.
// Only the order with that exact amount is credited, and replaying the same
// history cannot credit it again.
func TestReconcileBinancePayMatchesExactAmountOnce(t *testing.T) {
	setupBinancePayControllerDB(t)
	insertBinanceOrder(t, "BNP-A", 20, 20.0137, time.Minute)
	insertBinanceOrder(t, "BNP-B", 20, 20.0138, time.Minute)

	reader := &fakeBinanceReader{pay: []service.BinancePayTransaction{
		payRow("M_P_1", "20.0137", "111", testBinanceReceiver, "C2C"),
		payRow("M_P_2", "20.0000", "222", testBinanceReceiver, "C2C"), // nobody's order
	}}

	require.NoError(t, reconcileBinancePay(context.Background(), reader, "test"))
	assert.Equal(t, common.TopUpStatusSuccess, binanceStatusOf(t, "BNP-A"))
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-B"))
	assert.Equal(t, 20*500_000, binanceQuotaOf(t, 1))

	// Same history again: the ledger pins M_P_1 to BNP-A; B stays unpaid.
	require.NoError(t, reconcileBinancePay(context.Background(), reader, "test"))
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-B"))
	assert.Equal(t, 20*500_000, binanceQuotaOf(t, 1))
}

// A transfer *out* of the account, a refund, another asset, or money received
// by some other account must never settle an order, even at the right amount.
func TestReconcileBinancePayIgnoresOutgoingRefundsAndForeignRows(t *testing.T) {
	setupBinancePayControllerDB(t)
	insertBinanceOrder(t, "BNP-A", 20, 20.0137, time.Minute)

	other := payRow("M_P_4", "20.0137", "111", testBinanceReceiver, "C2C")
	other.Currency = "BUSD"
	reader := &fakeBinanceReader{pay: []service.BinancePayTransaction{
		payRow("M_P_1", "20.0137", testBinanceReceiver, "999", "C2C"),        // we paid someone
		payRow("M_P_2", "20.0137", "111", testBinanceReceiver, "PAY_REFUND"), // refund
		payRow("M_P_3", "20.0137", "111", "424242", "C2C"),                   // received by another account
		other,
	}}

	require.NoError(t, reconcileBinancePay(context.Background(), reader, "test"))
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-A"))
	assert.Equal(t, 0, binanceQuotaOf(t, 1))
}

func TestReconcileBinancePayAcceptsCreditedDepositToOurAddress(t *testing.T) {
	setupBinancePayControllerDB(t)
	insertBinanceOrder(t, "BNP-A", 20, 20.0137, time.Minute)
	insertBinanceOrder(t, "BNP-B", 30, 30.0001, time.Minute)
	insertBinanceOrder(t, "BNP-C", 40, 40.0001, time.Minute)

	now := time.Now().UnixMilli()
	reader := &fakeBinanceReader{deposits: []service.BinanceDeposit{
		{Id: "1", Amount: "20.0137", Coin: "USDT", Network: "TRX", Status: 1, Address: "TXYZ", TxId: "tx-1", InsertTime: now},
		{Id: "2", Amount: "30.0001", Coin: "USDT", Network: "TRX", Status: 0, Address: "TXYZ", TxId: "tx-2", InsertTime: now}, // not credited yet
		{Id: "3", Amount: "40.0001", Coin: "USDT", Network: "TRX", Status: 1, Address: "TOTHER", TxId: "tx-3", InsertTime: now},
	}}

	require.NoError(t, reconcileBinancePay(context.Background(), reader, "test"))
	assert.Equal(t, common.TopUpStatusSuccess, binanceStatusOf(t, "BNP-A"))
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-B"))
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-C"))
	assert.Equal(t, 20*500_000, binanceQuotaOf(t, 1))

	var ledger []model.BinancePayTransaction
	require.NoError(t, model.DB.Find(&ledger).Error)
	require.Len(t, ledger, 1)
	assert.Equal(t, "deposit:tx-1", ledger[0].TransactionId)
	assert.Equal(t, model.BinancePayTxnSourceDeposit, ledger[0].Source)
}

func TestReconcileBinancePayExpiresStaleOrdersBeforeMatching(t *testing.T) {
	setupBinancePayControllerDB(t)
	insertBinanceOrder(t, "BNP-old", 20, 20.0137, 3*time.Hour)

	reader := &fakeBinanceReader{pay: []service.BinancePayTransaction{
		payRow("M_P_1", "20.0137", "111", testBinanceReceiver, "C2C"),
	}}
	require.NoError(t, reconcileBinancePay(context.Background(), reader, "test"))
	assert.Equal(t, common.TopUpStatusExpired, binanceStatusOf(t, "BNP-old"))
	assert.Equal(t, 0, binanceQuotaOf(t, 1))
	// Nothing pending, so Binance was not even asked.
	assert.Equal(t, 0, reader.calls)
}

func TestReconcileBinancePayIgnoresPaymentsOlderThanTheOrder(t *testing.T) {
	setupBinancePayControllerDB(t)
	insertBinanceOrder(t, "BNP-A", 20, 20.0137, time.Minute)

	stale := payRow("M_P_1", "20.0137", "111", testBinanceReceiver, "C2C")
	stale.TransactionTime = time.Now().Add(-2 * time.Hour).UnixMilli()
	reader := &fakeBinanceReader{pay: []service.BinancePayTransaction{stale}}

	require.NoError(t, reconcileBinancePay(context.Background(), reader, "test"))
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-A"))
}

func TestGetBinancePayPriceUsesUnitPriceGroupRatioAndDiscount(t *testing.T) {
	prevUnit := setting.BinancePayUnitPrice
	setting.BinancePayUnitPrice = 1.02
	t.Cleanup(func() { setting.BinancePayUnitPrice = prevUnit })

	// Default group ratio 1, no discount configured for 20.
	assert.Equal(t, "20.40", getBinancePayPrice(20, "default").StringFixed(2))

	setting.BinancePayUnitPrice = 0 // misconfigured: falls back to 1, never to a free top-up
	assert.Equal(t, "20.00", getBinancePayPrice(20, "default").StringFixed(2))
}

func TestBinancePayMethodEntryAndInfoFields(t *testing.T) {
	prevMin := setting.BinancePayMinTopUp
	setting.BinancePayMinTopUp = 5
	t.Cleanup(func() { setting.BinancePayMinTopUp = prevMin })

	entry := binancePayMethodEntry()
	assert.Equal(t, model.PaymentMethodBinancePay, entry["type"])
	assert.Equal(t, "5", entry["min_topup"])

	fields := binancePayInfoFields(true, true)
	assert.Equal(t, true, fields["binance_pay_recommended"])
	fields = binancePayInfoFields(false, true)
	assert.Equal(t, false, fields["binance_pay_recommended"], "a disabled gateway is never recommended")
}
