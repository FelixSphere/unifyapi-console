package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// useFakeBinanceReader swaps the history source for the admin endpoints.
func useFakeBinanceReader(t *testing.T, reader service.BinanceHistoryReader) {
	t.Helper()
	previous := binancePayHistoryReaderFactory
	binancePayHistoryReaderFactory = func() service.BinanceHistoryReader { return reader }
	t.Cleanup(func() { binancePayHistoryReaderFactory = previous })

	prevKey, prevSecret := setting.BinancePayApiKey, setting.BinancePaySecretKey
	setting.BinancePayApiKey, setting.BinancePaySecretKey = "k", "s"
	t.Cleanup(func() { setting.BinancePayApiKey, setting.BinancePaySecretKey = prevKey, prevSecret })
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

// TestAdminCandidatesRankByDistanceAndFlagShortPayments: the operator sees the
// account's real incoming transfers around the order, closest amount first,
// with a short payment flagged and an already-used transaction marked.
func TestAdminCandidatesRankByDistanceAndFlagShortPayments(t *testing.T) {
	setupBinancePayControllerDB(t)
	insertBinanceOrder(t, "BNP-A", 20, 20.0137, time.Minute)
	insertBinanceOrder(t, "BNP-Z", 20, 19.0000, time.Minute)

	// 19.0000 already paid BNP-Z; it must show up for BNP-A as used, not offered.
	used := payRow("M_P_used", "19.0000", "555", testBinanceReceiver, "C2C")
	require.NoError(t, model.RechargeBinancePay("BNP-Z", &model.BinancePayTransaction{
		TransactionId: "pay:M_P_used", Source: model.BinancePayTxnSourcePay, Amount: "19.0000", Currency: "USDT",
	}, "test"))

	reader := &fakeBinanceReader{pay: []service.BinancePayTransaction{
		payRow("M_P_far", "25.0000", "111", testBinanceReceiver, "C2C"),
		payRow("M_P_short", "18.9137", "222", testBinanceReceiver, "C2C"), // 1.1 USDT short: 5.5% -> warn
		payRow("M_P_near", "20.0100", "333", testBinanceReceiver, "C2C"),  // 0.0037 short: fine
		payRow("M_P_out", "20.0137", testBinanceReceiver, "999", "C2C"),   // outgoing: dropped
		used,
	}}
	useFakeBinanceReader(t, reader)

	c, rec := adminContext(t, http.MethodGet, "/api/user/topup/binance-pay/candidates?trade_no=BNP-A", "")
	AdminListBinancePayCandidates(c)
	body := decodeBody(t, rec)
	require.Equal(t, true, body["success"], rec.Body.String())
	data := body["data"].(map[string]any)
	assert.Equal(t, "20.0137", data["expected_amount"])

	cands := data["candidates"].([]any)
	require.Len(t, cands, 4, "outgoing transfer must not be offered")
	first := cands[0].(map[string]any)
	assert.Equal(t, "pay:M_P_near", first["transaction_id"])
	assert.Equal(t, "-0.0037", first["delta"])
	assert.Equal(t, false, first["warn"])
	assert.Equal(t, false, first["exact_match"])

	byId := map[string]map[string]any{}
	for _, raw := range cands {
		m := raw.(map[string]any)
		byId[m["transaction_id"].(string)] = m
	}
	assert.Equal(t, true, byId["pay:M_P_short"]["warn"], "a payment more than 5% short is flagged")
	assert.Equal(t, "-1.1000", byId["pay:M_P_short"]["delta"])
	assert.Equal(t, "BNP-Z", byId["pay:M_P_used"]["used_by_trade_no"])
	assert.Nil(t, byId["pay:M_P_near"]["used_by_trade_no"])
}

// TestAdminMatchCreditsFromTheChosenTransactionOnly: the browser only names a
// transaction; the amount and existence come from Binance, and the ledger
// pins it.
func TestAdminMatchCreditsFromTheChosenTransactionOnly(t *testing.T) {
	setupBinancePayControllerDB(t)
	insertBinanceOrder(t, "BNP-A", 20, 20.0137, time.Minute)
	reader := &fakeBinanceReader{pay: []service.BinancePayTransaction{
		payRow("M_P_short", "19.9000", "222", testBinanceReceiver, "C2C"),
	}}
	useFakeBinanceReader(t, reader)

	// A transaction Binance does not know about is refused.
	c, rec := adminContext(t, http.MethodPost, "/api/user/topup/binance-pay/match", `{"trade_no":"BNP-A","transaction_id":"pay:invented"}`)
	AdminMatchBinancePay(c)
	assert.Equal(t, false, decodeBody(t, rec)["success"])
	assert.Equal(t, 0, binanceQuotaOf(t, 1))

	// The real one credits the order at the ORDER's amount (20 credits), and the
	// ledger records what was actually received and who matched it.
	c, rec = adminContext(t, http.MethodPost, "/api/user/topup/binance-pay/match", `{"trade_no":"BNP-A","transaction_id":"pay:M_P_short"}`)
	AdminMatchBinancePay(c)
	body := decodeBody(t, rec)
	require.Equal(t, true, body["success"], rec.Body.String())
	assert.Equal(t, common.TopUpStatusSuccess, binanceStatusOf(t, "BNP-A"))
	assert.Equal(t, 20*500_000, binanceQuotaOf(t, 1))

	var ledger []model.BinancePayTransaction
	require.NoError(t, model.DB.Find(&ledger).Error)
	require.Len(t, ledger, 1)
	assert.Equal(t, "pay:M_P_short", ledger[0].TransactionId)
	assert.Equal(t, "manual:pay", ledger[0].Source)
	assert.Equal(t, "19.9", ledger[0].Amount)

	// Evidence endpoint shows it, flagged manual.
	c, rec = adminContext(t, http.MethodGet, "/api/user/topup/binance-pay/evidence?trade_nos=BNP-A,unknown", "")
	AdminBinancePayEvidence(c)
	ev := decodeBody(t, rec)["data"].(map[string]any)
	require.Contains(t, ev, "BNP-A")
	assert.NotContains(t, ev, "unknown")
	assert.Equal(t, true, ev["BNP-A"].(map[string]any)["manual"])
	assert.Equal(t, "pay:M_P_short", ev["BNP-A"].(map[string]any)["transaction_id"])

	// Matching a completed order again is refused.
	c, rec = adminContext(t, http.MethodPost, "/api/user/topup/binance-pay/match", `{"trade_no":"BNP-A","transaction_id":"pay:M_P_short"}`)
	AdminMatchBinancePay(c)
	assert.Equal(t, false, decodeBody(t, rec)["success"])
	assert.Equal(t, 20*500_000, binanceQuotaOf(t, 1))
}

// A transaction that paid one order can never be matched to another, even by
// an administrator.
func TestAdminMatchRefusesATransactionAlreadyUsed(t *testing.T) {
	setupBinancePayControllerDB(t)
	insertBinanceOrder(t, "BNP-A", 20, 20.0137, time.Minute)
	insertBinanceOrder(t, "BNP-B", 20, 20.0138, time.Minute)
	reader := &fakeBinanceReader{pay: []service.BinancePayTransaction{
		payRow("M_P_1", "20.0137", "111", testBinanceReceiver, "C2C"),
	}}
	useFakeBinanceReader(t, reader)

	require.NoError(t, reconcileBinancePay(context.Background(), reader, "test"))
	assert.Equal(t, common.TopUpStatusSuccess, binanceStatusOf(t, "BNP-A"))

	c, rec := adminContext(t, http.MethodPost, "/api/user/topup/binance-pay/match", `{"trade_no":"BNP-B","transaction_id":"pay:M_P_1"}`)
	AdminMatchBinancePay(c)
	body := decodeBody(t, rec)
	assert.Equal(t, false, body["success"])
	assert.Contains(t, body["message"], "已用于另一笔订单")
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-B"))
	assert.Equal(t, 20*500_000, binanceQuotaOf(t, 1))
}

// An expired order can still be matched by hand -- the customer's money did
// arrive, just late -- but the automatic path never touches it.
func TestAdminMatchAllowsExpiredOrders(t *testing.T) {
	setupBinancePayControllerDB(t)
	insertBinanceOrder(t, "BNP-late", 20, 20.0137, 3*time.Hour)
	late := payRow("M_P_late", "20.0137", "111", testBinanceReceiver, "C2C")
	late.TransactionTime = time.Now().Add(-2 * time.Hour).UnixMilli()
	reader := &fakeBinanceReader{pay: []service.BinancePayTransaction{late}}
	useFakeBinanceReader(t, reader)

	require.NoError(t, reconcileBinancePay(context.Background(), reader, "test"))
	assert.Equal(t, common.TopUpStatusExpired, binanceStatusOf(t, "BNP-late"))

	c, rec := adminContext(t, http.MethodPost, "/api/user/topup/binance-pay/match", `{"trade_no":"BNP-late","transaction_id":"pay:M_P_late"}`)
	AdminMatchBinancePay(c)
	require.Equal(t, true, decodeBody(t, rec)["success"], rec.Body.String())
	assert.Equal(t, common.TopUpStatusSuccess, binanceStatusOf(t, "BNP-late"))
	assert.Equal(t, 20*500_000, binanceQuotaOf(t, 1))
}

// Blind completion is closed for this gateway: the upstream "complete order"
// endpoint refuses Binance Pay orders and points at matching.
func TestAdminCompleteTopUpRefusesBinancePayOrders(t *testing.T) {
	setupBinancePayControllerDB(t)
	insertBinanceOrder(t, "BNP-A", 20, 20.0137, time.Minute)

	c, rec := adminContext(t, http.MethodPost, "/api/user/topup/complete", `{"trade_no":"BNP-A"}`)
	AdminCompleteTopUp(c)
	body := decodeBody(t, rec)
	assert.Equal(t, false, body["success"])
	assert.Contains(t, body["message"], "匹配")
	assert.Equal(t, common.TopUpStatusPending, binanceStatusOf(t, "BNP-A"))
	assert.Equal(t, 0, binanceQuotaOf(t, 1))
}

func TestRequestBinancePayAmountRefusesWhenDisabled(t *testing.T) {
	prev := setting.BinancePayEnabled
	setting.BinancePayEnabled = false
	t.Cleanup(func() { setting.BinancePayEnabled = prev })

	c, rec := adminContext(t, http.MethodPost, "/api/user/binance-pay/amount", `{"amount":5}`)
	RequestBinancePayAmount(c)
	body := decodeBody(t, rec)
	assert.Equal(t, "error", body["message"])
}
