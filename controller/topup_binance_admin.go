package controller

// UNIFYAPI-FORK: the operator side of Binance Pay -- proof of receipt and
// health.
//
// The automatic reconciler only credits an order when a transaction of
// exactly (or acceptably above) its amount is in the receiving account's
// history, and it records which one. These endpoints make that evidence
// visible, replace blind "complete order" for this gateway with "match this
// order to that transaction", and tell the operator whether each account's
// key actually works. Nothing here trusts an amount sent by the browser; the
// chosen transaction is re-read from Binance before any quota moves.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// binancePayCandidateWindow is how far after an order was created the
// operator's view still looks for its payment. A payer who sends a wrong
// amount usually notices the same day; the window is wide so a slow one is
// still found.
const binancePayCandidateWindow = 7 * 24 * time.Hour

// binancePayDeltaWarnPercent marks a candidate whose amount is off by more
// than this share of the expected amount, so a short payment is not accepted
// by reflex.
const binancePayDeltaWarnPercent = 5.0

type binancePayCandidateView struct {
	TransactionId string `json:"transaction_id"`
	Source        string `json:"source"`
	Amount        string `json:"amount"`
	Currency      string `json:"currency"`
	// Delta is amount - expected, as a string at gateway precision.
	Delta        string  `json:"delta"`
	DeltaPercent float64 `json:"delta_percent"`
	ExactMatch   bool    `json:"exact_match"`
	Warn         bool    `json:"warn"`
	PayerId      string  `json:"payer_id"`
	PayerName    string  `json:"payer_name"`
	Network      string  `json:"network"`
	TransactTime int64   `json:"transact_time"`
	// UsedByTradeNo is set when this transaction already paid an order; it
	// is listed (greyed out) so the operator sees why it is not offered.
	UsedByTradeNo string `json:"used_by_trade_no,omitempty"`
}

type binancePayEvidenceView struct {
	TradeNo       string `json:"trade_no"`
	Platform      string `json:"platform"`
	TransactionId string `json:"transaction_id"`
	Source        string `json:"source"`
	Amount        string `json:"amount"`
	Currency      string `json:"currency"`
	PayerId       string `json:"payer_id"`
	TransactTime  int64  `json:"transact_time"`
	MatchedAt     int64  `json:"matched_at"`
	Manual        bool   `json:"manual"`
}

// AdminBinancePayEvidence returns, for each requested order, the Binance
// transaction that paid it. GET ?trade_nos=a,b,c (max 100).
func AdminBinancePayEvidence(c *gin.Context) {
	raw := strings.Split(c.Query("trade_nos"), ",")
	tradeNos := make([]string, 0, len(raw))
	for _, t := range raw {
		if t = strings.TrimSpace(t); t != "" {
			tradeNos = append(tradeNos, t)
		}
	}
	if len(tradeNos) == 0 {
		common.ApiSuccess(c, map[string]binancePayEvidenceView{})
		return
	}
	if len(tradeNos) > 100 {
		tradeNos = tradeNos[:100]
	}
	rows, err := model.GetBinancePayTransactionsByTradeNos(tradeNos)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	out := make(map[string]binancePayEvidenceView, len(rows))
	for tradeNo, row := range rows {
		platform := setting.BinancePayPlatformGlobal
		if strings.HasPrefix(row.TransactionId, "us:") {
			platform = setting.BinancePayPlatformUS
		}
		out[tradeNo] = binancePayEvidenceView{
			TradeNo:       tradeNo,
			Platform:      platform,
			TransactionId: row.TransactionId,
			Source:        row.Source,
			Amount:        row.Amount,
			Currency:      row.Currency,
			PayerId:       row.PayerId,
			TransactTime:  row.TransactTime,
			MatchedAt:     row.CreatedAt,
			Manual:        strings.HasPrefix(row.Source, model.BinancePayTxnSourceManual),
		}
	}
	common.ApiSuccess(c, out)
}

// loadBinancePayOrderForOperator fetches an order the operator may match:
// a Binance Pay order that is pending or expired.
func loadBinancePayOrderForOperator(tradeNo string) (*model.TopUp, error) {
	tradeNo = strings.TrimSpace(tradeNo)
	if tradeNo == "" {
		return nil, errors.New("未提供订单号")
	}
	order := model.GetTopUpByTradeNo(tradeNo)
	if order == nil {
		return nil, errors.New("订单不存在")
	}
	if order.PaymentProvider != model.PaymentProviderBinancePay {
		return nil, errors.New("不是 Binance Pay 订单")
	}
	if order.Status == common.TopUpStatusSuccess {
		return nil, errors.New("订单已完成")
	}
	if order.Status != common.TopUpStatusPending && order.Status != common.TopUpStatusExpired {
		return nil, errors.New("订单状态不允许匹配")
	}
	return order, nil
}

// binancePayCandidatesForOrder reads the order's account history around the
// order and ranks every incoming transaction by how close it is to the
// expected amount. Transactions that already paid an order are returned too,
// marked.
func binancePayCandidatesForOrder(ctx context.Context, account setting.BinancePayAccount, reader service.BinanceHistoryReader, order *model.TopUp) ([]binancePayCandidateView, []binancePayCandidate, error) {
	startMs := time.Unix(order.CreateTime, 0).Add(-binancePayMatchSlack).UnixMilli()
	endMs := time.Unix(order.CreateTime, 0).Add(binancePayCandidateWindow).UnixMilli()
	if now := time.Now().UnixMilli(); endMs > now {
		endMs = now
	}
	candidates, err := collectBinancePayCandidates(ctx, account, reader, startMs, endMs)
	if err != nil {
		return nil, nil, err
	}

	ids := make([]string, 0, len(candidates))
	for _, cand := range candidates {
		ids = append(ids, cand.txn.TransactionId)
	}
	used, err := model.GetUsedBinancePayTransactionIds(ids)
	if err != nil {
		return nil, nil, err
	}

	expected := decimal.NewFromFloat(order.Money).Round(model.BinancePayMoneyDecimals)
	views := make([]binancePayCandidateView, 0, len(candidates))
	for _, cand := range candidates {
		delta := cand.amount.Round(model.BinancePayMoneyDecimals).Sub(expected)
		pct := 0.0
		if expected.Sign() > 0 {
			pct, _ = delta.Div(expected).Mul(decimal.NewFromInt(100)).Float64()
		}
		views = append(views, binancePayCandidateView{
			TransactionId: cand.txn.TransactionId,
			Source:        cand.txn.Source,
			Amount:        cand.amount.String(),
			Currency:      cand.currency,
			Delta:         delta.StringFixed(model.BinancePayMoneyDecimals),
			DeltaPercent:  pct,
			ExactMatch:    delta.IsZero(),
			Warn:          pct < -binancePayDeltaWarnPercent || pct > binancePayDeltaWarnPercent,
			PayerId:       cand.txn.PayerId,
			PayerName:     cand.payerName,
			Network:       cand.network,
			TransactTime:  cand.timeMs,
			UsedByTradeNo: used[cand.txn.TransactionId],
		})
	}
	// Closest amount first; among equals, most recent first.
	sort.SliceStable(views, func(i, j int) bool {
		di := decimal.RequireFromString(views[i].Delta).Abs()
		dj := decimal.RequireFromString(views[j].Delta).Abs()
		if !di.Equal(dj) {
			return di.LessThan(dj)
		}
		return views[i].TransactTime > views[j].TransactTime
	})
	return views, candidates, nil
}

// AdminListBinancePayCandidates GET ?trade_no= lists the incoming
// transactions that could have paid the order.
func AdminListBinancePayCandidates(c *gin.Context) {
	order, err := loadBinancePayOrderForOperator(c.Query("trade_no"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	account := binancePayAccountForOrder(order)
	if !account.HasCredentials() {
		common.ApiErrorMsg(c, fmt.Sprintf("%s 账户未配置有效的 API Key，无法读取收款记录", account.Label()))
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), binancePayReconcileTimeout)
	defer cancel()
	views, _, err := binancePayCandidatesForOrder(ctx, account, binancePayHistoryReaderFactory(account), order)
	recordBinancePayHealth(account.Platform, "operator", err)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("Binance Pay 读取收款记录失败 trade_no=%s platform=%s error=%q", order.TradeNo, account.Platform, err.Error()))
		common.ApiErrorMsg(c, "读取币安收款记录失败: "+err.Error())
		return
	}
	common.ApiSuccess(c, gin.H{
		"trade_no":        order.TradeNo,
		"status":          order.Status,
		"user_id":         order.UserId,
		"platform":        account.Platform,
		"platform_label":  account.Label(),
		"amount":          order.Amount,
		"expected_amount": model.FormatBinancePayMoney(order.Money),
		"currency":        setting.GetBinancePayCurrency(),
		"created_at":      order.CreateTime,
		"window_end":      order.CreateTime + int64(binancePayCandidateWindow.Seconds()),
		"candidates":      views,
	})
}

type AdminMatchBinancePayRequest struct {
	TradeNo       string `json:"trade_no"`
	TransactionId string `json:"transaction_id"`
}

// AdminMatchBinancePay POST {trade_no, transaction_id} credits the order
// from the chosen transaction. The transaction is re-read from Binance: the
// request only names it, it cannot invent one.
func AdminMatchBinancePay(c *gin.Context) {
	var req AdminMatchBinancePayRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.TradeNo) == "" || strings.TrimSpace(req.TransactionId) == "" {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	order, err := loadBinancePayOrderForOperator(req.TradeNo)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	account := binancePayAccountForOrder(order)
	ctx, cancel := context.WithTimeout(c.Request.Context(), binancePayReconcileTimeout)
	defer cancel()
	_, candidates, err := binancePayCandidatesForOrder(ctx, account, binancePayHistoryReaderFactory(account), order)
	recordBinancePayHealth(account.Platform, "operator", err)
	if err != nil {
		common.ApiErrorMsg(c, "读取币安收款记录失败: "+err.Error())
		return
	}
	var chosen *model.BinancePayTransaction
	for i := range candidates {
		if candidates[i].txn.TransactionId == strings.TrimSpace(req.TransactionId) {
			txn := candidates[i].txn
			chosen = &txn
			break
		}
	}
	if chosen == nil {
		common.ApiErrorMsg(c, "该交易不在账户收款记录中，或不属于本订单的时间窗口")
		return
	}
	chosen.Source = model.BinancePayTxnSourceManual + ":" + chosen.Source

	adminId := c.GetInt("id")
	LockOrder(order.TradeNo)
	defer UnlockOrder(order.TradeNo)
	if err := model.RechargeBinancePayManual(order.TradeNo, chosen, c.ClientIP(), adminId); err != nil {
		if errors.Is(err, model.ErrBinancePayTxnUsed) {
			common.ApiErrorMsg(c, "该交易已用于另一笔订单")
			return
		}
		common.ApiError(c, err)
		return
	}
	logger.LogInfo(ctx, fmt.Sprintf("Binance Pay 管理员匹配成功 admin_id=%d platform=%s trade_no=%s transaction_id=%s amount=%s expected=%s",
		adminId, account.Platform, order.TradeNo, chosen.TransactionId, chosen.Amount, model.FormatBinancePayMoney(order.Money)))
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": binancePayOrderView(model.GetTopUpByTradeNo(order.TradeNo))})
}

// ---------------------------------------------------------------------------
// Account status and connection test (root settings page)
// ---------------------------------------------------------------------------

type binancePayAccountStatusView struct {
	Platform         string            `json:"platform"`
	Label            string            `json:"label"`
	PaymentMethod    string            `json:"payment_method"`
	Enabled          bool              `json:"enabled"`
	HasCredentials   bool              `json:"has_credentials"`
	ApiKeyLength     int               `json:"api_key_length"`
	SecretLength     int               `json:"secret_length"`
	PayId            string            `json:"pay_id"`
	PayIdValid       bool              `json:"pay_id_valid"`
	SupportsPay      bool              `json:"supports_pay"`
	AddressCount     int               `json:"address_count"`
	Configured       bool              `json:"configured"`
	PendingOrders    int               `json:"pending_orders"`
	LastCheck        *binancePayHealth `json:"last_check,omitempty"`
	ConfiguredReason string            `json:"configured_reason"`
}

func binancePayStatusFor(account setting.BinancePayAccount, pendingByMethod map[string]int) binancePayAccountStatusView {
	reason := ""
	switch {
	case !account.Enabled:
		reason = "disabled"
	case !account.HasCredentials():
		reason = "api key or secret is not a 64-character Binance key"
	case account.PayIdForPayers() == "" && len(account.Addresses()) == 0:
		if account.SupportsPayTransfers() {
			reason = "needs a valid Pay ID (6-20 digits) or a deposit address"
		} else {
			reason = "needs a deposit address (Binance.US has no Binance Pay)"
		}
	}
	v := binancePayAccountStatusView{
		Platform:         account.Platform,
		Label:            account.Label(),
		PaymentMethod:    account.PaymentMethod(),
		Enabled:          account.Enabled,
		HasCredentials:   account.HasCredentials(),
		ApiKeyLength:     len(account.ApiKey),
		SecretLength:     len(account.SecretKey),
		PayId:            account.ReceiverId,
		PayIdValid:       account.PayIdForPayers() != "",
		SupportsPay:      account.SupportsPayTransfers(),
		AddressCount:     len(account.Addresses()),
		Configured:       account.Configured(),
		PendingOrders:    pendingByMethod[account.PaymentMethod()],
		ConfiguredReason: reason,
	}
	if h, ok := getBinancePayHealth(account.Platform); ok {
		hh := h
		v.LastCheck = &hh
	}
	return v
}

// AdminBinancePayStatus GET returns both accounts' effective state: whether
// each is configured (and if not, why), how many orders wait on it, and the
// result of the last call to its platform.
func AdminBinancePayStatus(c *gin.Context) {
	pendingByMethod := map[string]int{}
	if pending, err := model.GetPendingBinancePayTopUps(); err == nil {
		for _, o := range pending {
			pendingByMethod[o.PaymentMethod]++
		}
	}
	accounts := setting.BinancePayAccounts()
	out := make([]binancePayAccountStatusView, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, binancePayStatusFor(a, pendingByMethod))
	}
	common.ApiSuccess(c, gin.H{
		"compliance_confirmed": isPaymentComplianceConfirmed(),
		"accounts":             out,
	})
}

type AdminBinancePayTestRequest struct {
	Platform string `json:"platform"`
}

// AdminBinancePayTest POST {platform} makes one read against the platform
// with the STORED credentials and reports what came back. It reads the last
// 24h of deposit history (both platforms) and Pay history (binance.com), so
// a bad key, a wrong platform or a region block shows up here, not in a
// customer's pending order.
func AdminBinancePayTest(c *gin.Context) {
	var req AdminBinancePayTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	account, ok := setting.BinancePayAccountForPlatform(req.Platform)
	if !ok {
		common.ApiErrorMsg(c, "未知平台")
		return
	}
	if !account.HasCredentials() {
		common.ApiErrorMsg(c, "该账户的 API Key / Secret 不是有效的 64 位币安密钥，请先保存正确的密钥")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), binancePayReconcileTimeout)
	defer cancel()
	reader := binancePayHistoryReaderFactory(account)
	endMs := time.Now().UnixMilli()
	startMs := time.Now().Add(-24 * time.Hour).UnixMilli()

	result := gin.H{"platform": account.Platform, "label": account.Label()}
	deposits, err := reader.DepositHistory(ctx, setting.GetBinancePayCurrency(), startMs, endMs)
	if err != nil {
		recordBinancePayHealth(account.Platform, "test", err)
		common.ApiErrorMsg(c, "读取充币记录失败: "+err.Error())
		return
	}
	result["deposits_24h"] = len(deposits)
	if account.SupportsPayTransfers() {
		pays, err := reader.PayTransactions(ctx, startMs, endMs)
		if err != nil {
			recordBinancePayHealth(account.Platform, "test", err)
			common.ApiErrorMsg(c, "读取 Binance Pay 记录失败: "+err.Error())
			return
		}
		result["pay_transactions_24h"] = len(pays)
	}
	recordBinancePayHealth(account.Platform, "test", nil)
	common.ApiSuccess(c, result)
}
