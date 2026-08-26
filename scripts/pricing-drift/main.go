// Command pricing-drift checks the pricing baseline against models.dev.
//
// UNIFYAPI-FORK. This is the tool that makes "the baseline is current" a
// checkable claim rather than a hope. It fetches the vendors' published prices
// from models.dev, diffs them against setting/ratio_setting/unifyapi_catalog.go,
// and exits non-zero when they disagree.
//
// It deliberately does NOT edit the catalog. A price change is a commercial
// event -- it changes what customers are billed the moment it deploys -- so it
// belongs in a reviewed commit, not in an automated push. The tool's job is to
// make sure nobody can *not notice*.
//
// Usage:
//
//	go run ./scripts/pricing-drift              # fetch live, report, exit 1 on drift
//	go run ./scripts/pricing-drift -json        # machine-readable, for CI annotations
//	go run ./scripts/pricing-drift -fixture F   # diff against a saved api.json
//	go run ./scripts/pricing-drift -save F      # write the fetched feed to F
//
// A vendor legitimately reprices, so drift is expected periodically and is not
// an error in the code -- it is a prompt to update the catalog and the
// PricingSnapshotDate, and to decide whether the customer-facing discount should
// absorb the change.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

const modelsDevURL = "https://models.dev/api.json"

// tolerance absorbs float representation noise only. It is deliberately tiny:
// a real price change of even a hundredth of a cent per 1M tokens is a change
// we want to see, because it usually means the vendor restructured a tier.
const tolerance = 1e-9

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

// Finding is one discrepancy between the catalog and the upstream feed.
type Finding struct {
	Model    string  `json:"model"`
	Field    string  `json:"field"`
	Catalog  float64 `json:"catalog"`
	Upstream float64 `json:"upstream"`
	Kind     string  `json:"kind"`
	Detail   string  `json:"detail"`
}

func main() {
	jsonOut := flag.Bool("json", false, "emit findings as JSON")
	fixture := flag.String("fixture", "", "read the models.dev feed from this file instead of the network")
	save := flag.String("save", "", "write the fetched feed to this file")
	flag.Parse()

	feed, err := loadFeed(*fixture, *save)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pricing-drift:", err)
		os.Exit(2)
	}

	findings := Check(feed)

	if *jsonOut {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(map[string]any{
			"snapshot_date": ratio_setting.PricingSnapshotDate,
			"checked":       len(ratio_setting.Catalog()),
			"findings":      findings,
		}); err != nil {
			fmt.Fprintln(os.Stderr, "pricing-drift:", err)
			os.Exit(2)
		}
	} else {
		report(findings)
	}

	if hasDrift(findings) {
		os.Exit(1)
	}
}

// hasDrift distinguishes findings that require action from informational ones.
// An unverifiable entry is reported every run by design -- it is a standing
// known gap, not a regression -- so it must not fail the check forever.
func hasDrift(findings []Finding) bool {
	for _, finding := range findings {
		if finding.Kind != "unverifiable" {
			return true
		}
	}
	return false
}

func loadFeed(fixture, save string) (map[string]modelsDevProvider, error) {
	var raw []byte
	var err error

	if fixture != "" {
		raw, err = os.ReadFile(fixture)
		if err != nil {
			return nil, fmt.Errorf("reading fixture: %w", err)
		}
	} else {
		client := &http.Client{Timeout: 60 * time.Second}
		response, err := client.Get(modelsDevURL)
		if err != nil {
			return nil, fmt.Errorf("fetching %s: %w", modelsDevURL, err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("fetching %s: HTTP %d", modelsDevURL, response.StatusCode)
		}
		raw, err = io.ReadAll(response.Body)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", modelsDevURL, err)
		}
	}

	if save != "" {
		if err := os.WriteFile(save, raw, 0o644); err != nil {
			return nil, fmt.Errorf("writing %s: %w", save, err)
		}
	}

	var feed map[string]modelsDevProvider
	if err := json.Unmarshal(raw, &feed); err != nil {
		return nil, fmt.Errorf("parsing feed: %w", err)
	}
	return feed, nil
}

// Check diffs the catalog against a models.dev feed. Exported so the offline
// test can drive it with a committed fixture.
func Check(feed map[string]modelsDevProvider) []Finding {
	var findings []Finding

	for _, problem := range ratio_setting.ValidateCatalog() {
		findings = append(findings, Finding{
			Kind:   "invalid",
			Detail: problem.Error(),
		})
	}

	for _, entry := range ratio_setting.Catalog() {
		if entry.Unverified {
			findings = append(findings, Finding{
				Model: entry.Model,
				Kind:  "unverifiable",
				Detail: "no models.dev listing; price cannot be checked automatically and " +
					"needs a manual quote from the vendor",
			})
			continue
		}

		provider, ok := feed[entry.Vendor]
		if !ok {
			findings = append(findings, Finding{
				Model: entry.Model, Kind: "vendor-missing",
				Detail: fmt.Sprintf("models.dev no longer lists provider %q", entry.Vendor),
			})
			continue
		}

		upstream, ok := provider.Models[entry.UpstreamID()]
		if !ok {
			findings = append(findings, Finding{
				Model: entry.Model, Kind: "model-retired",
				Detail: fmt.Sprintf("%s/%s is gone from models.dev -- the vendor may have retired it; "+
					"either drop it from the catalog or mark it Unverified with a reason",
					entry.Vendor, entry.UpstreamID()),
			})
			continue
		}
		if upstream.Cost == nil {
			findings = append(findings, Finding{
				Model: entry.Model, Kind: "price-withdrawn",
				Detail: fmt.Sprintf("%s/%s no longer publishes a price", entry.Vendor, entry.UpstreamID()),
			})
			continue
		}

		findings = append(findings, comparePrices(entry, *upstream.Cost)...)
	}

	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Kind != findings[j].Kind {
			return kindRank(findings[i].Kind) < kindRank(findings[j].Kind)
		}
		return findings[i].Model < findings[j].Model
	})
	return findings
}

// kindRank orders findings by how much they should worry the reader.
func kindRank(kind string) int {
	switch kind {
	case "invalid":
		return 0
	case "price-changed":
		return 1
	case "model-retired", "vendor-missing", "price-withdrawn":
		return 2
	default: // unverifiable
		return 3
	}
}

func comparePrices(entry ratio_setting.CatalogEntry, upstream modelsDevCost) []Finding {
	var findings []Finding

	compare := func(field string, catalogValue, upstreamValue float64) {
		if differs(catalogValue, upstreamValue) {
			findings = append(findings, Finding{
				Model: entry.Model, Field: field, Kind: "price-changed",
				Catalog: catalogValue, Upstream: upstreamValue,
				Detail: fmt.Sprintf("%s/%s %s is now $%g per 1M, catalog says $%g",
					entry.Vendor, entry.UpstreamID(), field, upstreamValue, catalogValue),
			})
		}
	}

	compare("input", entry.InputUSD, upstream.Input)
	compare("output", entry.OutputUSD, upstream.Output)
	compare("cache_read", entry.CacheReadUSD, deref(upstream.CacheRead))
	compare("cache_write", entry.CacheWriteUSD, deref(upstream.CacheWrite))

	return findings
}

func deref(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func differs(a, b float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff > tolerance
}

func report(findings []Finding) {
	catalog := ratio_setting.Catalog()
	fmt.Printf("pricing-drift: %d models, snapshot %s\n\n", len(catalog), ratio_setting.PricingSnapshotDate)

	if len(findings) == 0 {
		fmt.Println("  every catalogued price matches models.dev.")
		return
	}

	counts := map[string]int{}
	for _, finding := range findings {
		counts[finding.Kind]++
	}

	lastKind := ""
	for _, finding := range findings {
		if finding.Kind != lastKind {
			fmt.Printf("== %s (%d) ==\n", finding.Kind, counts[finding.Kind])
			lastKind = finding.Kind
		}
		if finding.Model != "" {
			fmt.Printf("  %-40s %s\n", finding.Model, finding.Detail)
		} else {
			fmt.Printf("  %s\n", finding.Detail)
		}
	}

	fmt.Println()
	if hasDrift(findings) {
		fmt.Println("ACTION REQUIRED: update setting/ratio_setting/unifyapi_catalog.go and bump")
		fmt.Println("PricingSnapshotDate, then decide whether the per-model customer discount")
		fmt.Println("should absorb the change or be passed through. Prices are NOT updated")
		fmt.Println("automatically: a repricing changes customer invoices and needs review.")
	} else {
		fmt.Printf("No drift. %d entries remain unverifiable and need manual quotes.\n", counts["unverifiable"])
	}
}
