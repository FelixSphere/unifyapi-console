package model

// UNIFYAPI-FORK: persistence for the Binance Pay (personal account) gateway.
//
// A Binance Pay top-up is an ordinary TopUp row (provider binance_pay, Amount
// = dollars of credit, Money = the exact stablecoin amount the payer must
// send). What is specific to this gateway lives here:
//
//   - the unique-amount reservation that lets a reference-less transfer be
//     matched to one order,
//   - the BinancePayTransaction ledger that pins each Binance transaction id
//     to the order it paid, so a transaction can never credit twice, and
//   - the credit path itself, which follows the Waffo/Stripe shape (row lock
//     plus status guard inside one transaction), not epay's.

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	BinancePayTxnSourcePay     = "pay"
	BinancePayTxnSourceDeposit = "deposit"
	// BinancePayTxnSourceManual prefixes the source of a transaction an
	// administrator matched by hand ("manual:pay", "manual:deposit").
	BinancePayTxnSourceManual = "manual"

	// BinancePayMoneyDecimals is the scale of the pay amount. Two decimals are
	// the price, the last two are the per-order identifier.
	BinancePayMoneyDecimals = 4
	binancePaySuffixRange   = 9999 // 0.0001 .. 0.9999
	binancePayReserveTries  = 25
)

var (
	ErrBinancePayTxnUsed           = errors.New("binance transaction already credited")
	ErrBinancePayNoAmountAvailable = errors.New("no unique payment amount available, try again")
)

// BinancePayTransaction records one Binance transaction that paid one order.
// TransactionId is unique: the reconciler inserts it in the same database
// transaction that credits the order, so a replayed or re-polled transaction
// fails the insert before any quota moves.
type BinancePayTransaction struct {
	Id            int    `json:"id" gorm:"primaryKey"`
	TransactionId string `json:"transaction_id" gorm:"type:varchar(128);uniqueIndex;not null"`
	Source        string `json:"source" gorm:"type:varchar(16);not null"`
	TradeNo       string `json:"trade_no" gorm:"type:varchar(255);index;not null"`
	Amount        string `json:"amount" gorm:"type:varchar(64);not null"`
	Currency      string `json:"currency" gorm:"type:varchar(16);not null"`
	PayerId       string `json:"payer_id" gorm:"type:varchar(64);not null;default:''"`
	TransactTime  int64  `json:"transact_time" gorm:"not null;default:0"`
	CreatedAt     int64  `json:"created_at" gorm:"autoCreateTime"`
}

// FormatBinancePayMoney renders a pay amount at exactly the gateway scale, so
// the same value prints the same way in the order, the UI and the match.
func FormatBinancePayMoney(money float64) string {
	return decimal.NewFromFloat(money).Round(BinancePayMoneyDecimals).StringFixed(BinancePayMoneyDecimals)
}

// BinancePayMoneyEqual compares two amounts at the gateway scale.
func BinancePayMoneyEqual(a, b decimal.Decimal) bool {
	return a.Round(BinancePayMoneyDecimals).Equal(b.Round(BinancePayMoneyDecimals))
}

// GetPendingBinancePayTopUps returns every unpaid Binance Pay order, oldest
// first.
func GetPendingBinancePayTopUps() ([]*TopUp, error) {
	var rows []*TopUp
	err := DB.Where("payment_provider = ? AND status = ?", PaymentProviderBinancePay, common.TopUpStatusPending).
		Order("create_time asc").Find(&rows).Error
	return rows, err
}

// ExpireBinancePayTopUps marks pending Binance Pay orders created before
// cutoff as expired. Returns how many rows changed.
func ExpireBinancePayTopUps(cutoff int64) (int64, error) {
	res := DB.Model(&TopUp{}).
		Where("payment_provider = ? AND status = ? AND create_time < ?", PaymentProviderBinancePay, common.TopUpStatusPending, cutoff).
		Updates(map[string]any{"status": common.TopUpStatusExpired, "complete_time": common.GetTimestamp()})
	return res.RowsAffected, res.Error
}

// ReserveBinancePayMoney turns a price into a pay amount no other pending
// Binance Pay order is using: price rounded to cents plus a random 0.0001 to
// 0.9999. With pending orders in the dozens the collision odds per try are
// well under 1%, so a handful of retries is plenty; giving up is reported
// rather than silently reusing an amount, because a shared amount would let
// one transfer settle the wrong order.
func ReserveBinancePayMoney(price decimal.Decimal) (decimal.Decimal, error) {
	if price.Sign() <= 0 {
		return decimal.Zero, errors.New("price must be positive")
	}
	pending, err := GetPendingBinancePayTopUps()
	if err != nil {
		return decimal.Zero, err
	}
	inUse := make(map[string]struct{}, len(pending))
	for _, row := range pending {
		inUse[FormatBinancePayMoney(row.Money)] = struct{}{}
	}
	base := price.Round(2)
	for i := 0; i < binancePayReserveTries; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(binancePaySuffixRange))
		if err != nil {
			return decimal.Zero, err
		}
		suffix := decimal.New(n.Int64()+1, -BinancePayMoneyDecimals) // 0.0001..0.9999
		candidate := base.Add(suffix)
		if _, taken := inUse[candidate.StringFixed(BinancePayMoneyDecimals)]; taken {
			continue
		}
		return candidate, nil
	}
	return decimal.Zero, ErrBinancePayNoAmountAvailable
}

// binancePayQuota is the credit for a Binance Pay order: Amount is dollars,
// exactly like epay/waffo, see settlement_payments.go.
func binancePayQuota(topUp *TopUp) (int, error) {
	d := decimal.NewFromInt(topUp.Amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit))
	quota, clamp := common.QuotaFromDecimalChecked(d)
	if clamp != nil {
		return 0, fmt.Errorf("binance pay quota clamped for trade_no=%s: %w", topUp.TradeNo, clamp)
	}
	if quota <= 0 {
		return 0, errors.New("无效的充值额度")
	}
	return quota, nil
}

// GetBinancePayTransactionsByTradeNos returns the ledger row (the Binance
// transaction that paid the order) for each of the given orders that has one.
func GetBinancePayTransactionsByTradeNos(tradeNos []string) (map[string]BinancePayTransaction, error) {
	out := map[string]BinancePayTransaction{}
	if len(tradeNos) == 0 {
		return out, nil
	}
	var rows []BinancePayTransaction
	if err := DB.Where("trade_no IN ?", tradeNos).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.TradeNo] = row
	}
	return out, nil
}

// GetUsedBinancePayTransactionIds reports which of the given Binance
// transaction ids have already paid an order, and which one.
func GetUsedBinancePayTransactionIds(ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []BinancePayTransaction
	if err := DB.Select("transaction_id", "trade_no").Where("transaction_id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.TransactionId] = row.TradeNo
	}
	return out, nil
}

// RechargeBinancePay credits the order paid by txn. It is idempotent on both
// sides: a transaction id already in the ledger returns ErrBinancePayTxnUsed
// before touching the order, and an order that is no longer pending is left
// alone. Both checks happen under the row lock, in one transaction with the
// quota increase.
func RechargeBinancePay(tradeNo string, txn *BinancePayTransaction, callerIp string) error {
	return rechargeBinancePay(tradeNo, txn, callerIp, 0)
}

// RechargeBinancePayManual is the operator path: an administrator has looked
// at the account's Binance history and picked the transaction that paid this
// order (typically because the payer sent a slightly different amount). It
// differs from the automatic path in two ways only -- an expired order may be
// completed, since the customer's money arrived even if late, and the log
// names the administrator. The ledger still pins the transaction, so it can
// never be used for a second order.
func RechargeBinancePayManual(tradeNo string, txn *BinancePayTransaction, callerIp string, adminId int) error {
	if adminId <= 0 {
		return errors.New("missing administrator")
	}
	return rechargeBinancePay(tradeNo, txn, callerIp, adminId)
}

func rechargeBinancePay(tradeNo string, txn *BinancePayTransaction, callerIp string, adminId int) error {
	if strings.TrimSpace(tradeNo) == "" {
		return errors.New("未提供支付单号")
	}
	if txn == nil || strings.TrimSpace(txn.TransactionId) == "" {
		return errors.New("missing binance transaction id")
	}
	manual := adminId > 0

	refCol := "`trade_no`"
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		refCol = `"trade_no"`
	}

	var quotaToAdd int
	topUp := &TopUp{}
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where(refCol+" = ?", tradeNo).First(topUp).Error; err != nil {
			return errors.New("充值订单不存在")
		}
		if topUp.PaymentProvider != PaymentProviderBinancePay {
			return ErrPaymentMethodMismatch
		}
		if topUp.Status == common.TopUpStatusSuccess {
			return nil
		}
		if topUp.Status != common.TopUpStatusPending &&
			!(manual && topUp.Status == common.TopUpStatusExpired) {
			return ErrTopUpStatusInvalid
		}

		var used int64
		if err := tx.Model(&BinancePayTransaction{}).Where("transaction_id = ?", txn.TransactionId).Count(&used).Error; err != nil {
			return err
		}
		if used > 0 {
			return ErrBinancePayTxnUsed
		}

		q, err := binancePayQuota(topUp)
		if err != nil {
			return err
		}
		quotaToAdd = q

		ledger := *txn
		ledger.Id = 0
		ledger.TradeNo = tradeNo
		if err := tx.Create(&ledger).Error; err != nil {
			// The unique index is the real guard; the Count above only gives a
			// friendlier error in the common case.
			return fmt.Errorf("%w: %v", ErrBinancePayTxnUsed, err)
		}

		topUp.CompleteTime = common.GetTimestamp()
		topUp.Status = common.TopUpStatusSuccess
		if err := tx.Save(topUp).Error; err != nil {
			return err
		}
		if _, err := IncreaseUserQuotaWithTx(tx, topUp.UserId, quotaToAdd); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrBinancePayTxnUsed) || errors.Is(err, ErrTopUpStatusInvalid) || errors.Is(err, ErrPaymentMethodMismatch) {
			return err
		}
		common.SysError("binance pay topup failed: " + err.Error())
		return errors.New("充值失败，请稍后重试")
	}
	_ = InvalidateBillingQuotaCacheForUser(topUp.UserId)

	if quotaToAdd > 0 {
		who := "自动对账"
		via := PaymentMethodBinancePay
		if manual {
			who = fmt.Sprintf("管理员(%d)匹配", adminId)
			via = "admin"
		}
		RecordTopupLog(topUp.UserId,
			fmt.Sprintf("Binance Pay充值成功（%s），充值额度: %v，应付: %s %s，实收: %s %s，交易号: %s",
				who, logger.FormatQuota(quotaToAdd), FormatBinancePayMoney(topUp.Money), txn.Currency,
				txn.Amount, txn.Currency, txn.TransactionId),
			callerIp, topUp.PaymentMethod, via)
	}
	return nil
}

// IsPartnershipCustomerGroup reports whether group is an active Partnership
// Program customer group -- the reseller channel the Binance Pay gateway is
// recommended to. Any lookup problem reads as "no", so a database hiccup can
// only hide the recommendation, never misplace it.
func IsPartnershipCustomerGroup(group string) bool {
	group = strings.TrimSpace(group)
	if group == "" || DB == nil {
		return false
	}
	if !DB.Migrator().HasTable(&PartnershipCustomer{}) {
		return false
	}
	groupCol := commonGroupCol
	if groupCol == "" {
		// initCol has not run (unit tests on an ad-hoc SQLite DB).
		groupCol = "`group`"
	}
	var n int64
	err := DB.Model(&PartnershipCustomer{}).
		Where(groupCol+" = ? AND enabled = ? AND removed_at = 0", group, true).
		Count(&n).Error
	return err == nil && n > 0
}
