package ratio_setting

// UNIFYAPI-FORK: generate a billing expression from a catalog row.
//
// The four flat fields -- input, output, cache read, cache write -- cannot
// express everything vendors actually charge. An audit against models.dev found
// two shapes we were billing wrong on models already on sale:
//
//	AUDIO INPUT. Google charges 2x to 3.3x the text rate for audio tokens
//	inside an ordinary chat request. We had no field for it, so those tokens
//	fell through to the text price. Worse, upstream's hardcoded prefix chain in
//	operation_setting/tools.go caught gemini-2.5-flash-lite with the PRODUCTION
//	flash constant -- $1.00 against Google's $0.30 -- and charged the customer
//	3.3x too much.
//
//	LONG CONTEXT. Five models on sale cost more past a context threshold:
//	gemini-2.5-pro and the 3.1-pro pair double their input above 200K, and
//	qwen3.6-plus / qwen3.7-plus QUADRUPLE theirs above 256K. We charged the base
//	rate at every length, and long requests are the expensive ones.
//
// Rather than bolt two more special cases onto the ratio maps, these models move
// to the billing-expression engine that already exists (pkg/billingexpr). Its
// coefficients are real $/1M prices -- the same unit the catalog stores -- so
// the expression IS the vendor's price list rather than a translation of it.
//
// THE DISCOUNT MUST BE BAKED IN. Tiered billing applies the group ratio and
// never touches modelRatioMap, so a model moved onto an expression would have
// its ModelDiscount silently stop applying. Every coefficient here is therefore
// multiplied by the discount, and the expressions are regenerated whenever the
// discount table changes -- see RebuildRatioMapsFromCatalog.

import (
	"fmt"
	"strconv"
	"strings"
)

// ContextTier is a vendor's price above a context-length threshold.
//
// One threshold, not a list: every model we sell has at most one, and a slice
// would invite encoding a shape nobody has while making the common case harder
// to read. If a vendor ships two tiers, this becomes a slice then.
type ContextTier struct {
	// ThresholdTokens is the input length above which the tier prices apply.
	ThresholdTokens int
	InputUSD        float64
	OutputUSD       float64
	CacheReadUSD    float64
	CacheWriteUSD   float64
}

// NeedsBillingExpr reports whether the flat ratio maps can express this row.
//
// They can, for all but a handful: a model needs an expression only when audio
// input is priced differently from text, or when a context tier exists.
// Everything else stays on the ratio path, which is simpler and far better
// exercised.
func (e CatalogEntry) NeedsBillingExpr() bool {
	return e.AudioInputUSD > 0 || e.ContextTier != nil
}

// BillingExpr renders this row as a billing expression, with the customer
// discount already applied to every coefficient.
//
// Returns "" when the row does not need one, so callers can range over the
// catalog and skip on empty rather than testing twice.
func (e CatalogEntry) BillingExpr(discount float64) string {
	if !e.NeedsBillingExpr() {
		return ""
	}
	if discount <= 0 {
		discount = 1
	}

	base := e.tierExpr("standard", discount, e.InputUSD, e.OutputUSD, e.CacheReadUSD, e.CacheWriteUSD)
	if e.ContextTier == nil {
		return e.tierExpr("base", discount, e.InputUSD, e.OutputUSD, e.CacheReadUSD, e.CacheWriteUSD)
	}

	long := e.tierExpr("long_context", discount,
		e.ContextTier.InputUSD, e.ContextTier.OutputUSD,
		e.ContextTier.CacheReadUSD, e.ContextTier.CacheWriteUSD)

	// `len`, not `p`: len is the full input length regardless of which
	// sub-categories the expression prices separately, so a cache hit cannot
	// shrink `p` below the threshold and drop a long request into the cheap
	// tier. expr.md is explicit about this and it is the easiest thing here to
	// get wrong.
	return fmt.Sprintf("len <= %d\n  ? %s\n  : %s", e.ContextTier.ThresholdTokens, base, long)
}

// tierExpr renders one tier's cost expression.
func (e CatalogEntry) tierExpr(name string, discount, input, output, cacheRead, cacheWrite float64) string {
	terms := []string{
		"p * " + price(input*discount),
		"c * " + price(output*discount),
	}
	if cacheRead > 0 {
		terms = append(terms, "cr * "+price(cacheRead*discount))
	}
	if cacheWrite > 0 {
		terms = append(terms, "cc * "+price(cacheWrite*discount))
	}
	// Audio input is listed last so the flat part of the expression reads the
	// same whether or not a model has one.
	if e.AudioInputUSD > 0 {
		terms = append(terms, "ai * "+price(e.AudioInputUSD*discount))
	}
	return fmt.Sprintf("tier(%q, %s)", name, strings.Join(terms, " + "))
}

// price formats a coefficient without exponent notation or trailing zeros.
//
// %g would render 0.000001 as 1e-06, which the expression parser does not
// accept; 'f' with -1 precision keeps the shortest exact decimal.
func price(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// BillingExprs returns every model that needs an expression, with discounts
// applied. Keyed by model name, ready to load into billing_setting.
func BillingExprs() map[string]string {
	out := map[string]string{}
	for _, entry := range Catalog() {
		if expr := entry.BillingExpr(GetModelDiscount(entry.Model)); expr != "" {
			out[entry.Model] = expr
		}
	}
	return out
}
