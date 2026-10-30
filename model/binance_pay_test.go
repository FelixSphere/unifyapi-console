package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupBinancePayTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Tenant{}, &User{}, &TopUp{}, &Log{}, &BinancePayTransaction{}, &PartnershipCustomer{}))

	previousDB, previousLogDB := DB, LOG_DB
	DB, LOG_DB = db, db
	t.Cleanup(func() { DB, LOG_DB = previousDB, previousLogDB })

	previousQPU := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = previousQPU })
}

func insertBinancePayUser(t *testing.T, id int) {
	t.Helper()
	require.NoError(t, DB.Create(&User{Id: id, Username: "bnp_user", Status: common.UserStatusEnabled, Quota: 0}).Error)
}

func insertPendingBinancePayOrder(t *testing.T, tradeNo string, userId int, amount int64, money float64) *TopUp {
	t.Helper()
	row := &TopUp{
		UserId:          userId,
		Amount:          amount,
		Money:           money,
		TradeNo:         tradeNo,
		PaymentMethod:   PaymentMethodBinancePay,
		PaymentProvider: PaymentProviderBinancePay,
		CreateTime:      common.GetTimestamp(),
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, row.Insert())
	return row
}

func userQuota(t *testing.T, id int) int {
	t.Helper()
	var u User
	require.NoError(t, DB.First(&u, id).Error)
	return u.Quota
}

// TestReserveBinancePayMoneyIsUniqueAmongPendingOrders: the amount *is* the
// order reference, so two pending orders must never share one.
func TestReserveBinancePayMoneyIsUniqueAmongPendingOrders(t *testing.T) {
	setupBinancePayTestDB(t)
	insertBinancePayUser(t, 1)

	price := decimal.NewFromFloat(100)
	seen := map[string]struct{}{}
	for i := 0; i < 150; i++ {
		money, err := ReserveBinancePayMoney(price)
		require.NoError(t, err)

		formatted := money.StringFixed(BinancePayMoneyDecimals)
		_, dup := seen[formatted]
		require.False(t, dup, "amount %s reserved twice", formatted)
		seen[formatted] = struct{}{}

		// Price part intact, suffix strictly inside (0, 1).
		assert.True(t, money.GreaterThan(price), formatted)
		assert.True(t, money.LessThan(price.Add(decimal.NewFromInt(1))), formatted)
		assert.Equal(t, 4, len(strings.Split(formatted, ".")[1]))

		f, _ := money.Float64()
		insertPendingBinancePayOrder(t, "BNP-"+formatted, 1, 100, f)
	}
}

func TestReserveBinancePayMoneyRejectsNonPositivePrice(t *testing.T) {
	setupBinancePayTestDB(t)
	_, err := ReserveBinancePayMoney(decimal.Zero)
	require.Error(t, err)
}

func TestFormatAndCompareBinancePayMoney(t *testing.T) {
	assert.Equal(t, "100.0473", FormatBinancePayMoney(100.0473))
	assert.Equal(t, "7.5000", FormatBinancePayMoney(7.5))
	// Float noise from the DB column must not defeat a match.
	assert.True(t, BinancePayMoneyEqual(decimal.NewFromFloat(100.04730000000001), decimal.RequireFromString("100.0473")))
	assert.False(t, BinancePayMoneyEqual(decimal.RequireFromString("100.0473"), decimal.RequireFromString("100.0474")))
}

// TestRechargeBinancePayCreditsOnceAndPinsTheTransaction is the money test:
// one transaction credits exactly one order exactly once, whatever order the
// reconciler replays things in.
func TestRechargeBinancePayCreditsOnceAndPinsTheTransaction(t *testing.T) {
	setupBinancePayTestDB(t)
	insertBinancePayUser(t, 7)
	insertPendingBinancePayOrder(t, "BNP-A", 7, 20, 20.0137)
	insertPendingBinancePayOrder(t, "BNP-B", 7, 20, 20.0138)

	txn := &BinancePayTransaction{
		TransactionId: "pay:M_P_1",
		Source:        BinancePayTxnSourcePay,
		Amount:        "20.0137",
		Currency:      "USDT",
		PayerId:       "12345678",
		TransactTime:  1_700_000_000_000,
	}

	require.NoError(t, RechargeBinancePay("BNP-A", txn, "test"))
	assert.Equal(t, 20*500_000, userQuota(t, 7))

	got := GetTopUpByTradeNo("BNP-A")
	require.NotNil(t, got)
	assert.Equal(t, common.TopUpStatusSuccess, got.Status)
	assert.NotZero(t, got.CompleteTime)

	// Replay on the same, now-settled order: no-op, no double credit.
	require.NoError(t, RechargeBinancePay("BNP-A", txn, "test"))
	assert.Equal(t, 20*500_000, userQuota(t, 7))

	// Same transaction against a *different* pending order: refused, order
	// untouched. This is the case a duplicate poll or a mistaken amount match
	// would otherwise turn into free credit.
	err := RechargeBinancePay("BNP-B", txn, "test")
	require.ErrorIs(t, err, ErrBinancePayTxnUsed)
	assert.Equal(t, 20*500_000, userQuota(t, 7))
	assert.Equal(t, common.TopUpStatusPending, GetTopUpByTradeNo("BNP-B").Status)

	var ledger []BinancePayTransaction
	require.NoError(t, DB.Find(&ledger).Error)
	require.Len(t, ledger, 1)
	assert.Equal(t, "BNP-A", ledger[0].TradeNo)
	assert.Equal(t, "pay:M_P_1", ledger[0].TransactionId)
}

func TestRechargeBinancePayRefusesOtherProvidersAndTerminalOrders(t *testing.T) {
	setupBinancePayTestDB(t)
	insertBinancePayUser(t, 3)

	stripe := &TopUp{UserId: 3, Amount: 5, Money: 5, TradeNo: "ref_x", PaymentMethod: PaymentMethodStripe,
		PaymentProvider: PaymentProviderStripe, CreateTime: common.GetTimestamp(), Status: common.TopUpStatusPending}
	require.NoError(t, stripe.Insert())
	txn := &BinancePayTransaction{TransactionId: "pay:1", Source: BinancePayTxnSourcePay, Amount: "5", Currency: "USDT"}
	require.ErrorIs(t, RechargeBinancePay("ref_x", txn, "test"), ErrPaymentMethodMismatch)

	expired := insertPendingBinancePayOrder(t, "BNP-E", 3, 5, 5.0001)
	expired.Status = common.TopUpStatusExpired
	require.NoError(t, expired.Update())
	require.ErrorIs(t, RechargeBinancePay("BNP-E", txn, "test"), ErrTopUpStatusInvalid)

	require.Error(t, RechargeBinancePay("", txn, "test"))
	require.Error(t, RechargeBinancePay("BNP-E", &BinancePayTransaction{}, "test"))
	assert.Equal(t, 0, userQuota(t, 3))
}

func TestExpireBinancePayTopUpsOnlyTouchesOldPendingRows(t *testing.T) {
	setupBinancePayTestDB(t)
	insertBinancePayUser(t, 1)
	now := common.GetTimestamp()

	old := insertPendingBinancePayOrder(t, "BNP-old", 1, 1, 1.0001)
	old.CreateTime = now - 7200
	require.NoError(t, old.Update())
	fresh := insertPendingBinancePayOrder(t, "BNP-fresh", 1, 1, 1.0002)
	paid := insertPendingBinancePayOrder(t, "BNP-paid", 1, 1, 1.0003)
	paid.CreateTime = now - 7200
	paid.Status = common.TopUpStatusSuccess
	require.NoError(t, paid.Update())

	n, err := ExpireBinancePayTopUps(now - 3600)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
	assert.Equal(t, common.TopUpStatusExpired, GetTopUpByTradeNo("BNP-old").Status)
	assert.Equal(t, common.TopUpStatusPending, GetTopUpByTradeNo(fresh.TradeNo).Status)
	assert.Equal(t, common.TopUpStatusSuccess, GetTopUpByTradeNo("BNP-paid").Status)

	pending, err := GetPendingBinancePayTopUps()
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, "BNP-fresh", pending[0].TradeNo)
}

func TestIsPartnershipCustomerGroup(t *testing.T) {
	setupBinancePayTestDB(t)
	require.NoError(t, DB.Create(&PartnershipCustomer{ProgramId: 1, Name: "Acme", Code: "acme", Group: "acme", Enabled: true}).Error)
	require.NoError(t, DB.Create(&PartnershipCustomer{ProgramId: 1, Name: "Gone", Code: "gone", Group: "gone", Enabled: true, RemovedAt: 1}).Error)
	require.NoError(t, DB.Create(&PartnershipCustomer{ProgramId: 1, Name: "Off", Code: "off", Group: "off", Enabled: true}).Error)
	// gorm skips a zero-value bool on Create when the column has default:true.
	require.NoError(t, DB.Model(&PartnershipCustomer{}).Where("code = ?", "off").Update("enabled", false).Error)

	assert.True(t, IsPartnershipCustomerGroup("acme"))
	assert.False(t, IsPartnershipCustomerGroup("gone"))
	assert.False(t, IsPartnershipCustomerGroup("off"))
	assert.False(t, IsPartnershipCustomerGroup("default"))
	assert.False(t, IsPartnershipCustomerGroup(""))
}

// The settlement branch table must know the new provider; see the header of
// settlement_payments.go.
func TestCreditedQuotaKnowsBinancePay(t *testing.T) {
	previous := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = previous })
	assert.InDelta(t, 20.0, creditedUSD(PaymentProviderBinancePay, 20, 20.0473), 1e-9)
}
