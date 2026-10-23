package main

// UNIFYAPI-FORK: the pieces that turn the drift report into a reviewable PR.
//
// The checker's contract has not changed: it never edits production state.
// What -fix edits is the SOURCE of the catalog, and the result goes through a
// pull request like any other price change. Automation writes the diff so no
// price is ever re-typed by hand; a human still decides whether the customer
// discount absorbs the change, and merging is what publishes it.
//
// The rewrite is done on the Go AST with byte offsets, not with text
// substitution: two rows can share a price, comments quote prices, and a
// regexp that touched the wrong "2.5" would be exactly the slipped decimal
// this whole catalog exists to prevent.

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// fieldNames maps a Finding.Field to the CatalogEntry field it lives in.
var fieldNames = map[string]string{
	"input":       "InputUSD",
	"output":      "OutputUSD",
	"cache_read":  "CacheReadUSD",
	"cache_write": "CacheWriteUSD",
}

type edit struct {
	start, end int
	text       string
}

// RewriteCatalog applies price-changed findings to the catalog source and
// moves PricingSnapshotDate to snapshotDate. Every other byte -- comments,
// ordering, provenance fields, rows that did not drift -- is left as it was.
//
// Only "price-changed" findings are applied. A retired model or a withdrawn
// price is a decision (drop the row, or mark it Unverified with a reason), not
// a number, and stays in the report for a person.
func RewriteCatalog(src []byte, findings []Finding, snapshotDate string) ([]byte, int, error) {
	changes := map[string]map[string]float64{}
	for _, finding := range findings {
		if finding.Kind != "price-changed" {
			continue
		}
		field, ok := fieldNames[finding.Field]
		if !ok {
			return nil, 0, fmt.Errorf("%s: unknown price field %q", finding.Model, finding.Field)
		}
		if changes[finding.Model] == nil {
			changes[finding.Model] = map[string]float64{}
		}
		changes[finding.Model][field] = finding.Upstream
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "unifyapi_catalog.go", src, parser.ParseComments)
	if err != nil {
		return nil, 0, fmt.Errorf("parsing catalog: %w", err)
	}
	offset := func(pos token.Pos) int { return fset.Position(pos).Offset }

	var edits []edit
	applied := map[string]bool{}
	sawSnapshotDate := false

	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || len(value.Values) != 1 {
				continue
			}
			switch value.Names[0].Name {
			case "PricingSnapshotDate":
				lit, ok := value.Values[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return nil, 0, fmt.Errorf("PricingSnapshotDate is not a string literal")
				}
				edits = append(edits, edit{offset(lit.Pos()), offset(lit.End()), strconv.Quote(snapshotDate)})
				sawSnapshotDate = true
			case "unifyapiCatalog":
				table, ok := value.Values[0].(*ast.CompositeLit)
				if !ok {
					return nil, 0, fmt.Errorf("unifyapiCatalog is not a composite literal")
				}
				for _, elt := range table.Elts {
					row, ok := elt.(*ast.CompositeLit)
					if !ok {
						continue
					}
					model, fields := describeRow(row)
					wanted, ok := changes[model]
					if !ok {
						continue
					}
					names := make([]string, 0, len(wanted))
					for name := range wanted {
						names = append(names, name)
					}
					sort.Strings(names)
					for _, name := range names {
						text := price(wanted[name])
						if kv, ok := fields[name]; ok {
							edits = append(edits, edit{offset(kv.Value.Pos()), offset(kv.Value.End()), text})
							continue
						}
						// The vendor now publishes a price the row never carried
						// (a cache rate, typically). Append it after the last
						// field so a trailing comma or newline stays where it is.
						last := row.Elts[len(row.Elts)-1]
						edits = append(edits, edit{offset(last.End()), offset(last.End()), ", " + name + ": " + text})
					}
					applied[model] = true
				}
			}
		}
	}

	if !sawSnapshotDate {
		return nil, 0, fmt.Errorf("PricingSnapshotDate not found in catalog source")
	}
	for model := range changes {
		if !applied[model] {
			return nil, 0, fmt.Errorf("%s: drifted but has no row in unifyapiCatalog (an admin-added extra cannot be fixed here)", model)
		}
	}

	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	out := append([]byte(nil), src...)
	for _, e := range edits {
		out = append(out[:e.start], append([]byte(e.text), out[e.end:]...)...)
	}
	formatted, err := format.Source(out)
	if err != nil {
		return nil, 0, fmt.Errorf("rewritten catalog does not format: %w", err)
	}
	return formatted, len(applied), nil
}

// describeRow reads one catalog row's Model and indexes its fields by name.
func describeRow(row *ast.CompositeLit) (string, map[string]*ast.KeyValueExpr) {
	fields := map[string]*ast.KeyValueExpr{}
	model := ""
	for _, elt := range row.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		fields[key.Name] = kv
		if key.Name == "Model" {
			if lit, ok := kv.Value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				model, _ = strconv.Unquote(lit.Value)
			}
		}
	}
	return model, fields
}

// price formats a coefficient the way the catalog writes them: shortest exact
// decimal, no exponent, so 0.075 stays 0.075 and 10 stays 10.
func price(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// TrimFeed keeps only the providers and models the compiled catalog is priced
// from, and only their cost block. The full models.dev dump is several
// megabytes of capability metadata; the committed fixture is the few kilobytes
// the checker actually reads, so a refresh is a reviewable diff.
func TrimFeed(feed map[string]modelsDevProvider) map[string]modelsDevProvider {
	wanted := map[string]map[string]bool{}
	for _, entry := range ratio_setting.CompiledCatalog() {
		if entry.Vendor == "" {
			continue
		}
		if wanted[entry.Vendor] == nil {
			wanted[entry.Vendor] = map[string]bool{}
		}
		wanted[entry.Vendor][entry.UpstreamID()] = true
	}

	trimmed := map[string]modelsDevProvider{}
	for vendor, models := range wanted {
		provider, ok := feed[vendor]
		if !ok {
			continue
		}
		kept := modelsDevProvider{Models: map[string]modelsDevModel{}}
		for id := range models {
			if upstream, ok := provider.Models[id]; ok {
				kept.Models[id] = modelsDevModel{Cost: upstream.Cost}
			}
		}
		trimmed[vendor] = kept
	}
	return trimmed
}

// servedModels is the set of models currently on sale: what GET /api/pricing
// lists, which is exactly the models enabled on at least one channel. It is
// the public endpoint the pricing page itself reads, so the checker needs no
// credentials and no database to know what is live.
type servedModels map[string]bool

func fetchServedModels(url string) (servedModels, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching %s: HTTP %d", url, response.StatusCode)
	}
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", url, err)
	}
	return parseServedModels(raw)
}

func parseServedModels(raw []byte) (servedModels, error) {
	var body struct {
		Data []struct {
			ModelName string `json:"model_name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("parsing pricing page: %w", err)
	}
	served := servedModels{}
	for _, row := range body.Data {
		if row.ModelName != "" {
			served[row.ModelName] = true
		}
	}
	if len(served) == 0 {
		return nil, fmt.Errorf("pricing page listed no models; refusing to treat everything as off sale")
	}
	return served, nil
}

// MarkServed stamps each finding with whether its model is on sale. A drift on
// a model nobody can call is still a catalog defect -- the fixture test will
// insist it is fixed -- but it is not a commercial event, and the report should
// not read as if it were.
func MarkServed(findings []Finding, served servedModels) []Finding {
	out := make([]Finding, len(findings))
	for i, finding := range findings {
		finding.Served = served[finding.Model]
		out[i] = finding
	}
	return out
}

// PullRequestBody renders the report a reviewer needs to decide on the price
// change, in the order they need it: what moved on sold models, what moved on
// unsold ones, what -fix could not decide, and the checks that still gate a
// merge.
func PullRequestBody(findings []Finding, fixed int, snapshotDate string, servedKnown bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Daily pricing-drift run: models.dev disagrees with `setting/ratio_setting/unifyapi_catalog.go`. ")
	fmt.Fprintf(&b, "This PR moves %d catalogue row(s) to the vendors' current list prices and sets `PricingSnapshotDate` to %s.\n\n", fixed, snapshotDate)

	writeTable := func(title string, rows []Finding) {
		if len(rows) == 0 {
			return
		}
		fmt.Fprintf(&b, "### %s\n\n| Model | Field | Catalogue | Vendor now | Change |\n|---|---|---|---|---|\n", title)
		for _, f := range rows {
			change := "new price"
			if f.Catalog != 0 {
				change = fmt.Sprintf("%+.1f%%", (f.Upstream-f.Catalog)/f.Catalog*100)
			}
			fmt.Fprintf(&b, "| `%s` | %s | $%s | $%s | %s |\n", f.Model, f.Field, price(f.Catalog), price(f.Upstream), change)
		}
		b.WriteString("\n")
	}

	var soldChanges, unsoldChanges, undecided []Finding
	for _, f := range findings {
		switch f.Kind {
		case "price-changed":
			if !servedKnown || f.Served {
				soldChanges = append(soldChanges, f)
			} else {
				unsoldChanges = append(unsoldChanges, f)
			}
		case "model-retired", "vendor-missing", "price-withdrawn", "invalid":
			undecided = append(undecided, f)
		}
	}
	if servedKnown {
		writeTable("Price changes on models currently on sale", soldChanges)
		writeTable("Price changes on catalogued models not on any channel", unsoldChanges)
	} else {
		writeTable("Price changes", soldChanges)
	}
	if len(undecided) > 0 {
		b.WriteString("### Needs a decision, not applied\n\n")
		for _, f := range undecided {
			fmt.Fprintf(&b, "- **%s** `%s`: %s\n", f.Kind, f.Model, f.Detail)
		}
		b.WriteString("\n")
	}

	b.WriteString("### Before merging\n\n")
	b.WriteString("1. **A rise changes what every customer pays the moment this deploys.** Decide per model whether `ModelDiscount` absorbs it or it passes through; check `GET /api/pricing/reconcile?group_by=model` for the affected rows -- a model already thin on margin cannot absorb a rise.\n")
	b.WriteString("2. **Red baseline tests are expected, not a bug in this PR.** `TestPinnedDollarsForTheModelsThatCarryTheTraffic` and several other tests hold hand-typed vendor dollar figures precisely so a table cannot vouch for itself. Re-read the new price off the vendor's own page and update those figures in the same PR; if the vendor's page disagrees with models.dev, the page wins -- set `QuoteSource`/`QuoteDate` on the row instead of merging this.\n")
	b.WriteString("3. The `Needs a decision` items above are not in this diff. Drop the row or mark it `Unverified` with a reason in a follow-up commit on this branch.\n")
	b.WriteString("4. Merging publishes nothing by itself; the next console release carries it. Customers who used an affected model are emailed by the console once the new price is live.\n")
	return b.String()
}
