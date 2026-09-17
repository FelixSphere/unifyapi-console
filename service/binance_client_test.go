package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The documented example from Binance's "SIGNED endpoint" reference.
func TestBinanceSignMatchesDocumentedVector(t *testing.T) {
	secret := "NhqPtmdSJYdKjVHjA7PZj4Mge3R5YNiP1e3UZjInClVN65XAbvqqM6A7H5fATj0j"
	query := "symbol=LTCBTC&side=BUY&type=LIMIT&timeInForce=GTC&quantity=1&price=0.1&recvWindow=5000&timestamp=1499827319559"
	assert.Equal(t, "c8db56825ae71d6d79447849e617115f4a920fa2acdcab2b053c4b2838bd6b71", BinanceSign(secret, query))
}

func TestBinanceFlexStringAcceptsStringsAndNumbers(t *testing.T) {
	var got struct {
		A BinanceFlexString `json:"a"`
		B BinanceFlexString `json:"b"`
		C BinanceFlexString `json:"c"`
	}
	require.NoError(t, common.Unmarshal([]byte(`{"a":"12345678","b":12345678,"c":null}`), &got))
	assert.Equal(t, "12345678", got.A.String())
	assert.Equal(t, "12345678", got.B.String())
	assert.Equal(t, "", got.C.String())
}

func newTestBinanceClient(t *testing.T, handler http.HandlerFunc) *BinanceClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewBinanceClient("key-1", "secret-1", srv.URL)
	c.http = srv.Client()
	c.now = func() time.Time { return time.UnixMilli(1_700_000_000_000) }
	return c
}

func TestPayTransactionsSignsAndDecodes(t *testing.T) {
	c := newTestBinanceClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/sapi/v1/pay/transactions", r.URL.Path)
		assert.Equal(t, "key-1", r.Header.Get("X-MBX-APIKEY"))

		q := r.URL.RawQuery
		idx := strings.LastIndex(q, "&signature=")
		require.Greater(t, idx, 0, "signature must be the last parameter")
		assert.Equal(t, BinanceSign("secret-1", q[:idx]), q[idx+len("&signature="):])

		values, err := url.ParseQuery(q[:idx])
		require.NoError(t, err)
		assert.Equal(t, "1700000000000", values.Get("timestamp"))
		assert.Equal(t, "100", values.Get("limit"))
		assert.Equal(t, "1000", values.Get("startTime"))
		assert.Equal(t, "2000", values.Get("endTime"))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"000000","message":"success","success":true,"data":[
		  {"orderType":"C2C","transactionId":"M_P_1","transactionTime":1500,"amount":"20.0137","currency":"USDT",
		   "payerInfo":{"name":"Jack","type":"USER","binanceId":12345678},
		   "receiverInfo":{"name":"Op","type":"USER","binanceId":"34355667"}}
		]}`))
	})

	rows, err := c.PayTransactions(context.Background(), 1000, 2000)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "M_P_1", rows[0].TransactionId.String())
	assert.Equal(t, "20.0137", rows[0].Amount.String())
	assert.Equal(t, "12345678", rows[0].PayerInfo.BinanceId.String())
	assert.Equal(t, "34355667", rows[0].ReceiverInfo.BinanceId.String())
}

func TestPayTransactionsPagesBackwardsUntilWindowExhausted(t *testing.T) {
	calls := 0
	c := newTestBinanceClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		values, _ := url.ParseQuery(r.URL.RawQuery)
		var sb strings.Builder
		sb.WriteString(`{"code":"000000","success":true,"data":[`)
		if calls == 1 {
			assert.Equal(t, "9000", values.Get("endTime"))
			for i := 0; i < BinancePayHistoryPageSize; i++ {
				if i > 0 {
					sb.WriteString(",")
				}
				// Oldest on this page is 5000.
				sb.WriteString(`{"transactionId":"p1-` + strings.Repeat("x", i%3) + `","transactionTime":` + strconv.Itoa(9000-i*40) + `,"amount":"1","currency":"USDT"}`)
			}
		} else {
			assert.Equal(t, "5039", values.Get("endTime"))
			sb.WriteString(`{"transactionId":"p2","transactionTime":4000,"amount":"1","currency":"USDT"}`)
		}
		sb.WriteString(`]}`)
		_, _ = w.Write([]byte(sb.String()))
	})

	rows, err := c.PayTransactions(context.Background(), 1000, 9000)
	require.NoError(t, err)
	assert.Equal(t, 2, calls)
	assert.Len(t, rows, BinancePayHistoryPageSize+1)
}

func TestDepositHistoryDecodesBareArray(t *testing.T) {
	c := newTestBinanceClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/sapi/v1/capital/deposit/hisrec", r.URL.Path)
		values, _ := url.ParseQuery(r.URL.RawQuery)
		assert.Equal(t, "USDT", values.Get("coin"))
		_, _ = w.Write([]byte(`[{"id":"769800519366885376","amount":"20.0137","coin":"USDT","network":"TRX","status":1,
		  "address":"TXYZ","txId":"98A3","insertTime":1661493146000}]`))
	})
	rows, err := c.DepositHistory(context.Background(), "usdt", 1, 2)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.True(t, rows[0].IsCredited())
	assert.Equal(t, "98A3", rows[0].TxId)
}

func TestSignedGetSurfacesBinanceErrors(t *testing.T) {
	c := newTestBinanceClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":-2015,"msg":"Invalid API-key, IP, or permissions for action."}`))
	})
	_, err := c.PayTransactions(context.Background(), 1, 2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "-2015")

	c2 := newTestBinanceClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code":-1003,"msg":"Too many requests."}`))
	})
	_, err = c2.DepositHistory(context.Background(), "USDT", 1, 2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "429")

	c3 := NewBinanceClient("", "", "")
	_, err = c3.DepositHistory(context.Background(), "USDT", 1, 2)
	require.Error(t, err)
}
