package service

// UNIFYAPI-FORK: minimal signed client for the two Binance *personal account*
// endpoints the Binance Pay gateway reconciles against. Deliberately stdlib
// only -- go.mod is an upstream file and a full exchange SDK would drag in far
// more surface than two read-only GETs justify.
//
//	GET /sapi/v1/pay/transactions        Binance Pay history (weight 3000/UID)
//	GET /sapi/v1/capital/deposit/hisrec  on-chain deposit history (weight 1/IP)
//
// Both are SIGNED endpoints: the query string (including a millisecond
// timestamp) is HMAC-SHA256'd with the secret and appended as `signature`,
// with the API key in the X-MBX-APIKEY header.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const (
	BinanceApiBaseURL = "https://api.binance.com"

	binancePayTransactionsPath = "/sapi/v1/pay/transactions"
	binanceDepositHistoryPath  = "/sapi/v1/capital/deposit/hisrec"

	// BinancePayHistoryPageSize is the hard maximum of /sapi/v1/pay/transactions.
	BinancePayHistoryPageSize = 100
	// BinanceHistoryMaxWindow is the widest startTime..endTime either endpoint
	// accepts.
	BinanceHistoryMaxWindow = 89 * 24 * time.Hour

	binanceRecvWindowMs = 10_000
	binanceMaxErrorBody = 512
)

// BinanceFlexString accepts a JSON string or number. Binance documents ids and
// amounts as strings but has shipped numbers for some of them; a payment must
// not fail to reconcile over that.
type BinanceFlexString string

func (s *BinanceFlexString) UnmarshalJSON(b []byte) error {
	raw := strings.TrimSpace(string(b))
	if raw == "null" || raw == "" {
		*s = ""
		return nil
	}
	if strings.HasPrefix(raw, `"`) {
		var str string
		if err := common.Unmarshal(b, &str); err != nil {
			return err
		}
		*s = BinanceFlexString(str)
		return nil
	}
	*s = BinanceFlexString(raw)
	return nil
}

func (s BinanceFlexString) String() string { return string(s) }

// BinancePayParty is the payer or receiver of a Pay transaction. Which fields
// are populated depends on orderType; binanceId is the one that is stable.
type BinancePayParty struct {
	Name      string            `json:"name"`
	Type      string            `json:"type"`
	BinanceId BinanceFlexString `json:"binanceId"`
	AccountId BinanceFlexString `json:"accountId"`
	Email     string            `json:"email"`
}

// BinancePayTransaction is one row of the account's Binance Pay history.
type BinancePayTransaction struct {
	OrderType       string            `json:"orderType"`
	TransactionId   BinanceFlexString `json:"transactionId"`
	TransactionTime int64             `json:"transactionTime"`
	Amount          BinanceFlexString `json:"amount"`
	Currency        string            `json:"currency"`
	PayerInfo       BinancePayParty   `json:"payerInfo"`
	ReceiverInfo    BinancePayParty   `json:"receiverInfo"`
}

// BinanceDeposit is one row of the account's on-chain deposit history.
type BinanceDeposit struct {
	Id           BinanceFlexString `json:"id"`
	Amount       BinanceFlexString `json:"amount"`
	Coin         string            `json:"coin"`
	Network      string            `json:"network"`
	Status       int               `json:"status"`
	Address      string            `json:"address"`
	TxId         string            `json:"txId"`
	InsertTime   int64             `json:"insertTime"`
	CompleteTime int64             `json:"completeTime"`
}

// Deposit status codes, per Binance's wallet API reference.
const (
	BinanceDepositStatusPending         = 0
	BinanceDepositStatusSuccess         = 1
	BinanceDepositStatusCreditedNoWithd = 6
)

// IsCredited reports whether the deposit has been credited to the account's
// balance. Status 6 is credited but temporarily not withdrawable; the money
// has arrived either way.
func (d BinanceDeposit) IsCredited() bool {
	return d.Status == BinanceDepositStatusSuccess || d.Status == BinanceDepositStatusCreditedNoWithd
}

// BinanceHistoryReader is what the reconciler depends on, so tests can feed it
// canned history without a network.
type BinanceHistoryReader interface {
	PayTransactions(ctx context.Context, startTimeMs, endTimeMs int64) ([]BinancePayTransaction, error)
	DepositHistory(ctx context.Context, coin string, startTimeMs, endTimeMs int64) ([]BinanceDeposit, error)
}

type BinanceClient struct {
	apiKey  string
	secret  string
	baseURL string
	http    *http.Client
	now     func() time.Time
}

// NewBinanceClient builds a client for the given read-only key pair. baseURL
// may be empty for production.
func NewBinanceClient(apiKey, secret, baseURL string) *BinanceClient {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = BinanceApiBaseURL
	}
	return &BinanceClient{
		apiKey:  strings.TrimSpace(apiKey),
		secret:  strings.TrimSpace(secret),
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    GetHttpClient(),
		now:     time.Now,
	}
}

var _ BinanceHistoryReader = (*BinanceClient)(nil)

// BinanceSign returns the lower-case hex HMAC-SHA256 of query under secret,
// exactly as Binance expects in the `signature` parameter.
func BinanceSign(secret, query string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(query))
	return hex.EncodeToString(mac.Sum(nil))
}

type binanceEnvelope struct {
	Code    any    `json:"code"`
	Message string `json:"message"`
	Msg     string `json:"msg"`
	Success *bool  `json:"success"`
}

// PayTransactions returns the account's Binance Pay history in
// [startTimeMs, endTimeMs], newest first, paging backwards until the window
// is exhausted. Pages are capped so a runaway account cannot pin the
// reconciler.
func (c *BinanceClient) PayTransactions(ctx context.Context, startTimeMs, endTimeMs int64) ([]BinancePayTransaction, error) {
	const maxPages = 5
	var out []BinancePayTransaction
	cursorEnd := endTimeMs
	for page := 0; page < maxPages; page++ {
		params := url.Values{}
		params.Set("startTime", strconv.FormatInt(startTimeMs, 10))
		params.Set("endTime", strconv.FormatInt(cursorEnd, 10))
		params.Set("limit", strconv.Itoa(BinancePayHistoryPageSize))

		var resp struct {
			binanceEnvelope
			Data []BinancePayTransaction `json:"data"`
		}
		if err := c.signedGet(ctx, binancePayTransactionsPath, params, &resp); err != nil {
			return nil, err
		}
		if resp.Success != nil && !*resp.Success {
			return nil, fmt.Errorf("binance pay/transactions: code=%v message=%q", resp.Code, firstNonEmpty(resp.Message, resp.Msg))
		}
		out = append(out, resp.Data...)
		if len(resp.Data) < BinancePayHistoryPageSize {
			break
		}
		oldest := resp.Data[len(resp.Data)-1].TransactionTime
		for _, t := range resp.Data {
			if t.TransactionTime > 0 && t.TransactionTime < oldest {
				oldest = t.TransactionTime
			}
		}
		if oldest <= startTimeMs || oldest <= 0 {
			break
		}
		cursorEnd = oldest - 1
	}
	return out, nil
}

// DepositHistory returns credited-or-pending deposits of coin in
// [startTimeMs, endTimeMs]. Binance returns a bare array here, and up to 1000
// rows per call, which is far more than a top-up window ever holds.
func (c *BinanceClient) DepositHistory(ctx context.Context, coin string, startTimeMs, endTimeMs int64) ([]BinanceDeposit, error) {
	params := url.Values{}
	if coin = strings.TrimSpace(coin); coin != "" {
		params.Set("coin", strings.ToUpper(coin))
	}
	params.Set("startTime", strconv.FormatInt(startTimeMs, 10))
	params.Set("endTime", strconv.FormatInt(endTimeMs, 10))
	params.Set("limit", "1000")

	var out []BinanceDeposit
	if err := c.signedGet(ctx, binanceDepositHistoryPath, params, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *BinanceClient) signedGet(ctx context.Context, path string, params url.Values, into any) error {
	if c.apiKey == "" || c.secret == "" {
		return errors.New("binance api key or secret not configured")
	}
	params.Set("timestamp", strconv.FormatInt(c.now().UnixMilli(), 10))
	params.Set("recvWindow", strconv.Itoa(binanceRecvWindowMs))
	query := params.Encode()
	query += "&signature=" + BinanceSign(c.secret, query)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path+"?"+query, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-MBX-APIKEY", c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("binance %s: %w", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("binance %s: read body: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("binance %s: http %d: %s", path, resp.StatusCode, truncateForLog(body))
	}
	// A 200 with an error envelope happens too ({"code":-1022,"msg":...}).
	var env binanceEnvelope
	if err := common.Unmarshal(body, &env); err == nil {
		if codeStr := fmt.Sprint(env.Code); env.Code != nil && codeStr != "000000" && codeStr != "0" && codeStr != "<nil>" {
			if msg := firstNonEmpty(env.Message, env.Msg); msg != "" {
				return fmt.Errorf("binance %s: code=%s message=%q", path, codeStr, msg)
			}
		}
	}
	if err := common.Unmarshal(body, into); err != nil {
		return fmt.Errorf("binance %s: decode: %w: %s", path, err, truncateForLog(body))
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func truncateForLog(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > binanceMaxErrorBody {
		return s[:binanceMaxErrorBody] + "…"
	}
	return s
}
