package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const (
	testBinanceReceiver = "34355667"
	testBinanceKey      = "uuMAzHfMkidM7MATrq3KVgVpECAF7YwBjxFMBkQz4coxRkpIYXsNsgfw6lYyWfHw"
	testBinanceSecret   = "cmIgXGjSG9ELedXxntXhbjWGoSymFMqcZEKtY40Zm3BK2C1wCpL9T0obhGhp9QSD"
)

// setupBinancePayControllerDB gives every test a fresh SQLite database and
// both accounts configured with plausible credentials: binance.com with a
// Pay ID and a TRX address, Binance.US with a TRX address.
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

	snapshot := []*string{&setting.BinancePayApiKey, &setting.BinancePaySecretKey, &setting.BinancePayReceiverId, &setting.BinancePayDepositAddresses,
		&setting.BinancePayUSApiKey, &setting.BinancePayUSSecretKey, &setting.BinancePayUSDepositAddresses, &setting.BinancePayCurrency}
	saved := make([]string, len(snapshot))
	for i, p := range snapshot {
		saved[i] = *p
	}
	prevEnabled, prevUSEnabled, prevTTL, prevTol := setting.BinancePayEnabled, setting.BinancePayUSEnabled, setting.BinancePayOrderTTLMinutes, setting.BinancePayOverpayTolerancePercent
	t.Cleanup(func() {
		for i, p := range snapshot {
			*p = saved[i]
		}
		setting.BinancePayEnabled, setting.BinancePayUSEnabled, setting.BinancePayOrderTTLMinutes, setting.BinancePayOverpayTolerancePercent = prevEnabled, prevUSEnabled, prevTTL, prevTol
	})

	setting.BinancePayEnabled, setting.BinancePayApiKey, setting.BinancePaySecretKey = true, testBinanceKey, testBinanceSecret
	setting.BinancePayReceiverId = testBinanceReceiver
	setting.BinancePayDepositAddresses = `[{"network":"TRX","address":"TXYZ"}]`
	setting.BinancePayUSEnabled, setting.BinancePayUSApiKey, setting.BinancePayUSSecretKey = true, testBinanceKey, testBinanceSecret
	setting.BinancePayUSDepositAddresses = `[{"network":"TRX","address":"TUSA"}]`
	setting.BinancePayDepositNetworks, setting.BinancePayUSDepositNetworks = "", ""
	t.Cleanup(func() { setting.BinancePayDepositNetworks, setting.BinancePayUSDepositNetworks = "", "" })
	// Persisting a snapshot goes through model.UpdateOption in production,
	// which drags in the whole option/config machinery; here it just sets the
	// setting variable, which is the part the code under test reads back.
	prevStore := binancePayStoreOption
	binancePayStoreOption = func(key, value string) error {
		switch key {
		case "BinancePayDepositAddresses":
			setting.BinancePayDepositAddresses = value
		case "BinancePayUSDepositAddresses":
			setting.BinancePayUSDepositAddresses = value
		default:
			return errors.New("unexpected option " + key)
		}
		return nil
	}
	t.Cleanup(func() { binancePayStoreOption = prevStore })
	// Address refresh is exercised explicitly; keep the reconciler from
	// rewriting the snapshots the tests set up.
	binancePayAddrMu.Lock()
	for _, p := range []string{setting.BinancePayPlatformGlobal, setting.BinancePayPlatformUS} {
		binancePayAddrLastRefresh[p] = time.Now()
	}
	binancePayAddrMu.Unlock()
	t.Cleanup(func() {
		binancePayAddrMu.Lock()
		binancePayAddrLastRefresh = map[string]time.Time{}
		binancePayAddrMu.Unlock()
	})
	setting.BinancePayCurrency = "USDT"
	setting.BinancePayOrderTTLMinutes = 60
	setting.BinancePayOverpayTolerancePercent = 5

	// The wallet gate: every gateway hides until the operator confirmed the
	// payment compliance terms.
	ps := operation_setting.GetPaymentSetting()
	prevPS := *ps
	ps.ComplianceConfirmed = true
	ps.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
	t.Cleanup(func() { *ps = prevPS })

	require.NoError(t, db.Create(&model.User{Id: 1, Username: "payer", Status: common.UserStatusEnabled}).Error)
}

func comAccount() setting.BinancePayAccount {
	a, _ := setting.BinancePayAccountForPlatform(setting.BinancePayPlatformGlobal)
	return a
}

func usAccount() setting.BinancePayAccount {
	a, _ := setting.BinancePayAccountForPlatform(setting.BinancePayPlatformUS)
	return a
}

func insertBinanceOrder(t *testing.T, tradeNo string, amount int64, money float64, createdAgo time.Duration) {
	t.Helper()
	insertBinanceOrderFor(t, comAccount(), tradeNo, amount, money, createdAgo)
}

func insertBinanceOrderFor(t *testing.T, account setting.BinancePayAccount, tradeNo string, amount int64, money float64, createdAgo time.Duration) {
	t.Helper()
	row := &model.TopUp{
		UserId: 1, Amount: amount, Money: money, TradeNo: tradeNo,
		PaymentMethod: account.PaymentMethod(), PaymentProvider: model.PaymentProviderBinancePay,
		CreateTime: time.Now().Add(-createdAgo).Unix(), Status: common.TopUpStatusPending,
	}
	require.NoError(t, row.Insert())
}

type fakeBinanceReader struct {
	pay       []service.BinancePayTransaction
	deposits  []service.BinanceDeposit
	addresses map[string]string // network -> address the account owns
	calls     int
	addrCalls int
}

func (f *fakeBinanceReader) DepositAddress(_ context.Context, coin, network string) (service.BinanceDepositAddress, error) {
	f.addrCalls++
	if a, ok := f.addresses[network]; ok {
		return service.BinanceDepositAddress{Coin: coin, Address: a}, nil
	}
	return service.BinanceDepositAddress{}, errors.New("no address for network " + network)
}

func (f *fakeBinanceReader) PayTransactions(context.Context, int64, int64) ([]service.BinancePayTransaction, error) {
	f.calls++
	return f.pay, nil
}

func (f *fakeBinanceReader) DepositHistory(context.Context, string, int64, int64) ([]service.BinanceDeposit, error) {
	return f.deposits, nil
}

// useFakeBinanceReaders routes each platform to its own fake.
func useFakeBinanceReaders(t *testing.T, readers map[string]service.BinanceHistoryReader) {
	t.Helper()
	previous := binancePayHistoryReaderFactory
	binancePayHistoryReaderFactory = func(account setting.BinancePayAccount) service.BinanceHistoryReader {
		if r, ok := readers[account.Platform]; ok {
			return r
		}
		return &fakeBinanceReader{}
	}
	t.Cleanup(func() { binancePayHistoryReaderFactory = previous })
}

func payRow(id, amount, payer, receiver string, orderType string) service.BinancePayTransaction {
	return service.BinancePayTransaction{
		OrderType: orderType, TransactionId: service.BinanceFlexString(id), TransactionTime: time.Now().UnixMilli(),
		Amount: service.BinanceFlexString(amount), Currency: "USDT",
		PayerInfo:    service.BinancePayParty{BinanceId: service.BinanceFlexString(payer)},
		ReceiverInfo: service.BinancePayParty{BinanceId: service.BinanceFlexString(receiver)},
	}
}

func depositRow(id, amount, address string, status int) service.BinanceDeposit {
	return service.BinanceDeposit{Id: service.BinanceFlexString(id), Amount: service.BinanceFlexString(amount), Coin: "USDT",
		Network: "TRX", Status: status, Address: address, TxId: "tx-" + id, InsertTime: time.Now().UnixMilli()}
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

func reconcileCom(t *testing.T, reader service.BinanceHistoryReader) {
	t.Helper()
	require.NoError(t, reconcileBinancePay(context.Background(), comAccount(), reader, "test"))
}

func adminContext(t *testing.T, method, target, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", 99) // the administrator
	return c, rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return out
}

// ---------------------------------------------------------------------------
// Reconciler
// ---------------------------------------------------------------------------

// The amount is the reference. Only the order with that exact amount is
// credited, and replaying the same history cannot credit it again.
func TestReconcileBinancePayMatchesExactAmountOnce(t *testing.T) {
	setupBinancePayControllerDB(t)
	insertBinanceOrder(t, "BNP-A", 20, 20.0137, time.Minute)
	insertBinanceOrder(t, "BNP-B", 20, 20.0138, time.Minute)

	reader := &fakeBinanceReader{pay: []service.BinancePayTransaction{
		payRow("M_P_1", "20.0137", "111", testBinanceReceiver, "C2C"),
		payRow("M_P_2", "20.0000", "222", testBinanceReceiver, "C2C"), // nobody's order
	}}
	reconcileCom(t, reader)
	assert.Equal(t, common.TopUpStatusSuccess, binanceStatusOf(t, "BNP-A"))
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-B"))
	assert.Equal(t, 20*500_000, binanceQuotaOf(t, 1))

	reconcileCom(t, reader)
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-B"))
	assert.Equal(t, 20*500_000, binanceQuotaOf(t, 1))
}

// A transfer out of the account, a refund, another asset, or money received
// by some other account must never settle an order, even at the right amount.
func TestReconcileBinancePayIgnoresOutgoingRefundsAndForeignRows(t *testing.T) {
	setupBinancePayControllerDB(t)
	insertBinanceOrder(t, "BNP-A", 20, 20.0137, time.Minute)

	other := payRow("M_P_4", "20.0137", "111", testBinanceReceiver, "C2C")
	other.Currency = "BUSD"
	reader := &fakeBinanceReader{pay: []service.BinancePayTransaction{
		payRow("M_P_1", "20.0137", testBinanceReceiver, "999", "C2C"),
		payRow("M_P_2", "20.0137", "111", testBinanceReceiver, "PAY_REFUND"),
		payRow("M_P_3", "20.0137", "111", "424242", "C2C"),
		other,
	}}
	reconcileCom(t, reader)
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-A"))
	assert.Equal(t, 0, binanceQuotaOf(t, 1))
}

func TestReconcileBinancePayAcceptsCreditedDepositToOurAddress(t *testing.T) {
	setupBinancePayControllerDB(t)
	insertBinanceOrder(t, "BNP-A", 20, 20.0137, time.Minute)
	insertBinanceOrder(t, "BNP-B", 30, 30.0001, time.Minute)
	insertBinanceOrder(t, "BNP-C", 40, 40.0001, time.Minute)

	reader := &fakeBinanceReader{deposits: []service.BinanceDeposit{
		depositRow("1", "20.0137", "TXYZ", 1),
		depositRow("2", "30.0001", "TXYZ", 0),   // not credited yet
		depositRow("3", "40.0001", "TOTHER", 1), // someone else's address
	}}
	reconcileCom(t, reader)
	assert.Equal(t, common.TopUpStatusSuccess, binanceStatusOf(t, "BNP-A"))
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-B"))
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-C"))

	var ledger []model.BinancePayTransaction
	require.NoError(t, model.DB.Find(&ledger).Error)
	require.Len(t, ledger, 1)
	assert.Equal(t, "deposit:tx-1", ledger[0].TransactionId, "binance.com keeps the legacy prefix")
}

func TestReconcileBinancePayExpiresStaleOrdersBeforeMatching(t *testing.T) {
	setupBinancePayControllerDB(t)
	insertBinanceOrder(t, "BNP-old", 20, 20.0137, 3*time.Hour)
	reader := &fakeBinanceReader{pay: []service.BinancePayTransaction{payRow("M_P_1", "20.0137", "111", testBinanceReceiver, "C2C")}}
	reconcileCom(t, reader)
	assert.Equal(t, common.TopUpStatusExpired, binanceStatusOf(t, "BNP-old"))
	assert.Equal(t, 0, binanceQuotaOf(t, 1))
	assert.Equal(t, 0, reader.calls, "nothing pending, Binance not asked")
}

func TestReconcileBinancePayIgnoresPaymentsOlderThanTheOrder(t *testing.T) {
	setupBinancePayControllerDB(t)
	insertBinanceOrder(t, "BNP-A", 20, 20.0137, time.Minute)
	stale := payRow("M_P_1", "20.0137", "111", testBinanceReceiver, "C2C")
	stale.TransactionTime = time.Now().Add(-2 * time.Hour).UnixMilli()
	reconcileCom(t, &fakeBinanceReader{pay: []service.BinancePayTransaction{stale}})
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-A"))
}

// Overpayment: within tolerance and unambiguous -> credited at the order's
// amount; ambiguous, short or oversized -> left for the operator.
func TestReconcileOverpaymentRules(t *testing.T) {
	setupBinancePayControllerDB(t)
	insertBinanceOrder(t, "BNP-A", 20, 20.0137, time.Minute)
	insertBinanceOrder(t, "BNP-B", 50, 50.4321, time.Minute)

	reconcileCom(t, &fakeBinanceReader{pay: []service.BinancePayTransaction{
		payRow("M_P_round", "20.02", "111", testBinanceReceiver, "C2C"), // +0.0063 over A, fits only A
	}})
	assert.Equal(t, common.TopUpStatusSuccess, binanceStatusOf(t, "BNP-A"))
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-B"))
	assert.Equal(t, 20*500_000, binanceQuotaOf(t, 1), "credited the order amount, not the overpayment")
	var ledger []model.BinancePayTransaction
	require.NoError(t, model.DB.Find(&ledger).Error)
	require.Len(t, ledger, 1)
	assert.Equal(t, "20.02", ledger[0].Amount)

	// Two orders in the same band: nobody is credited.
	insertBinanceOrder(t, "BNP-C", 20, 20.0500, time.Minute)
	insertBinanceOrder(t, "BNP-D", 20, 20.0600, time.Minute)
	reconcileCom(t, &fakeBinanceReader{pay: []service.BinancePayTransaction{
		payRow("M_P_amb", "20.10", "111", testBinanceReceiver, "C2C"),
	}})
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-C"))
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-D"))

	// Exact beats over.
	reconcileCom(t, &fakeBinanceReader{pay: []service.BinancePayTransaction{
		payRow("M_P_exact", "20.0600", "111", testBinanceReceiver, "C2C"),
	}})
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-C"))
	assert.Equal(t, common.TopUpStatusSuccess, binanceStatusOf(t, "BNP-D"))

	// Short and oversized never auto-credit; tolerance 0 disables overpayment.
	reconcileCom(t, &fakeBinanceReader{pay: []service.BinancePayTransaction{
		payRow("M_P_short", "20.0400", "111", testBinanceReceiver, "C2C"), // 0.01 short of C
		payRow("M_P_big", "25.0000", "222", testBinanceReceiver, "C2C"),   // +25%
	}})
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-C"))
	setting.BinancePayOverpayTolerancePercent = 0
	reconcileCom(t, &fakeBinanceReader{pay: []service.BinancePayTransaction{
		payRow("M_P_over", "20.0510", "333", testBinanceReceiver, "C2C"),
	}})
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-C"))
}

// Two accounts, two histories: each account's orders are settled only from
// its own platform, and a transaction id from one platform can never collide
// with the other's ledger rows.
func TestReconcileKeepsAccountsApart(t *testing.T) {
	setupBinancePayControllerDB(t)
	insertBinanceOrderFor(t, comAccount(), "BNP-com", 20, 20.0137, time.Minute)
	insertBinanceOrderFor(t, usAccount(), "BNPUS-us", 20, 20.0138, time.Minute)

	comReader := &fakeBinanceReader{
		// binance.com sees a deposit of the US order's amount on ITS address: not the US order's money.
		deposits: []service.BinanceDeposit{depositRow("c1", "20.0138", "TXYZ", 1)},
		pay:      []service.BinancePayTransaction{payRow("M_P_1", "20.0137", "111", testBinanceReceiver, "C2C")},
	}
	usReader := &fakeBinanceReader{deposits: []service.BinanceDeposit{
		depositRow("u1", "20.0138", "TUSA", 1),
		depositRow("u2", "20.0137", "TUSA", 1), // the com order's amount, on the US address: not the com order's money
	}}
	useFakeBinanceReaders(t, map[string]service.BinanceHistoryReader{
		setting.BinancePayPlatformGlobal: comReader,
		setting.BinancePayPlatformUS:     usReader,
	})

	reconcileAllBinancePayAccounts(context.Background(), "test")

	assert.Equal(t, common.TopUpStatusSuccess, binanceStatusOf(t, "BNP-com"))
	assert.Equal(t, common.TopUpStatusSuccess, binanceStatusOf(t, "BNPUS-us"))
	assert.Equal(t, 40*500_000, binanceQuotaOf(t, 1))
	assert.Equal(t, 0, usReader.calls, "Binance.US has no Pay history to ask for")

	var ledger []model.BinancePayTransaction
	require.NoError(t, model.DB.Order("trade_no").Find(&ledger).Error)
	require.Len(t, ledger, 2)
	byTrade := map[string]string{}
	for _, row := range ledger {
		byTrade[row.TradeNo] = row.TransactionId
	}
	assert.Equal(t, "pay:M_P_1", byTrade["BNP-com"])
	assert.Equal(t, "us:deposit:tx-u1", byTrade["BNPUS-us"])

	h, ok := getBinancePayHealth(setting.BinancePayPlatformUS)
	require.True(t, ok)
	assert.True(t, h.OK)
}

// A failing account must not stop the other one from being reconciled.
func TestReconcileOneAccountFailingDoesNotBlockTheOther(t *testing.T) {
	setupBinancePayControllerDB(t)
	insertBinanceOrderFor(t, usAccount(), "BNPUS-us", 20, 20.0138, time.Minute)
	useFakeBinanceReaders(t, map[string]service.BinanceHistoryReader{
		setting.BinancePayPlatformGlobal: failingReader{},
		setting.BinancePayPlatformUS:     &fakeBinanceReader{deposits: []service.BinanceDeposit{depositRow("u1", "20.0138", "TUSA", 1)}},
	})
	insertBinanceOrderFor(t, comAccount(), "BNP-com", 20, 20.0137, time.Minute)

	reconcileAllBinancePayAccounts(context.Background(), "test")
	assert.Equal(t, common.TopUpStatusSuccess, binanceStatusOf(t, "BNPUS-us"))
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-com"))
	h, ok := getBinancePayHealth(setting.BinancePayPlatformGlobal)
	require.True(t, ok)
	assert.False(t, h.OK)
	assert.Contains(t, h.Error, "-2008")
}

type failingReader struct{}

func (failingReader) PayTransactions(context.Context, int64, int64) ([]service.BinancePayTransaction, error) {
	return nil, errors.New(`binance /sapi/v1/pay/transactions: http 400: {"code":-2008,"msg":"Invalid Api-Key ID."}`)
}

func (failingReader) DepositAddress(context.Context, string, string) (service.BinanceDepositAddress, error) {
	return service.BinanceDepositAddress{}, errors.New(`binance /sapi/v1/capital/deposit/address: http 400: {"code":-2008,"msg":"Invalid Api-Key ID."}`)
}

func (failingReader) DepositHistory(context.Context, string, int64, int64) ([]service.BinanceDeposit, error) {
	return nil, errors.New(`binance /sapi/v1/capital/deposit/hisrec: http 400: {"code":-2008,"msg":"Invalid Api-Key ID."}`)
}

// ---------------------------------------------------------------------------
// Orders and wallet info
// ---------------------------------------------------------------------------

func TestOrderViewFollowsTheOrdersAccount(t *testing.T) {
	setupBinancePayControllerDB(t)
	insertBinanceOrderFor(t, comAccount(), "BNP-com", 20, 20.0137, time.Minute)
	insertBinanceOrderFor(t, usAccount(), "BNPUS-us", 20, 20.0138, time.Minute)

	com := binancePayOrderView(model.GetTopUpByTradeNo("BNP-com"))
	assert.Equal(t, "binance.com", com["platform"])
	assert.Equal(t, testBinanceReceiver, com["receiver_id"])
	assert.Equal(t, "binance_pay", com["payment_method"])
	assert.Len(t, com["deposit_addresses"], 1)

	us := binancePayOrderView(model.GetTopUpByTradeNo("BNPUS-us"))
	assert.Equal(t, "binance.us", us["platform"])
	assert.Equal(t, "", us["receiver_id"], "no Pay ID on Binance.US")
	assert.Equal(t, "binance_pay_us", us["payment_method"])
	addrs := us["deposit_addresses"].([]setting.BinancePayDepositAddress)
	require.Len(t, addrs, 1)
	assert.Equal(t, "TUSA", addrs[0].Address)
}

func TestMethodEntriesAndInfoFieldsListConfiguredAccountsOnly(t *testing.T) {
	setupBinancePayControllerDB(t)
	entries := binancePayMethodEntries()
	require.Len(t, entries, 2)
	assert.Equal(t, "binance_pay", entries[0]["type"])
	assert.Contains(t, entries[0]["name"], "binance.com")
	assert.Equal(t, "binance_pay_us", entries[1]["type"])
	assert.Contains(t, entries[1]["name"], "Binance.US")

	// Placeholder credentials take the account off the wallet.
	setting.BinancePayUSApiKey = "1"
	entries = binancePayMethodEntries()
	require.Len(t, entries, 1)
	assert.Equal(t, "binance_pay", entries[0]["type"])

	fields := binancePayInfoFields(true, true)
	assert.Equal(t, true, fields["binance_pay_recommended"])
	assert.Len(t, fields["binance_pay_accounts"], 1)
	fields = binancePayInfoFields(false, true)
	assert.Equal(t, false, fields["binance_pay_recommended"], "a disabled gateway is never recommended")
}

func TestGetBinancePayPriceUsesUnitPriceAndFallsBackOnZero(t *testing.T) {
	prevUnit := setting.BinancePayUnitPrice
	setting.BinancePayUnitPrice = 1.02
	t.Cleanup(func() { setting.BinancePayUnitPrice = prevUnit })
	assert.Equal(t, "20.40", getBinancePayPrice(20, "default").StringFixed(2))
	setting.BinancePayUnitPrice = 0
	assert.Equal(t, "20.00", getBinancePayPrice(20, "default").StringFixed(2))
}

func TestRequestBinancePayAmountRefusesWhenNothingConfigured(t *testing.T) {
	setupBinancePayControllerDB(t)
	setting.BinancePayEnabled, setting.BinancePayUSEnabled = false, false
	c, rec := adminContext(t, http.MethodPost, "/api/user/binance-pay/amount", `{"amount":5}`)
	RequestBinancePayAmount(c)
	assert.Equal(t, "error", decodeBody(t, rec)["message"])
}

// ---------------------------------------------------------------------------
// Operator endpoints
// ---------------------------------------------------------------------------

func TestAdminCandidatesRankByDistanceAndFlagShortPayments(t *testing.T) {
	setupBinancePayControllerDB(t)
	insertBinanceOrder(t, "BNP-A", 20, 20.0137, time.Minute)
	insertBinanceOrder(t, "BNP-Z", 20, 19.0000, time.Minute)
	require.NoError(t, model.RechargeBinancePay("BNP-Z", &model.BinancePayTransaction{
		TransactionId: "pay:M_P_used", Source: model.BinancePayTxnSourcePay, Amount: "19.0000", Currency: "USDT",
	}, "test"))

	reader := &fakeBinanceReader{pay: []service.BinancePayTransaction{
		payRow("M_P_far", "25.0000", "111", testBinanceReceiver, "C2C"),
		payRow("M_P_short", "18.9137", "222", testBinanceReceiver, "C2C"), // 5.5% short -> warn
		payRow("M_P_near", "20.0100", "333", testBinanceReceiver, "C2C"),
		payRow("M_P_out", "20.0137", testBinanceReceiver, "999", "C2C"), // outgoing: dropped
		payRow("M_P_used", "19.0000", "555", testBinanceReceiver, "C2C"),
	}}
	useFakeBinanceReaders(t, map[string]service.BinanceHistoryReader{setting.BinancePayPlatformGlobal: reader})

	c, rec := adminContext(t, http.MethodGet, "/api/user/topup/binance-pay/candidates?trade_no=BNP-A", "")
	AdminListBinancePayCandidates(c)
	body := decodeBody(t, rec)
	require.Equal(t, true, body["success"], rec.Body.String())
	data := body["data"].(map[string]any)
	assert.Equal(t, "20.0137", data["expected_amount"])
	assert.Equal(t, "binance.com", data["platform"])

	cands := data["candidates"].([]any)
	require.Len(t, cands, 4)
	first := cands[0].(map[string]any)
	assert.Equal(t, "pay:M_P_near", first["transaction_id"])
	assert.Equal(t, "-0.0037", first["delta"])
	assert.Equal(t, false, first["warn"])

	byId := map[string]map[string]any{}
	for _, raw := range cands {
		m := raw.(map[string]any)
		byId[m["transaction_id"].(string)] = m
	}
	assert.Equal(t, true, byId["pay:M_P_short"]["warn"])
	assert.Equal(t, "BNP-Z", byId["pay:M_P_used"]["used_by_trade_no"])
}

func TestAdminMatchCreditsFromTheChosenTransactionOnly(t *testing.T) {
	setupBinancePayControllerDB(t)
	insertBinanceOrder(t, "BNP-A", 20, 20.0137, time.Minute)
	useFakeBinanceReaders(t, map[string]service.BinanceHistoryReader{setting.BinancePayPlatformGlobal: &fakeBinanceReader{
		pay: []service.BinancePayTransaction{payRow("M_P_short", "19.9000", "222", testBinanceReceiver, "C2C")},
	}})

	c, rec := adminContext(t, http.MethodPost, "/api/user/topup/binance-pay/match", `{"trade_no":"BNP-A","transaction_id":"pay:invented"}`)
	AdminMatchBinancePay(c)
	assert.Equal(t, false, decodeBody(t, rec)["success"])
	assert.Equal(t, 0, binanceQuotaOf(t, 1))

	c, rec = adminContext(t, http.MethodPost, "/api/user/topup/binance-pay/match", `{"trade_no":"BNP-A","transaction_id":"pay:M_P_short"}`)
	AdminMatchBinancePay(c)
	require.Equal(t, true, decodeBody(t, rec)["success"], rec.Body.String())
	assert.Equal(t, common.TopUpStatusSuccess, binanceStatusOf(t, "BNP-A"))
	assert.Equal(t, 20*500_000, binanceQuotaOf(t, 1))

	var ledger []model.BinancePayTransaction
	require.NoError(t, model.DB.Find(&ledger).Error)
	require.Len(t, ledger, 1)
	assert.Equal(t, "manual:pay", ledger[0].Source)
	assert.Equal(t, "19.9", ledger[0].Amount)

	c, rec = adminContext(t, http.MethodGet, "/api/user/topup/binance-pay/evidence?trade_nos=BNP-A,unknown", "")
	AdminBinancePayEvidence(c)
	ev := decodeBody(t, rec)["data"].(map[string]any)
	require.Contains(t, ev, "BNP-A")
	assert.Equal(t, true, ev["BNP-A"].(map[string]any)["manual"])
	assert.Equal(t, "binance.com", ev["BNP-A"].(map[string]any)["platform"])

	c, rec = adminContext(t, http.MethodPost, "/api/user/topup/binance-pay/match", `{"trade_no":"BNP-A","transaction_id":"pay:M_P_short"}`)
	AdminMatchBinancePay(c)
	assert.Equal(t, false, decodeBody(t, rec)["success"], "completed orders cannot be matched again")
}

func TestAdminMatchUsesTheOrdersOwnAccount(t *testing.T) {
	setupBinancePayControllerDB(t)
	insertBinanceOrderFor(t, usAccount(), "BNPUS-late", 20, 20.0137, 3*time.Hour)
	late := depositRow("u9", "20.0000", "TUSA", 1) // short: manual only
	late.InsertTime = time.Now().Add(-2 * time.Hour).UnixMilli()
	useFakeBinanceReaders(t, map[string]service.BinanceHistoryReader{
		setting.BinancePayPlatformUS:     &fakeBinanceReader{deposits: []service.BinanceDeposit{late}},
		setting.BinancePayPlatformGlobal: failingReader{}, // must not be consulted
	})

	reconcileAllBinancePayAccounts(context.Background(), "test")
	assert.Equal(t, common.TopUpStatusExpired, binanceStatusOf(t, "BNPUS-late"))

	c, rec := adminContext(t, http.MethodGet, "/api/user/topup/binance-pay/candidates?trade_no=BNPUS-late", "")
	AdminListBinancePayCandidates(c)
	data := decodeBody(t, rec)["data"].(map[string]any)
	assert.Equal(t, "binance.us", data["platform"])
	require.Len(t, data["candidates"], 1)

	c, rec = adminContext(t, http.MethodPost, "/api/user/topup/binance-pay/match", `{"trade_no":"BNPUS-late","transaction_id":"us:deposit:tx-u9"}`)
	AdminMatchBinancePay(c)
	require.Equal(t, true, decodeBody(t, rec)["success"], rec.Body.String())
	assert.Equal(t, common.TopUpStatusSuccess, binanceStatusOf(t, "BNPUS-late"), "expired orders may be matched by hand")
	assert.Equal(t, 20*500_000, binanceQuotaOf(t, 1))
}

func TestAdminMatchRefusesATransactionAlreadyUsed(t *testing.T) {
	setupBinancePayControllerDB(t)
	insertBinanceOrder(t, "BNP-A", 20, 20.0137, time.Minute)
	insertBinanceOrder(t, "BNP-B", 20, 20.0138, time.Minute)
	reader := &fakeBinanceReader{pay: []service.BinancePayTransaction{payRow("M_P_1", "20.0137", "111", testBinanceReceiver, "C2C")}}
	useFakeBinanceReaders(t, map[string]service.BinanceHistoryReader{setting.BinancePayPlatformGlobal: reader})
	reconcileCom(t, reader)

	c, rec := adminContext(t, http.MethodPost, "/api/user/topup/binance-pay/match", `{"trade_no":"BNP-B","transaction_id":"pay:M_P_1"}`)
	AdminMatchBinancePay(c)
	body := decodeBody(t, rec)
	assert.Equal(t, false, body["success"])
	assert.Contains(t, body["message"], "已用于另一笔订单")
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-B"))
}

func TestAdminCompleteTopUpRefusesBinancePayOrders(t *testing.T) {
	setupBinancePayControllerDB(t)
	insertBinanceOrder(t, "BNP-A", 20, 20.0137, time.Minute)
	c, rec := adminContext(t, http.MethodPost, "/api/user/topup/complete", `{"trade_no":"BNP-A"}`)
	AdminCompleteTopUp(c)
	body := decodeBody(t, rec)
	assert.Equal(t, false, body["success"])
	assert.Contains(t, body["message"], "匹配")
	assert.Equal(t, 0, binanceQuotaOf(t, 1))
}

// The settings page asks this before the operator wonders why nothing is
// credited: is each account configured, and if not, why; did the last call
// to its platform work.
func TestAdminBinancePayStatusExplainsEachAccount(t *testing.T) {
	setupBinancePayControllerDB(t)
	setting.BinancePayUSApiKey = "1" // the placeholder incident
	insertBinanceOrder(t, "BNP-A", 20, 20.0137, time.Minute)
	recordBinancePayHealth(setting.BinancePayPlatformGlobal, "reconciler", nil)

	c, rec := adminContext(t, http.MethodGet, "/api/option/binance-pay/status", "")
	AdminBinancePayStatus(c)
	data := decodeBody(t, rec)["data"].(map[string]any)
	accounts := data["accounts"].([]any)
	require.Len(t, accounts, 2)

	com := accounts[0].(map[string]any)
	assert.Equal(t, "binance.com", com["platform"])
	assert.Equal(t, true, com["configured"])
	assert.Equal(t, float64(1), com["pending_orders"])
	assert.Equal(t, true, com["last_check"].(map[string]any)["ok"])

	us := accounts[1].(map[string]any)
	assert.Equal(t, false, us["configured"])
	assert.Equal(t, false, us["has_credentials"])
	assert.Contains(t, us["configured_reason"], "64-character")
	assert.Equal(t, float64(1), us["api_key_length"])
}

func TestAdminBinancePayTestReportsWhatBinanceSaid(t *testing.T) {
	setupBinancePayControllerDB(t)
	useFakeBinanceReaders(t, map[string]service.BinanceHistoryReader{
		setting.BinancePayPlatformGlobal: failingReader{},
		setting.BinancePayPlatformUS: &fakeBinanceReader{deposits: []service.BinanceDeposit{depositRow("u1", "5", "TUSA", 1)},
			addresses: map[string]string{"TRX": "TUSA"}},
	})

	c, rec := adminContext(t, http.MethodPost, "/api/option/binance-pay/test", `{"platform":"binance.us"}`)
	AdminBinancePayTest(c)
	body := decodeBody(t, rec)
	require.Equal(t, true, body["success"], rec.Body.String())
	assert.Equal(t, float64(1), body["data"].(map[string]any)["deposits_24h"])
	_, hasPay := body["data"].(map[string]any)["pay_transactions_24h"]
	assert.False(t, hasPay, "no Pay history on Binance.US")

	c, rec = adminContext(t, http.MethodPost, "/api/option/binance-pay/test", `{"platform":"binance.com"}`)
	AdminBinancePayTest(c)
	body = decodeBody(t, rec)
	assert.Equal(t, false, body["success"])
	assert.Contains(t, body["message"], "-2008")
	h, _ := getBinancePayHealth(setting.BinancePayPlatformGlobal)
	assert.False(t, h.OK)
	assert.Equal(t, "test", h.Source)

	setting.BinancePayUSApiKey = "1"
	c, rec = adminContext(t, http.MethodPost, "/api/option/binance-pay/test", `{"platform":"binance.us"}`)
	AdminBinancePayTest(c)
	body = decodeBody(t, rec)
	assert.Equal(t, false, body["success"])
	assert.Contains(t, body["message"], "64")
}

// ---------------------------------------------------------------------------
// Deposit addresses come from Binance, not from a text box
// ---------------------------------------------------------------------------

// The address a customer is told to pay must belong to the account whose
// key does the reading. The refresh replaces a hand-typed snapshot with what
// Binance says, and the reconciler then matches against that.
func TestRefreshReplacesHandTypedAddressesWithTheAccountsOwn(t *testing.T) {
	setupBinancePayControllerDB(t)
	setting.BinancePayUSDepositNetworks = `["TRON","bsc"]` // aliases, mixed case
	setting.BinancePayUSDepositAddresses = `[{"network":"TRX","address":"TOTHER_ACCOUNT"}]`

	reader := &fakeBinanceReader{addresses: map[string]string{"TRX": "TJUgo", "BSC": "0xf46d"}}
	resolved, err := refreshBinancePayAddresses(context.Background(), usAccount(), reader)
	require.NoError(t, err)
	assert.Equal(t, []setting.BinancePayDepositAddress{{Network: "TRX", Address: "TJUgo"}, {Network: "BSC", Address: "0xf46d"}}, resolved)
	assert.Equal(t, 2, reader.addrCalls)

	// Stored, normalised, and now what the account matches against.
	assert.Equal(t, resolved, usAccount().Addresses())
	assert.True(t, usAccount().Configured())

	insertBinanceOrderFor(t, usAccount(), "BNPUS-1", 20, 20.0137, time.Minute)
	reader.deposits = []service.BinanceDeposit{
		depositRow("old", "20.0137", "TOTHER_ACCOUNT", 1), // the other account's address: ignored
		depositRow("new", "20.0137", "TJUgo", 1),
	}
	require.NoError(t, reconcileBinancePay(context.Background(), usAccount(), reader, "test"))
	assert.Equal(t, common.TopUpStatusSuccess, binanceStatusOf(t, "BNPUS-1"))
	var ledger []model.BinancePayTransaction
	require.NoError(t, model.DB.Find(&ledger).Error)
	require.Len(t, ledger, 1)
	assert.Equal(t, "us:deposit:tx-new", ledger[0].TransactionId)
}

// A legacy snapshot with no networks selected still yields networks, so the
// first refresh after the upgrade heals it instead of disabling the account.
func TestNetworksFallBackToLegacySnapshot(t *testing.T) {
	a := setting.BinancePayAccount{Platform: setting.BinancePayPlatformUS,
		DepositAddresses: `[{"network":"TRON","address":"TVBx"},{"network":"BNB","address":"0xd2"},{"network":"TRX","address":"dup"}]`}
	assert.Equal(t, []string{"TRX", "BSC"}, a.Networks())
	a.DepositNetworks = `["ETH"]`
	assert.Equal(t, []string{"ETH"}, a.Networks(), "selected networks win over the snapshot")
}

// A read failure must not blank a working configuration.
func TestRefreshKeepsSnapshotWhenBinanceFails(t *testing.T) {
	setupBinancePayControllerDB(t)
	setting.BinancePayUSDepositNetworks = `["TRX"]`
	before := usAccount().DepositAddresses
	_, err := refreshBinancePayAddresses(context.Background(), usAccount(), failingReader{})
	require.Error(t, err)
	assert.Equal(t, before, usAccount().DepositAddresses)
}

func TestAdminRefreshAddressesEndpoint(t *testing.T) {
	setupBinancePayControllerDB(t)
	setting.BinancePayUSDepositNetworks = `["TRX"]`
	useFakeBinanceReaders(t, map[string]service.BinanceHistoryReader{
		setting.BinancePayPlatformUS: &fakeBinanceReader{addresses: map[string]string{"TRX": "TJUgo"}},
	})
	c, rec := adminContext(t, http.MethodPost, "/api/option/binance-pay/refresh-addresses", `{"platform":"binance.us"}`)
	AdminBinancePayRefreshAddresses(c)
	body := decodeBody(t, rec)
	require.Equal(t, true, body["success"], rec.Body.String())
	addrs := body["data"].(map[string]any)["addresses"].([]any)
	require.Len(t, addrs, 1)
	assert.Equal(t, "TJUgo", addrs[0].(map[string]any)["address"])

	// Without networks the endpoint says so instead of guessing.
	setting.BinancePayUSDepositNetworks, setting.BinancePayUSDepositAddresses = "", ""
	c, rec = adminContext(t, http.MethodPost, "/api/option/binance-pay/refresh-addresses", `{"platform":"binance.us"}`)
	AdminBinancePayRefreshAddresses(c)
	body = decodeBody(t, rec)
	assert.Equal(t, false, body["success"])
	assert.Contains(t, body["message"], "网络")
}

// An unconfigured account has no networks and no addresses. Go serialises a
// nil slice as null, and the settings page reads .length off both -- a null
// there crashed the whole page into the error boundary once, which the
// operator saw as a "500" that no server ever sent.
func TestAdminBinancePayStatusNeverReturnsNullArrays(t *testing.T) {
	setupBinancePayControllerDB(t)
	setting.BinancePayEnabled, setting.BinancePayUSEnabled = false, false
	setting.BinancePayDepositAddresses, setting.BinancePayUSDepositAddresses = "[]", "[]"
	setting.BinancePayDepositNetworks, setting.BinancePayUSDepositNetworks = "", ""

	c, rec := adminContext(t, http.MethodGet, "/api/option/binance-pay/status", "")
	AdminBinancePayStatus(c)
	require.NotContains(t, rec.Body.String(), `"addresses":null`)
	require.NotContains(t, rec.Body.String(), `"networks":null`)

	accounts := decodeBody(t, rec)["data"].(map[string]any)["accounts"].([]any)
	require.Len(t, accounts, 2)
	for _, raw := range accounts {
		account := raw.(map[string]any)
		assert.NotNil(t, account["addresses"], account["platform"])
		assert.NotNil(t, account["networks"], account["platform"])
		assert.Empty(t, account["addresses"])
		assert.Empty(t, account["networks"])
	}
}
