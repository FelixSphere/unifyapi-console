package controller

// UNIFYAPI-FORK: the operator side of Binance Pay -- proof of receipt.
//
// The automatic reconciler only credits an order when a transaction of
// exactly its amount is in the receiving account's history, and it records
// which one. These endpoints make that evidence visible, and replace blind
// "complete order" for this gateway with "match this order to that
// transaction": the administrator sees the account's real incoming transfers
// around the order, picks one, and the ledger pins it so it can never settle a
// second order. Nothing here trusts an amount sent by the browser; the chosen
// transaction is re-read from Binance before any quota moves.

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
		out[tradeNo] = binancePayEvidenceView{
			TradeNo:       tradeNo,
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

// binancePayCandidatesForOrder reads the account's history around the order
// and ranks every incoming transaction by how close it is to the expected
// amount. Transactions that already paid an order are returned too, marked.
func binancePayCandidatesForOrder(ctx context.Context, reader service.BinanceHistoryReader, order *model.TopUp) ([]binancePayCandidateView, []binancePayCandidate, error) {
	startMs := time.Unix(order.CreateTime, 0).Add(-binancePayMatchSlack).UnixMilli()
	endMs := time.Unix(order.CreateTime, 0).Add(binancePayCandidateWindow).UnixMilli()
	if now := time.Now().UnixMilli(); endMs > now {
		endMs = now
	}
	candidates, err := collectBinancePayCandidates(ctx, reader, startMs, endMs)
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
	if strings.TrimSpace(setting.BinancePayApiKey) == "" || strings.TrimSpace(setting.BinancePaySecretKey) == "" {
		common.ApiErrorMsg(c, "Binance Pay 未配置 API Key，无法读取收款记录")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), binancePayReconcileTimeout)
	defer cancel()
	views, _, err := binancePayCandidatesForOrder(ctx, binancePayHistoryReaderFactory(), order)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("Binance Pay 读取收款记录失败 trade_no=%s error=%q", order.TradeNo, err.Error()))
		common.ApiErrorMsg(c, "读取币安收款记录失败: "+err.Error())
		return
	}
	common.ApiSuccess(c, gin.H{
		"trade_no":        order.TradeNo,
		"status":          order.Status,
		"user_id":         order.UserId,
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
	ctx, cancel := context.WithTimeout(c.Request.Context(), binancePayReconcileTimeout)
	defer cancel()
	_, candidates, err := binancePayCandidatesForOrder(ctx, binancePayHistoryReaderFactory(), order)
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
	logger.LogInfo(ctx, fmt.Sprintf("Binance Pay 管理员匹配成功 admin_id=%d trade_no=%s transaction_id=%s amount=%s expected=%s",
		adminId, order.TradeNo, chosen.TransactionId, chosen.Amount, model.FormatBinancePayMoney(order.Money)))
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": binancePayOrderView(model.GetTopUpByTradeNo(order.TradeNo))})
}
