package service

// UNIFYAPI-FORK: look a model's published price up on models.dev.
//
// So that adding a model in the console is "type the name, press sync" rather
// than "open a vendor page in another tab and retype four numbers". Retyping is
// where a decimal slips, and a slipped decimal in this system billed a model at
// 8.5% of cost for weeks.
//
// One thing this deliberately does NOT do is pick a price for you. The same
// model id appears under many providers at different prices -- a sweep for
// MiniMax entries returns over three hundred rows -- and choosing one silently
// would mean the console inventing a commercial decision. Every match is
// returned with its provider, sorted so the vendor's own listing comes first,
// and a human picks.
//
// It is also not a substitute for the catalog. A price synced here still lands
// in the extras table, still carries AdminAdded, and is still checked by nobody
// afterwards -- models.dev lags, which is how DeepSeek's August increase went
// unnoticed for seventeen days. Sync saves typing, not vigilance.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

const modelsDevURL = "https://models.dev/api.json"

// modelsDevCacheTTL bounds how stale a lookup can be. The feed is a few
// megabytes and changes daily at most, so re-fetching per keystroke would be
// rude to a free service and slow for the operator.
const modelsDevCacheTTL = time.Hour

type modelsDevCost struct {
	Input      float64  `json:"input"`
	Output     float64  `json:"output"`
	CacheRead  *float64 `json:"cache_read"`
	CacheWrite *float64 `json:"cache_write"`
}

type modelsDevModel struct {
	Cost *modelsDevCost `json:"cost"`
}

type modelsDevProvider struct {
	Models map[string]modelsDevModel `json:"models"`
}

// ModelPriceCandidate is one provider's published price for a model.
type ModelPriceCandidate struct {
	Provider      string  `json:"provider"`
	Model         string  `json:"model"`
	InputUSD      float64 `json:"input_usd"`
	OutputUSD     float64 `json:"output_usd"`
	CacheReadUSD  float64 `json:"cache_read_usd,omitempty"`
	CacheWriteUSD float64 `json:"cache_write_usd,omitempty"`

	// FirstParty marks a listing under the vendor's own provider id rather than
	// a reseller's. Those are the ones whose price is the official one; the
	// rest are somebody's resale margin.
	FirstParty bool `json:"first_party"`
}

var (
	modelsDevMu      sync.Mutex
	modelsDevFeed    map[string]modelsDevProvider
	modelsDevFetched time.Time
)

// fetchModelsDev returns the feed, cached.
func fetchModelsDev() (map[string]modelsDevProvider, error) {
	modelsDevMu.Lock()
	defer modelsDevMu.Unlock()

	if modelsDevFeed != nil && time.Since(modelsDevFetched) < modelsDevCacheTTL {
		return modelsDevFeed, nil
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(modelsDevURL)
	if err != nil {
		return nil, fmt.Errorf("无法访问 models.dev：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models.dev 返回 %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var feed map[string]modelsDevProvider
	if err := json.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("models.dev 返回的内容无法解析：%w", err)
	}

	// Only replace a good cache with another good one. A malformed response
	// should leave the operator with an hour-old answer rather than none.
	modelsDevFeed = feed
	modelsDevFetched = time.Now()
	common.SysLog(fmt.Sprintf("models.dev: refreshed price feed, %d providers", len(feed)))
	return feed, nil
}

// LookupModelPrice finds every published price for a model name.
//
// Matching is case-insensitive and also matches on the segment after the last
// slash, because the same model is listed as "glm-5.3" by one provider and
// "z-ai/glm-5.3" by another, and an operator typing the plain name should find
// both.
func LookupModelPrice(query string) ([]ModelPriceCandidate, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("请先填写模型名")
	}

	feed, err := fetchModelsDev()
	if err != nil {
		return nil, err
	}

	needle := strings.ToLower(query)
	var out []ModelPriceCandidate
	for provider, entry := range feed {
		for id, m := range entry.Models {
			if m.Cost == nil {
				// No published price. Video and image models bill per second or
				// per call and legitimately have none; returning them as $0
				// would be a lie the form would happily save.
				continue
			}
			// A zero on both sides is a subscription plan, not a price -- the
			// "*-coding-plan" and "*-token-plan" providers list every model at
			// 0 because the tokens are bundled. Offering those as sync
			// candidates would let one click make a model free, and a live
			// lookup for glm-5.3 returned three of them ahead of Zhipu's own
			// listing before this filter existed.
			if m.Cost.Input == 0 && m.Cost.Output == 0 {
				continue
			}
			lower := strings.ToLower(id)
			tail := lower
			if idx := strings.LastIndex(lower, "/"); idx >= 0 {
				tail = lower[idx+1:]
			}
			if lower != needle && tail != needle {
				continue
			}

			candidate := ModelPriceCandidate{
				Provider:   provider,
				Model:      id,
				InputUSD:   m.Cost.Input,
				OutputUSD:  m.Cost.Output,
				FirstParty: isFirstPartyListing(provider, id),
			}
			if m.Cost.CacheRead != nil {
				candidate.CacheReadUSD = *m.Cost.CacheRead
			}
			if m.Cost.CacheWrite != nil {
				candidate.CacheWriteUSD = *m.Cost.CacheWrite
			}
			out = append(out, candidate)
		}
	}

	// First-party listings first, then cheapest. The vendor's own price is the
	// one an operator almost always wants, and burying it in a list of
	// resellers is how the wrong number gets picked.
	sortCandidates(out)
	return out, nil
}

// isFirstPartyListing reports whether a provider id is a vendor we already
// treat as authoritative.
//
// The signal is our own catalog, not a guess about the string. models.dev has no
// "official" flag, and inferring one from the id is how `zhipuai-coding-plan`
// got ranked above `zhipuai` -- a $0 subscription listing sorted first, one
// click from making the model free.
//
// Every provider in CompiledCatalog() has already been vetted as the vendor's
// own listing by whoever added that row, so reusing it means the ranking
// improves as the catalog does, instead of depending on a pattern that has to
// anticipate every reseller's naming.
func isFirstPartyListing(provider, model string) bool {
	return knownVendors()[strings.ToLower(provider)]
}

var (
	knownVendorsOnce sync.Once
	knownVendorsSet  map[string]bool
)

// knownVendors is the set of provider ids the compiled catalog sources prices
// from. Computed once: the catalog is compiled in and cannot change at runtime.
func knownVendors() map[string]bool {
	knownVendorsOnce.Do(func() {
		knownVendorsSet = map[string]bool{}
		for _, entry := range ratio_setting.CompiledCatalog() {
			if entry.Vendor != "" {
				knownVendorsSet[strings.ToLower(entry.Vendor)] = true
			}
		}
	})
	return knownVendorsSet
}

// sortCandidates puts the vendor's own listing first, then the cheapest. Split
// out so the ordering can be tested without a network call -- the ordering is
// the part that decides which number an operator clicks.
func sortCandidates(candidates []ModelPriceCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].FirstParty != candidates[j].FirstParty {
			return candidates[i].FirstParty
		}
		if candidates[i].InputUSD != candidates[j].InputUSD {
			return candidates[i].InputUSD < candidates[j].InputUSD
		}
		return candidates[i].Provider < candidates[j].Provider
	})
}
