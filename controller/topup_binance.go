package controller

// UNIFYAPI-FORK: Binance Pay top-ups into the operator's personal Binance
// account(s). See setting/payment_binance.go for the account model and
// model/binance_pay.go for the ledger.
//
// Flow: the user asks for N credits on one of the Binance tiles -> we price
// it, reserve a unique stablecoin amount and store a pending TopUp whose
// PaymentMethod names the receiving account -> the wallet shows "send exactly
// X USDT to Pay ID Y" (binance.com) or "to address Z" (either platform) ->
// the reconciler polls each account's own history with its read-only key and
// credits the one pending order whose amount matches. There is no webhook:
// Binance does not notify personal accounts, so the wallet polls the order
// status and each poll may nudge the reconciler, rate limited.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

const (
	// binancePayMatchSlack tolerates a payer who sends before the order row
	// commits, and clock skew between us and Binance.
	binancePayMatchSlack = 15 * time.Minute
	// binancePayReconcileInterval is the background poll cadence.
	binancePayReconcileInterval = 60 * time.Second
	// binancePayReconcileMinGap bounds how often user-triggered status checks
	// may hit Binance. /sapi/v1/pay/transactions weighs 3000 per UID against a
	// 180,000/min budget; one call every 15s uses a third of it at most.
	binancePayReconcileMinGap  = 15 * time.Second
	binancePayReconcileTimeout = 20 * time.Second
	// binancePayPollIntervalSeconds is what the wallet is told to poll at.
	binancePayPollIntervalSeconds = 10
)

// binancePayConfiguredAccounts are the accounts that can take payments right
// now: compliance confirmed, enabled, credible credentials, a way to pay.
func binancePayConfiguredAccounts() []setting.BinancePayAccount {
	if !isPaymentComplianceConfirmed() {
		return nil
	}
	var out []setting.BinancePayAccount
	for _, a := range setting.BinancePayAccounts() {
		if a.Configured() {
			out = append(out, a)
		}
	}
	return out
}

func isBinancePayTopUpEnabled() bool {
	return len(binancePayConfiguredAccounts()) > 0
}

// binancePayAccountForOrder resolves the receiving account of an order from
// its PaymentMethod. Legacy rows (method "binance_pay") are binance.com.
func binancePayAccountForOrder(topUp *model.TopUp) setting.BinancePayAccount {
	if a, ok := setting.BinancePayAccountForMethod(topUp.PaymentMethod); ok {
		return a
	}
	a, _ := setting.BinancePayAccountForPlatform(setting.BinancePayPlatformGlobal)
	return a
}

func binancePayOrderTTL() time.Duration {
	minutes := setting.BinancePayOrderTTLMinutes
	if minutes <= 0 {
		minutes = 60
	}
	return time.Duration(minutes) * time.Minute
}

// getBinancePayPrice is the stablecoin price of `amount` credits before the
// unique suffix: credits x unit price x top-up group ratio x amount discount,
// in decimal all the way. Shared by both accounts.
func getBinancePayPrice(amount float64, group string) decimal.Decimal {
	originalAmount := amount
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		amount = amount / common.QuotaPerUnit
	}
	dAmount := decimal.NewFromFloat(amount)
	dUnit := decimal.NewFromFloat(setting.BinancePayUnitPrice)
	if dUnit.Sign() <= 0 {
		dUnit = decimal.NewFromInt(1)
	}
	ratio := common.GetTopupGroupRatio(group)
	if ratio == 0 {
		ratio = 1
	}
	dRatio := decimal.NewFromFloat(ratio)
	dDiscount := decimal.NewFromInt(1)
	if ds, ok := operation_setting.GetPaymentSetting().AmountDiscount[int(originalAmount)]; ok && ds > 0 {
		dDiscount = decimal.NewFromFloat(ds)
	}
	return dAmount.Mul(dUnit).Mul(dRatio).Mul(dDiscount).Round(2)
}

type BinancePayRequest struct {
	Amount int64 `json:"amount"`
	// Platform selects the receiving account: "binance.com" (default) or
	// "binance.us". The wallet sends the tile's platform.
	Platform string `json:"platform"`
	// PaymentMethod is accepted as an alternative to Platform ("binance_pay"
	// or "binance_pay_us"), matching the tile type.
	PaymentMethod string `json:"payment_method"`
}

func (r BinancePayRequest) platform() string {
	if strings.TrimSpace(r.Platform) != "" {
		return setting.NormalizeBinancePayPlatform(r.Platform)
	}
	if strings.TrimSpace(r.PaymentMethod) != "" {
		return setting.NormalizeBinancePayPlatform(r.PaymentMethod)
	}
	return setting.BinancePayPlatformGlobal
}

func binancePayMinTopUp() int64 {
	if setting.BinancePayMinTopUp < 1 {
		return 1
	}
	return int64(setting.BinancePayMinTopUp)
}

// RequestBinancePayAmount quotes the price (without suffix) for the wallet's
// "Amount to pay" field.
func RequestBinancePayAmount(c *gin.Context) {
	if !isBinancePayTopUpEnabled() {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "Binance Pay 支付未启用"})
		return
	}
	var req BinancePayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}
	if req.Amount < binancePayMinTopUp() {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", binancePayMinTopUp())})
		return
	}
	id := c.GetInt("id")
	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}
	price := getBinancePayPrice(float64(req.Amount), group)
	if price.LessThan(decimal.NewFromFloat(0.01)) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "success", "data": price.StringFixed(2)})
}

// binancePayOrderView is what the wallet renders: the transfer instructions
// for the order's own receiving account, plus status.
func binancePayOrderView(topUp *model.TopUp) gin.H {
	account := binancePayAccountForOrder(topUp)
	expiresAt := topUp.CreateTime + int64(binancePayOrderTTL().Seconds())
	addresses := account.Addresses()
	if addresses == nil {
		addresses = []setting.BinancePayDepositAddress{}
	}
	return gin.H{
		"trade_no":              topUp.TradeNo,
		"status":                topUp.Status,
		"amount":                topUp.Amount,
		"pay_amount":            model.FormatBinancePayMoney(topUp.Money),
		"currency":              setting.GetBinancePayCurrency(),
		"platform":              account.Platform,
		"platform_label":        account.Label(),
		"payment_method":        account.PaymentMethod(),
		"receiver_id":           account.PayIdForPayers(),
		"receiver_nickname":     account.ReceiverNickname,
		"deposit_addresses":     addresses,
		"created_at":            topUp.CreateTime,
		"expires_at":            expiresAt,
		"poll_interval_seconds": binancePayPollIntervalSeconds,
	}
}

// RequestBinancePay creates a pending order on the chosen account and returns
// the exact transfer instructions.
func RequestBinancePay(c *gin.Context) {
	var req BinancePayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}
	if !isPaymentComplianceConfirmed() {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "Binance Pay 支付未启用"})
		return
	}
	account, ok := setting.BinancePayAccountForPlatform(req.platform())
	if !ok || !account.Configured() {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "该 Binance 收款账户未启用"})
		return
	}
	if req.Amount < binancePayMinTopUp() {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", binancePayMinTopUp())})
		return
	}

	id := c.GetInt("id")
	user, err := model.GetUserById(id, false)
	if err != nil || user == nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "用户不存在"})
		return
	}
	group, _ := model.GetUserGroup(id, true)
	price := getBinancePayPrice(float64(req.Amount), group)
	if price.LessThan(decimal.NewFromFloat(0.01)) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}

	// Expire stale orders first so their amounts are free again.
	if _, err := model.ExpireBinancePayTopUps(time.Now().Add(-binancePayOrderTTL()).Unix()); err != nil {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Binance Pay 过期订单清理失败 error=%q", err.Error()))
	}

	// Amounts are unique across BOTH accounts (ReserveBinancePayMoney looks at
	// every pending Binance order), so a transfer can never be ambiguous
	// between platforms either.
	money, err := model.ReserveBinancePayMoney(price)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Binance Pay 金额预留失败 user_id=%d amount=%d error=%q", id, req.Amount, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "暂时无法创建订单，请稍后重试"})
		return
	}

	// Token display mode: normalise Amount to credit units so the credit path
	// does not scale twice (same as Waffo).
	amount := req.Amount
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		amount = int64(float64(req.Amount) / common.QuotaPerUnit)
		if amount < 1 {
			amount = 1
		}
	}

	prefix := "BNP"
	if account.Platform == setting.BinancePayPlatformUS {
		prefix = "BNPUS"
	}
	tradeNo := fmt.Sprintf("%s-%d-%d-%s", prefix, id, time.Now().UnixMilli(), common.GetRandomString(6))
	moneyF, _ := money.Float64()
	topUp := &model.TopUp{
		UserId:          id,
		Amount:          amount,
		Money:           moneyF,
		TradeNo:         tradeNo,
		PaymentMethod:   account.PaymentMethod(),
		PaymentProvider: model.PaymentProviderBinancePay,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	if err := topUp.Insert(); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Binance Pay 创建充值订单失败 user_id=%d trade_no=%s amount=%d error=%q", id, tradeNo, req.Amount, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Binance Pay 充值订单创建成功 platform=%s user_id=%d trade_no=%s amount=%d pay_amount=%s %s",
		account.Platform, id, tradeNo, req.Amount, money.StringFixed(model.BinancePayMoneyDecimals), setting.GetBinancePayCurrency()))

	c.JSON(http.StatusOK, gin.H{"message": "success", "data": binancePayOrderView(topUp)})
}

// GetBinancePayOrderStatus is what the wallet polls. A pending order nudges
// the reconciler (rate limited) so a payer who has just sent the money does
// not wait for the next background tick.
func GetBinancePayOrderStatus(c *gin.Context) {
	tradeNo := strings.TrimSpace(c.Param("trade_no"))
	if tradeNo == "" {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "未提供订单号"})
		return
	}
	id := c.GetInt("id")
	topUp := model.GetTopUpByTradeNo(tradeNo)
	if topUp == nil || topUp.UserId != id || topUp.PaymentProvider != model.PaymentProviderBinancePay {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "订单不存在"})
		return
	}

	if topUp.Status == common.TopUpStatusPending {
		if topUp.CreateTime < time.Now().Add(-binancePayOrderTTL()).Unix() {
			if _, err := model.ExpireBinancePayTopUps(time.Now().Add(-binancePayOrderTTL()).Unix()); err == nil {
				topUp.Status = common.TopUpStatusExpired
			}
		} else if isBinancePayTopUpEnabled() {
			ctx, cancel := context.WithTimeout(c.Request.Context(), binancePayReconcileTimeout)
			reconcileBinancePayThrottled(ctx, c.ClientIP())
			cancel()
			if fresh := model.GetTopUpByTradeNo(tradeNo); fresh != nil {
				topUp = fresh
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "success", "data": binancePayOrderView(topUp)})
}

// ---------------------------------------------------------------------------
// Reconciler
// ---------------------------------------------------------------------------

// binancePayHealth is the last result of talking to each account's platform,
// shown to the operator so "why is nothing being credited" has an answer.
type binancePayHealth struct {
	CheckedAt int64  `json:"checked_at"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	Source    string `json:"source"` // "reconciler" | "test" | "operator"
}

var (
	binancePayReconcileMu   sync.Mutex
	binancePayLastReconcile time.Time
	binancePayHealthMu      sync.RWMutex
	binancePayHealthByPlat  = map[string]binancePayHealth{}
	// binancePayHistoryReaderFactory is swapped by tests.
	binancePayHistoryReaderFactory = func(account setting.BinancePayAccount) service.BinanceHistoryReader {
		return service.NewBinanceClient(account.ApiKey, account.SecretKey, account.ApiBaseURL())
	}
)

func recordBinancePayHealth(platform, source string, err error) {
	h := binancePayHealth{CheckedAt: time.Now().Unix(), OK: err == nil, Source: source}
	if err != nil {
		h.Error = err.Error()
	}
	binancePayHealthMu.Lock()
	binancePayHealthByPlat[platform] = h
	binancePayHealthMu.Unlock()
}

func getBinancePayHealth(platform string) (binancePayHealth, bool) {
	binancePayHealthMu.RLock()
	defer binancePayHealthMu.RUnlock()
	h, ok := binancePayHealthByPlat[platform]
	return h, ok
}

// reconcileBinancePayThrottled runs one reconcile pass over every configured
// account unless another is in flight or one finished less than
// binancePayReconcileMinGap ago.
func reconcileBinancePayThrottled(ctx context.Context, callerIp string) {
	if !binancePayReconcileMu.TryLock() {
		return
	}
	defer binancePayReconcileMu.Unlock()
	if time.Since(binancePayLastReconcile) < binancePayReconcileMinGap {
		return
	}
	binancePayLastReconcile = time.Now()
	reconcileAllBinancePayAccounts(ctx, callerIp)
}

// RunBinancePayReconciler is the background loop started from main. It does
// nothing while no account is configured, so enabling one needs no restart.
func RunBinancePayReconciler() {
	ticker := time.NewTicker(binancePayReconcileInterval)
	defer ticker.Stop()
	for range ticker.C {
		if !isBinancePayTopUpEnabled() {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), binancePayReconcileTimeout)
		func() {
			defer func() {
				if r := recover(); r != nil {
					common.SysError(fmt.Sprintf("Binance Pay reconciler panic: %v", r))
				}
			}()
			if !binancePayReconcileMu.TryLock() {
				return
			}
			defer binancePayReconcileMu.Unlock()
			binancePayLastReconcile = time.Now()
			reconcileAllBinancePayAccounts(ctx, "reconciler")
		}()
		cancel()
	}
}

// binancePayAddressRefreshEvery bounds how often the reconciler re-reads an
// account's deposit addresses from Binance. Addresses are stable; this is
// only to self-heal after a key change.
const binancePayAddressRefreshEvery = 30 * time.Minute

var (
	binancePayAddrMu          sync.Mutex
	binancePayAddrLastRefresh = map[string]time.Time{}
	// binancePayStoreOption persists a resolved snapshot; tests swap it.
	binancePayStoreOption = func(key, value string) error { return model.UpdateOption(key, value) }
)

// refreshBinancePayAddresses reads, from Binance, the account's own deposit
// address for every selected network and stores the result as the account's
// address snapshot. This is what makes "where customers send money" and
// "which account the key reads" the same account by construction. It
// returns the resolved addresses; on any read error nothing is written, so a
// transient failure never blanks a working configuration.
func refreshBinancePayAddresses(ctx context.Context, account setting.BinancePayAccount, reader service.BinanceHistoryReader) ([]setting.BinancePayDepositAddress, error) {
	networks := account.Networks()
	if len(networks) == 0 {
		return nil, nil
	}
	currency := setting.GetBinancePayCurrency()
	resolved := make([]setting.BinancePayDepositAddress, 0, len(networks))
	for _, network := range networks {
		addr, err := reader.DepositAddress(ctx, currency, network)
		if err != nil {
			return nil, fmt.Errorf("deposit address %s/%s: %w", currency, network, err)
		}
		resolved = append(resolved, setting.BinancePayDepositAddress{Network: network, Address: strings.TrimSpace(addr.Address)})
	}
	encoded, err := common.Marshal(resolved)
	if err != nil {
		return nil, err
	}
	if string(encoded) != strings.TrimSpace(account.DepositAddresses) {
		if err := binancePayStoreOption(account.AddressesOptionKey(), string(encoded)); err != nil {
			return nil, fmt.Errorf("store addresses: %w", err)
		}
		logger.LogInfo(ctx, fmt.Sprintf("Binance Pay 收款地址已从币安同步 platform=%s networks=%v", account.Platform, networks))
	}
	binancePayAddrMu.Lock()
	binancePayAddrLastRefresh[account.Platform] = time.Now()
	binancePayAddrMu.Unlock()
	return resolved, nil
}

func binancePayAddressesStale(platform string) bool {
	binancePayAddrMu.Lock()
	defer binancePayAddrMu.Unlock()
	return time.Since(binancePayAddrLastRefresh[platform]) > binancePayAddressRefreshEvery
}

// reconcileAllBinancePayAccounts runs one pass per configured account. An
// error on one account (bad key, region block) must not stop the other.
func reconcileAllBinancePayAccounts(ctx context.Context, callerIp string) {
	for _, account := range binancePayConfiguredAccounts() {
		reader := binancePayHistoryReaderFactory(account)
		if binancePayAddressesStale(account.Platform) {
			if _, err := refreshBinancePayAddresses(ctx, account, reader); err != nil {
				logger.LogWarn(ctx, fmt.Sprintf("Binance Pay 收款地址同步失败 platform=%s error=%q", account.Platform, err.Error()))
			} else if fresh, ok := setting.BinancePayAccountForPlatform(account.Platform); ok {
				account = fresh // match against the addresses just resolved
			}
		}
		err := reconcileBinancePay(ctx, account, reader, callerIp)
		recordBinancePayHealth(account.Platform, "reconciler", err)
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("Binance Pay 对账失败 platform=%s error=%q", account.Platform, err.Error()))
		}
	}
}

// binancePayCandidate is an incoming transaction normalised from either
// history source.
type binancePayCandidate struct {
	txn       model.BinancePayTransaction
	amount    decimal.Decimal
	currency  string
	timeMs    int64
	payerName string
	network   string
}

// pendingBinancePayOrdersFor returns the unpaid orders that belong to one
// account, oldest first.
func pendingBinancePayOrdersFor(account setting.BinancePayAccount) ([]*model.TopUp, error) {
	all, err := model.GetPendingBinancePayTopUps()
	if err != nil {
		return nil, err
	}
	method := account.PaymentMethod()
	out := make([]*model.TopUp, 0, len(all))
	for _, o := range all {
		if o.PaymentMethod == method {
			out = append(out, o)
		}
	}
	return out, nil
}

// reconcileBinancePay expires stale orders, then matches each of this
// account's pending orders to an incoming transaction in its history.
func reconcileBinancePay(ctx context.Context, account setting.BinancePayAccount, reader service.BinanceHistoryReader, callerIp string) error {
	ttl := binancePayOrderTTL()
	if n, err := model.ExpireBinancePayTopUps(time.Now().Add(-ttl).Unix()); err != nil {
		return fmt.Errorf("expire orders: %w", err)
	} else if n > 0 {
		logger.LogInfo(ctx, fmt.Sprintf("Binance Pay 已过期订单 count=%d", n))
	}

	pending, err := pendingBinancePayOrdersFor(account)
	if err != nil {
		return fmt.Errorf("load pending: %w", err)
	}
	if len(pending) == 0 {
		return nil
	}

	now := time.Now()
	oldest := pending[0].CreateTime
	for _, p := range pending {
		if p.CreateTime < oldest {
			oldest = p.CreateTime
		}
	}
	startMs := time.Unix(oldest, 0).Add(-binancePayMatchSlack).UnixMilli()
	if minStart := now.Add(-service.BinanceHistoryMaxWindow).UnixMilli(); startMs < minStart {
		startMs = minStart
	}
	endMs := now.UnixMilli()

	candidates, err := collectBinancePayCandidates(ctx, account, reader, startMs, endMs)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		return nil
	}

	matched := 0
	settled := map[string]bool{}
	for _, cand := range candidates {
		order, reason := pickBinancePayOrder(pending, cand, settled)
		if order == nil {
			if reason != "" {
				logger.LogWarn(ctx, fmt.Sprintf("Binance Pay 收款未自动匹配 platform=%s transaction_id=%s amount=%s %s reason=%s",
					account.Platform, cand.txn.TransactionId, cand.amount.String(), cand.currency, reason))
			}
			continue
		}
		txn := cand.txn
		LockOrder(order.TradeNo)
		err := model.RechargeBinancePay(order.TradeNo, &txn, callerIp)
		UnlockOrder(order.TradeNo)
		switch {
		case err == nil:
			matched++
			settled[order.TradeNo] = true
			logger.LogInfo(ctx, fmt.Sprintf("Binance Pay 充值成功 platform=%s trade_no=%s transaction_id=%s amount=%s %s expected=%s",
				account.Platform, order.TradeNo, txn.TransactionId, txn.Amount, txn.Currency, model.FormatBinancePayMoney(order.Money)))
		case errors.Is(err, model.ErrBinancePayTxnUsed):
			// This transaction already paid an order (earlier pass, or by hand).
			continue
		case errors.Is(err, model.ErrTopUpStatusInvalid):
			// Settled concurrently; do not offer this order to later candidates.
			settled[order.TradeNo] = true
			continue
		default:
			logger.LogError(ctx, fmt.Sprintf("Binance Pay 充值处理失败 trade_no=%s transaction_id=%s error=%q",
				order.TradeNo, txn.TransactionId, err.Error()))
		}
	}
	if matched > 0 {
		logger.LogInfo(ctx, fmt.Sprintf("Binance Pay 对账完成 platform=%s pending=%d candidates=%d matched=%d",
			account.Platform, len(pending), len(candidates), matched))
	}
	return nil
}

// pickBinancePayOrder decides which pending order an incoming transaction
// pays, or none. Exact amount wins. Otherwise a payer who sent slightly MORE
// than the unique amount is accepted when the overpayment is within the
// configured tolerance and exactly one pending order fits -- two orders in
// the same band cannot be told apart, and a short payment is never
// auto-credited; both are left for the operator's matching view. The
// returned reason is non-empty when there were fitting orders but no safe
// choice.
func pickBinancePayOrder(pending []*model.TopUp, cand binancePayCandidate, settled map[string]bool) (*model.TopUp, string) {
	amt := cand.amount.Round(model.BinancePayMoneyDecimals)
	tolerance := decimal.NewFromFloat(setting.BinancePayOverpayTolerancePercent).Div(decimal.NewFromInt(100))
	var exact, over []*model.TopUp
	for _, order := range pending {
		if settled[order.TradeNo] {
			continue
		}
		notBeforeMs := time.Unix(order.CreateTime, 0).Add(-binancePayMatchSlack).UnixMilli()
		if cand.timeMs > 0 && cand.timeMs < notBeforeMs {
			continue
		}
		want := decimal.NewFromFloat(order.Money).Round(model.BinancePayMoneyDecimals)
		switch {
		case amt.Equal(want):
			exact = append(exact, order)
		case amt.GreaterThan(want) && tolerance.Sign() > 0 && amt.Sub(want).LessThanOrEqual(want.Mul(tolerance)):
			over = append(over, order)
		}
	}
	switch {
	case len(exact) == 1:
		return exact[0], ""
	case len(exact) > 1:
		return nil, fmt.Sprintf("%d pending orders share this exact amount", len(exact))
	case len(over) == 1:
		return over[0], ""
	case len(over) > 1:
		return nil, fmt.Sprintf("overpayment fits %d pending orders", len(over))
	}
	return nil, ""
}

// binancePayLedgerPrefix keeps transaction ids unique across accounts. The
// binance.com account keeps the original bare prefixes so rows written before
// the second account existed still guard against replay.
func binancePayLedgerPrefix(account setting.BinancePayAccount) string {
	if account.Platform == setting.BinancePayPlatformUS {
		return "us:"
	}
	return ""
}

// collectBinancePayCandidates reads one account's history for
// [startMs, endMs] and returns every incoming transaction in the configured
// asset that could have paid an order: refunds, the account's own outgoing
// payments and money received by other accounts are dropped here, once, for
// both the reconciler and the operator's matching view.
func collectBinancePayCandidates(ctx context.Context, account setting.BinancePayAccount, reader service.BinanceHistoryReader, startMs, endMs int64) ([]binancePayCandidate, error) {
	currency := setting.GetBinancePayCurrency()
	receiverId := account.ReceiverId
	prefix := binancePayLedgerPrefix(account)
	candidates := make([]binancePayCandidate, 0, 16)

	if account.SupportsPayTransfers() {
		payRows, err := reader.PayTransactions(ctx, startMs, endMs)
		if err != nil {
			return nil, fmt.Errorf("pay history: %w", err)
		}
		for _, row := range payRows {
			if !strings.EqualFold(row.Currency, currency) {
				continue
			}
			if isBinanceRefundOrderType(row.OrderType) {
				continue
			}
			// Only money that arrived in this account counts. Binance lists the
			// account's outgoing payments in the same history.
			if r := row.ReceiverInfo.BinanceId.String(); r != "" && receiverId != "" && r != receiverId {
				continue
			}
			if p := row.PayerInfo.BinanceId.String(); p != "" && p == receiverId {
				continue
			}
			amt, err := decimal.NewFromString(strings.TrimSpace(row.Amount.String()))
			if err != nil || amt.Sign() <= 0 {
				continue
			}
			txnId := row.TransactionId.String()
			if txnId == "" {
				continue
			}
			candidates = append(candidates, binancePayCandidate{
				txn: model.BinancePayTransaction{
					TransactionId: prefix + "pay:" + txnId,
					Source:        model.BinancePayTxnSourcePay,
					Amount:        amt.String(),
					Currency:      currency,
					PayerId:       row.PayerInfo.BinanceId.String(),
					TransactTime:  row.TransactionTime,
				},
				amount:    amt,
				currency:  currency,
				timeMs:    row.TransactionTime,
				payerName: row.PayerInfo.Name,
			})
		}
	}

	if addresses := account.Addresses(); len(addresses) > 0 {
		ours := make(map[string]struct{}, len(addresses))
		for _, a := range addresses {
			ours[strings.ToLower(a.Address)] = struct{}{}
		}
		deposits, err := reader.DepositHistory(ctx, currency, startMs, endMs)
		if err != nil {
			return nil, fmt.Errorf("deposit history: %w", err)
		}
		for _, d := range deposits {
			if !d.IsCredited() || !strings.EqualFold(d.Coin, currency) {
				continue
			}
			if _, ok := ours[strings.ToLower(strings.TrimSpace(d.Address))]; !ok {
				continue
			}
			amt, err := decimal.NewFromString(strings.TrimSpace(d.Amount.String()))
			if err != nil || amt.Sign() <= 0 {
				continue
			}
			id := strings.TrimSpace(d.TxId)
			if id == "" {
				id = d.Id.String()
			}
			if id == "" {
				continue
			}
			candidates = append(candidates, binancePayCandidate{
				txn: model.BinancePayTransaction{
					TransactionId: prefix + "deposit:" + id,
					Source:        model.BinancePayTxnSourceDeposit,
					Amount:        amt.String(),
					Currency:      currency,
					PayerId:       d.Network,
					TransactTime:  d.InsertTime,
				},
				amount:   amt,
				currency: currency,
				timeMs:   d.InsertTime,
				network:  d.Network,
			})
		}
	}
	return candidates, nil
}

func isBinanceRefundOrderType(orderType string) bool {
	switch strings.ToUpper(strings.TrimSpace(orderType)) {
	case "PAY_REFUND", "C2C_HOLDING_RF", "CRYPTO_BOX_RF":
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// GetTopUpInfo integration
// ---------------------------------------------------------------------------

// binancePayInfoFields is what GetTopUpInfo exposes for this gateway.
func binancePayInfoFields(enabled bool, recommended bool) gin.H {
	accounts := make([]gin.H, 0, 2)
	if enabled {
		for _, a := range binancePayConfiguredAccounts() {
			accounts = append(accounts, gin.H{
				"platform":       a.Platform,
				"label":          a.Label(),
				"payment_method": a.PaymentMethod(),
				"has_pay_id":     a.PayIdForPayers() != "",
				"has_addresses":  len(a.Addresses()) > 0,
			})
		}
	}
	return gin.H{
		"enable_binance_pay_topup": enabled,
		"binance_pay_min_topup":    int(binancePayMinTopUp()),
		"binance_pay_recommended":  enabled && recommended,
		"binance_pay_currency":     setting.GetBinancePayCurrency(),
		"binance_pay_unit_price":   setting.BinancePayUnitPrice,
		"binance_pay_accounts":     accounts,
	}
}

// binancePayMethodEntries are the wallet's payment-method tiles, one per
// configured account, binance.com first.
func binancePayMethodEntries() []map[string]string {
	accounts := binancePayConfiguredAccounts()
	out := make([]map[string]string, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, map[string]string{
			"name":      a.Label(),
			"type":      a.PaymentMethod(),
			"color":     "#F0B90B",
			"min_topup": strconv.Itoa(int(binancePayMinTopUp())),
		})
	}
	return out
}

// isBinancePayMethodType reports whether a wallet tile type is one of ours.
func isBinancePayMethodType(t string) bool {
	return t == setting.BinancePayMethodGlobal || t == setting.BinancePayMethodUS
}
