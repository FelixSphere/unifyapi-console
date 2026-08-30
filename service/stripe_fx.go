package service

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/setting"
)

// The wallet page quotes foreign currencies from a live reference rate so the
// figure tracks the market instead of drifting away from a constant someone set
// months ago.
//
// This never prices a charge. Checkout is always created in the account's
// settlement currency and Stripe Adaptive Pricing re-quotes it at Stripe's own
// guaranteed rate, so a stale or missing rate here can only make an estimate
// slightly wrong — it can never underprice a sale.
//
// Deliberately not per-request: Stripe guarantees its own rate for 24 hours, so
// fetching more often than the cache TTL buys no accuracy, and putting a
// third-party HTTP call in front of the topup page would trade a small display
// improvement for an outage risk. Reads never block on the network — a stale
// value is served while a refresh runs behind it, and if the feed is down the
// admin-configured rate stands in.
const (
	fxRefreshInterval = 6 * time.Hour
	// Past this, a cached rate is treated as untrustworthy and the configured
	// fallback takes over, so a feed that fails quietly for days cannot keep
	// quoting last week's market.
	fxMaxStaleness = 48 * time.Hour
	fxFetchTimeout = 5 * time.Second
	// European Central Bank reference rates, published on business days.
	fxSource = "https://api.frankfurter.dev/v1/latest?base=USD"
)

type fxRates struct {
	mu         sync.RWMutex
	rates      map[string]float64
	fetchedAt  time.Time
	refreshing bool
}

var midMarketRates = &fxRates{rates: map[string]float64{}}

// EffectiveStripeCurrencies returns the currencies the wallet page may quote,
// with live rates applied where they are available.
//
// One source of truth on purpose: the preset buttons price themselves from the
// rate in this list while the amount box asks the server, so both must come
// from here or the same page contradicts itself.
func EffectiveStripeCurrencies() []setting.StripeCurrency {
	configured := setting.GetStripeCurrencies()
	out := make([]setting.StripeCurrency, 0, len(configured))
	for _, currency := range configured {
		out = append(out, setting.StripeCurrency{
			Code: currency.Code,
			Rate: StripeEstimateRate(currency.Code, currency.Rate),
		})
	}
	return out
}

// StripeEstimateRate returns "1 USD = X <code>" for display, falling back to the
// admin-configured rate when no trustworthy live rate is available.
func StripeEstimateRate(code string, fallback float64) float64 {
	if code == setting.StripeBaseCurrency {
		return 1
	}

	mid, ok := midMarketRates.lookup(code)
	if !ok {
		return fallback
	}

	// Stripe's conversion markup is paid by the buyer, so a bare mid-market
	// quote reads cheaper than the checkout page will. Marking the estimate up
	// keeps the amount at Stripe equal to or below what we promised.
	return mid * (1 + setting.GetStripeEstimateMargin())
}

func (f *fxRates) lookup(code string) (float64, bool) {
	f.mu.RLock()
	rate, ok := f.rates[code]
	age := time.Since(f.fetchedAt)
	empty := f.fetchedAt.IsZero()
	f.mu.RUnlock()

	if empty || age > fxRefreshInterval {
		f.refreshAsync()
	}
	if !ok || empty || age > fxMaxStaleness {
		return 0, false
	}
	return rate, true
}

func (f *fxRates) refreshAsync() {
	f.mu.Lock()
	if f.refreshing {
		f.mu.Unlock()
		return
	}
	f.refreshing = true
	f.mu.Unlock()

	go func() {
		defer func() {
			f.mu.Lock()
			f.refreshing = false
			f.mu.Unlock()
		}()
		rates, err := fetchMidMarketRates()
		if err != nil {
			// Not an error for the caller: the previous value, or the
			// configured fallback, still answers.
			common.SysError("fx reference rates unavailable, keeping previous: " + err.Error())
			return
		}
		f.mu.Lock()
		f.rates = rates
		f.fetchedAt = time.Now()
		f.mu.Unlock()
		logger.LogInfo(nil, fmt.Sprintf("fx reference rates refreshed: %d currencies", len(rates)))
	}()
}

func fetchMidMarketRates() (map[string]float64, error) {
	client := &http.Client{Timeout: fxFetchTimeout}
	resp, err := client.Get(fxSource)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("reference feed returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, err
	}

	var payload struct {
		Rates map[string]float64 `json:"rates"`
	}
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if len(payload.Rates) == 0 {
		return nil, fmt.Errorf("reference feed returned no rates")
	}

	rates := make(map[string]float64, len(payload.Rates))
	for code, rate := range payload.Rates {
		if rate > 0 {
			rates[strings.ToUpper(code)] = rate
		}
	}
	return rates, nil
}
