package main

import (
	"encoding/json"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const syntheticCatalog = `package ratio_setting

// PricingSnapshotDate is the day the prices were verified.
const PricingSnapshotDate = "2026-01-01"

var unifyapiCatalog = []CatalogEntry{
	// Anthropic publishes $5 in / $25 out; the 2.5 here is $5/1M.
	{Model: "claude-opus-5", Vendor: "anthropic", InputUSD: 5, OutputUSD: 25, CacheReadUSD: 0.5, CacheWriteUSD: 6.25},
	{Model: "gpt-4o", Vendor: "openai", InputUSD: 2.5, OutputUSD: 10, CacheReadUSD: 1.25, CacheWriteUSD: 0},
	{Model: "gpt-5", Vendor: "openai", InputUSD: 1.25, OutputUSD: 10, CacheReadUSD: 0.125, CacheWriteUSD: 0,
		QuoteSource: "https://models.dev/api.json (openai/gpt-5)", QuoteDate: "2026-08-30"},
	{Model: "qwen3.7-plus", Vendor: "alibaba", InputUSD: 0.5, OutputUSD: 3,
		ContextTier: &ContextTier{ThresholdTokens: 256000, InputUSD: 2, OutputUSD: 6}},
}
`

// rowPrices parses a catalog source and returns model -> field -> value, so a
// test can assert on the meaning of the rewrite rather than on its bytes.
func rowPrices(t *testing.T, src []byte) (map[string]map[string]float64, string) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "catalog.go", src, parser.ParseComments)
	require.NoError(t, err, "rewritten source must still parse")

	prices := map[string]map[string]float64{}
	snapshot := ""
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Values) != 1 {
				continue
			}
			if value.Names[0].Name == "PricingSnapshotDate" {
				snapshot, _ = strconv.Unquote(value.Values[0].(*ast.BasicLit).Value)
			}
			table, ok := value.Values[0].(*ast.CompositeLit)
			if !ok || value.Names[0].Name != "unifyapiCatalog" {
				continue
			}
			for _, elt := range table.Elts {
				model, fields := describeRow(elt.(*ast.CompositeLit))
				prices[model] = map[string]float64{}
				for name, kv := range fields {
					if lit, ok := kv.Value.(*ast.BasicLit); ok && lit.Kind == token.FLOAT || ok && lit.Kind == token.INT {
						prices[model][name], _ = strconv.ParseFloat(lit.Value, 64)
					}
				}
			}
		}
	}
	return prices, snapshot
}

func TestRewriteCatalogMovesOnlyTheDriftedFields(t *testing.T) {
	out, fixed, err := RewriteCatalog([]byte(syntheticCatalog), []Finding{
		{Model: "claude-opus-5", Field: "input", Kind: "price-changed", Catalog: 5, Upstream: 6},
		{Model: "claude-opus-5", Field: "cache_read", Kind: "price-changed", Catalog: 0.5, Upstream: 0.6},
		{Model: "gpt-4o", Field: "output", Kind: "price-changed", Catalog: 10, Upstream: 8},
		// Not a price: must be left for a person.
		{Model: "gpt-5", Kind: "model-retired", Detail: "gone"},
	}, "2026-09-22")
	require.NoError(t, err)
	require.Equal(t, 2, fixed)

	prices, snapshot := rowPrices(t, out)
	assert.Equal(t, "2026-09-22", snapshot)
	assert.Equal(t, 6.0, prices["claude-opus-5"]["InputUSD"])
	assert.Equal(t, 0.6, prices["claude-opus-5"]["CacheReadUSD"])
	assert.Equal(t, 25.0, prices["claude-opus-5"]["OutputUSD"], "untouched field must keep its value")
	assert.Equal(t, 6.25, prices["claude-opus-5"]["CacheWriteUSD"])
	assert.Equal(t, 8.0, prices["gpt-4o"]["OutputUSD"])
	assert.Equal(t, 2.5, prices["gpt-4o"]["InputUSD"])
	assert.Equal(t, 1.25, prices["gpt-5"]["InputUSD"], "a retired model is a decision, not a rewrite")
	assert.Equal(t, 0.5, prices["qwen3.7-plus"]["InputUSD"])

	text := string(out)
	assert.Contains(t, text, "the 2.5 here is $5/1M", "comments are not prices and must survive")
	assert.Contains(t, text, `QuoteSource: "https://models.dev/api.json (openai/gpt-5)", QuoteDate: "2026-08-30"`,
		"provenance on other rows is untouched")
	assert.Contains(t, text, "ContextTier: &ContextTier{ThresholdTokens: 256000, InputUSD: 2, OutputUSD: 6}",
		"a nested tier literal is not the row's own InputUSD and must not move")

	formatted, err := format.Source(out)
	require.NoError(t, err)
	assert.Equal(t, string(formatted), text, "output must already be gofmt-clean so the PR has no formatting noise")
}

func TestRewriteCatalogAppendsAPriceTheRowNeverCarried(t *testing.T) {
	out, fixed, err := RewriteCatalog([]byte(syntheticCatalog), []Finding{
		// Alibaba starts publishing a cached-read rate for a row that had none.
		{Model: "qwen3.7-plus", Field: "cache_read", Kind: "price-changed", Catalog: 0, Upstream: 0.05},
	}, "2026-09-22")
	require.NoError(t, err)
	require.Equal(t, 1, fixed)

	prices, _ := rowPrices(t, out)
	assert.Equal(t, 0.05, prices["qwen3.7-plus"]["CacheReadUSD"])
	assert.Contains(t, string(out), "OutputUSD: 6}, CacheReadUSD: 0.05}",
		"appended after the last field, keeping the multi-line layout intact")
}

func TestRewriteCatalogRefusesADriftItCannotPlace(t *testing.T) {
	_, _, err := RewriteCatalog([]byte(syntheticCatalog), []Finding{
		{Model: "admin-added-model", Field: "input", Kind: "price-changed", Upstream: 1},
	}, "2026-09-22")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "admin-added-model")

	_, _, err = RewriteCatalog([]byte(syntheticCatalog), []Finding{
		{Model: "gpt-4o", Field: "per_call", Kind: "price-changed", Upstream: 1},
	}, "2026-09-22")
	require.Error(t, err, "an unknown field must fail loudly rather than be dropped")
}

func TestRewriteCatalogIsANoOpWithoutPriceChanges(t *testing.T) {
	out, fixed, err := RewriteCatalog([]byte(syntheticCatalog), []Finding{
		{Model: "gpt-4o", Kind: "unverifiable"},
	}, "2026-01-01")
	require.NoError(t, err)
	assert.Equal(t, 0, fixed)
	formatted, err := format.Source([]byte(syntheticCatalog))
	require.NoError(t, err)
	assert.Equal(t, string(formatted), string(out))
}

// TestRewriteCatalogAppliesToTheRealCatalog runs the rewriter over the file it
// will actually edit, so a layout the synthetic source does not exercise (a
// multi-line row with provenance, a per-second video row, a nested tier) is
// covered by the real thing.
func TestRewriteCatalogAppliesToTheRealCatalog(t *testing.T) {
	src, err := os.ReadFile("../../setting/ratio_setting/unifyapi_catalog.go")
	require.NoError(t, err)

	before, _ := rowPrices(t, src)
	require.Contains(t, before, "gpt-4o")
	require.Contains(t, before, "gemini-2.5-pro")

	out, fixed, err := RewriteCatalog(src, []Finding{
		{Model: "gpt-4o", Field: "input", Kind: "price-changed", Catalog: before["gpt-4o"]["InputUSD"], Upstream: 2.4},
		{Model: "gemini-2.5-pro", Field: "output", Kind: "price-changed", Catalog: before["gemini-2.5-pro"]["OutputUSD"], Upstream: 11},
	}, "2099-01-01")
	require.NoError(t, err)
	require.Equal(t, 2, fixed)

	after, snapshot := rowPrices(t, out)
	assert.Equal(t, "2099-01-01", snapshot)
	assert.Equal(t, 2.4, after["gpt-4o"]["InputUSD"])
	assert.Equal(t, 11.0, after["gemini-2.5-pro"]["OutputUSD"])

	for model, fields := range before {
		for name, value := range fields {
			if (model == "gpt-4o" && name == "InputUSD") || (model == "gemini-2.5-pro" && name == "OutputUSD") {
				continue
			}
			assert.Equal(t, value, after[model][name], "%s.%s must not move", model, name)
		}
	}
	assert.Equal(t, len(before), len(after), "no row may appear or vanish")

	formatted, err := format.Source(out)
	require.NoError(t, err)
	assert.Equal(t, string(formatted), string(out))
}

func TestTrimFeedKeepsExactlyWhatTheCheckerReads(t *testing.T) {
	feed := loadFixture(t)
	junk := 1.0
	feed["someone-else"] = modelsDevProvider{Models: map[string]modelsDevModel{"x": {Cost: &modelsDevCost{Input: junk}}}}
	openai := feed["openai"]
	openai.Models["gpt-3.5-turbo"] = modelsDevModel{Cost: &modelsDevCost{Input: 0.5, Output: 1.5}}
	feed["openai"] = openai

	trimmed := TrimFeed(feed)
	assert.NotContains(t, trimmed, "someone-else")
	assert.NotContains(t, trimmed["openai"].Models, "gpt-3.5-turbo")
	for _, entry := range ratio_setting.CompiledCatalog() {
		if entry.Vendor == "" {
			continue
		}
		assert.Contains(t, trimmed[entry.Vendor].Models, entry.UpstreamID(), "%s must survive the trim", entry.Model)
	}

	// The trimmed feed must be a valid fixture: same verdict as the full one.
	raw, err := json.Marshal(trimmed)
	require.NoError(t, err)
	var reloaded map[string]modelsDevProvider
	require.NoError(t, json.Unmarshal(raw, &reloaded))
	assert.Equal(t, hasDrift(Check(loadFixture(t))), hasDrift(Check(reloaded)))
}

func TestServedModelsComeFromThePublicPricingPage(t *testing.T) {
	served, err := parseServedModels([]byte(`{"success":true,"data":[{"model_name":"gpt-4o","quota_type":0},{"model_name":"claude-opus-5"}],"group_ratio":{"default":1}}`))
	require.NoError(t, err)
	assert.True(t, served["gpt-4o"])
	assert.True(t, served["claude-opus-5"])
	assert.False(t, served["gpt-5"])

	_, err = parseServedModels([]byte(`{"success":true,"data":[]}`))
	require.Error(t, err, "an empty page must not silently mark every model as off sale")
	_, err = parseServedModels([]byte(`<html>maintenance</html>`))
	require.Error(t, err)
}

func TestMarkServedFlagsOnlyModelsOnSale(t *testing.T) {
	findings := MarkServed([]Finding{
		{Model: "gpt-4o", Kind: "price-changed"},
		{Model: "gpt-5", Kind: "price-changed"},
	}, servedModels{"gpt-4o": true})
	assert.True(t, findings[0].Served)
	assert.False(t, findings[1].Served)
	assert.True(t, hasDrift(findings), "an unsold drift is still a catalog defect the fixture test will insist on")
}

func TestPullRequestBodySeparatesSoldFromUnsold(t *testing.T) {
	body := PullRequestBody([]Finding{
		{Model: "gpt-4o", Field: "input", Kind: "price-changed", Catalog: 2.5, Upstream: 3, Served: true},
		{Model: "gpt-5", Field: "output", Kind: "price-changed", Catalog: 10, Upstream: 9},
		{Model: "kimi-k2.6", Kind: "model-retired", Detail: "moonshotai/kimi-k2.6 is gone from models.dev"},
		{Model: "glm-5-turbo", Kind: "unverifiable", Detail: "no listing"},
	}, 2, "2026-09-22", true)

	sold := strings.Index(body, "currently on sale")
	unsold := strings.Index(body, "not on any channel")
	decision := strings.Index(body, "Needs a decision")
	require.True(t, sold > 0 && unsold > sold && decision > unsold, "sections must appear in order of urgency")
	assert.Contains(t, body[sold:unsold], "`gpt-4o` | input | $2.5 | $3 | +20.0%")
	assert.Contains(t, body[unsold:decision], "`gpt-5` | output | $10 | $9 | -10.0%")
	assert.Contains(t, body[decision:], "kimi-k2.6")
	assert.NotContains(t, body, "glm-5-turbo", "standing unverifiable entries are not a decision for this PR")
	assert.Contains(t, body, "TestPinnedDollarsForTheModelsThatCarryTheTraffic")
	assert.Contains(t, body, "2026-09-22")
}
