package ratio_setting

// UNIFYAPI-FORK: the pricing baseline.
//
// Upstream (new-api) keeps its baseline as raw billing ratios in
// defaultModelRatio & friends, spread across five parallel maps keyed by model
// name. That shape has two problems we hit in production:
//
//  1. A ratio is not a price. "2.5" only means $5/1M once you know the
//     USD == 500 quota convention, so a typo (0.2125 for 2.5) reads as
//     plausible and shipped an 11.8x underprice on claude-opus-4-8.
//  2. Nothing tied a ratio back to what the vendor actually charges, so no
//     check could ever say the baseline was stale.
//
// So the fork's baseline is a single table of OFFICIAL VENDOR LIST PRICES in
// USD per 1M tokens -- the number the vendor publishes, nothing else. Every
// billing ratio is derived from it. Customer discounts are NOT expressed here:
// a per-model discount lives in ModelDiscount (unifyapi_discount.go) and a
// per-group one in GroupRatio, so a model has exactly one price basis and every
// discount is visible in its own table.
//
//	customer price = official list price  x  model discount  x  group ratio
//
// Vendor + UpstreamModel point at the models.dev id this row was taken from,
// which is what makes the baseline checkable: scripts/pricing-drift re-fetches
// models.dev and diffs it against this table. Entries marked Unverified have no
// models.dev listing (retired model, non-standard name, or per-second video
// billing) -- they carry the price that was live when the table was written and
// the drift check reports them every run so they cannot rot silently.
//
// To add a model: add a row here, run `go run ./scripts/gen-pricing-seed` to
// regenerate seed-pricing.sql, and reseed. Do not hand-edit ratios in the admin
// UI -- see docs/PRICING-AND-DISCOUNTS.md.

// PricingSnapshotDate is the day the official prices below were last verified
// against models.dev. scripts/pricing-drift compares against it.
const PricingSnapshotDate = "2026-09-02"

// CatalogEntry is one model's official vendor list price. Prices are USD per
// 1M tokens, exactly as the vendor publishes them. A zero CacheReadUSD or
// CacheWriteUSD means the vendor publishes no such price for this model.
type CatalogEntry struct {
	Model         string
	Vendor        string // models.dev provider id; "" when unlisted
	UpstreamModel string // models.dev model id, when it differs from Model
	InputUSD      float64
	OutputUSD     float64
	CacheReadUSD  float64
	CacheWriteUSD float64
	Unverified    bool // no models.dev listing; needs a manual quote

	// AdminAdded marks a price typed into the console rather than compiled in --
	// see unifyapi_extra_models.go. It flows through to the pricing page and the
	// drift checker because nobody is watching these for vendor price changes,
	// and a price with no provenance must never look like one that has it.
	AdminAdded bool

	// QuoteSource and QuoteDate record a price read directly off the vendor's own
	// price list, by hand, on a date. They exist because models.dev is an
	// aggregator and aggregators lag: on 2026-08-30 it still carried DeepSeek's
	// pre-August prices, seventeen days after DeepSeek raised them, so
	// pricing-drift reported "no drift" on two models we were selling below cost.
	//
	// A dated direct quote therefore OUTRANKS the feed. When they disagree, the
	// drift checker reports the feed as stale rather than the catalog as wrong --
	// see scripts/pricing-drift. Clear both fields when the feed catches up.
	QuoteSource string
	QuoteDate   string // YYYY-MM-DD

	// PerCallUSD prices a model per request instead of per token (image and
	// video generation work this way). Zero means per-token. Set it and the model moves to quota_type 1;
	// PriceUnit distinguishes a whole request from a second of video.
	PerCallUSD float64
	PriceUnit  string // "second" for video rates; empty retains per-request billing

	// ImageRatio / AudioRatio / AudioCompletionRatio are multipliers for models
	// that bill image or audio tokens at a different rate from text. Zero means
	// "no separate rate", which is every catalogued model today.
	//
	// They live here rather than in upstream's parallel default maps so that
	// every price a model has comes from its one catalog row, and the admin
	// pricing page never lists a model UnifyAPI does not sell.
	ImageRatio           float64
	AudioRatio           float64
	AudioCompletionRatio float64

	// AudioInputUSD is the vendor's price for audio tokens inside an ordinary
	// chat request, when it differs from the text input price. Zero means
	// audio bills at the text rate, which is true for most models.
	//
	// Setting it moves the model onto a billing expression -- the flat ratio
	// maps have nowhere to put it. See unifyapi_billing_expr.go.
	AudioInputUSD float64

	// ContextTier is the vendor's price above a context-length threshold, for
	// models that charge more for long inputs. Nil for a flat-priced model.
	ContextTier *ContextTier
}

// UpstreamID is the models.dev model id this row was priced from.
func (e CatalogEntry) UpstreamID() string {
	if e.UpstreamModel != "" {
		return e.UpstreamModel
	}
	return e.Model
}

// unifyapiCatalog is the complete set of models UnifyAPI prices. A model that
// is not in this table has no price and is refused at relay time; that is
// deliberate, so an unpriced model can never be served by accident.
var unifyapiCatalog = []CatalogEntry{
	// Video list prices are USD per output second; adaptors apply duration and resolution.
	// HappyHorse uses the official international (Singapore) list price.
	{Model: "wan3.0-video", PerCallUSD: 0.10, PriceUnit: "second", Unverified: true, QuoteSource: "https://www.alibabacloud.com/help/en/model-studio/model-pricing", QuoteDate: "2026-09-10"},
	{Model: "wan3.0-video-prime", PerCallUSD: 0.14, PriceUnit: "second", Unverified: true, QuoteSource: "https://www.alibabacloud.com/help/en/model-studio/model-pricing", QuoteDate: "2026-09-10"},
	{Model: "happyhorse-1.1-t2v", Vendor: "", PerCallUSD: 0.14, PriceUnit: "second", Unverified: true, QuoteSource: "https://www.alibabacloud.com/help/en/model-studio/model-pricing", QuoteDate: "2026-09-10"},
	{Model: "happyhorse-1.1-i2v", Vendor: "", PerCallUSD: 0.14, PriceUnit: "second", Unverified: true, QuoteSource: "https://www.alibabacloud.com/help/en/model-studio/model-pricing", QuoteDate: "2026-09-10"},
	{Model: "happyhorse-1.1-r2v", Vendor: "", PerCallUSD: 0.14, PriceUnit: "second", Unverified: true, QuoteSource: "https://www.alibabacloud.com/help/en/model-studio/model-pricing", QuoteDate: "2026-09-10"},
	{Model: "MiniMax-H3", Vendor: "", PerCallUSD: 0.08, PriceUnit: "second", Unverified: true, QuoteSource: "https://platform.minimax.io/docs/guides/pricing-paygo", QuoteDate: "2026-09-10"},
	{Model: "MiniMax-H3-Max", Vendor: "", PerCallUSD: 0.08, PriceUnit: "second", Unverified: true, QuoteSource: "https://platform.minimax.io/docs/guides/pricing-paygo", QuoteDate: "2026-09-10"},
	// Seedance 2.5 reports completion_tokens/total_tokens when the async task
	// finishes, so it remains token-billed. $10.70/M is the no-video baseline;
	// the Doubao task adaptor applies 6.40/10.70 when the request contains video.
	// The doubao-prefixed name is retained as a compatibility alias.
	{Model: "seedance-2.5", Vendor: "", InputUSD: 10.7, OutputUSD: 10.7, Unverified: true, QuoteSource: "https://ai.byteplus.com/en", QuoteDate: "2026-09-10"},
	{Model: "doubao-seedance-2-5-260628", Vendor: "", InputUSD: 10.7, OutputUSD: 10.7, Unverified: true, QuoteSource: "https://ai.byteplus.com/en", QuoteDate: "2026-09-10"},
	{Model: "dreamina-seedance-2-5-260628", Vendor: "", InputUSD: 10.7, OutputUSD: 10.7, Unverified: true, QuoteSource: "https://ai.byteplus.com/en", QuoteDate: "2026-09-10"},

	// ---- unlisted vendor ----
	{Model: "nano-banana-pro-preview", Vendor: "google", UpstreamModel: "gemini-3-pro-image", InputUSD: 2, OutputUSD: 120, CacheReadUSD: 0, CacheWriteUSD: 0},

	// ---- Anthropic ----
	{Model: "claude-fable-5", Vendor: "anthropic", InputUSD: 10, OutputUSD: 50, CacheReadUSD: 1, CacheWriteUSD: 12.5},
	{Model: "claude-fable-5.1", Vendor: "anthropic", UpstreamModel: "claude-fable-5-1", InputUSD: 10, OutputUSD: 50, CacheReadUSD: 0.25, CacheWriteUSD: 12.5,
		QuoteSource: "https://www.anthropic.com/claude/fable", QuoteDate: "2026-09-01"},
	{Model: "claude-haiku-4-5-20251001", Vendor: "anthropic", InputUSD: 1, OutputUSD: 5, CacheReadUSD: 0.1, CacheWriteUSD: 1.25},
	{Model: "claude-opus-4-5", Vendor: "anthropic", InputUSD: 5, OutputUSD: 25, CacheReadUSD: 0.5, CacheWriteUSD: 6.25},
	{Model: "claude-opus-4-6", Vendor: "anthropic", InputUSD: 5, OutputUSD: 25, CacheReadUSD: 0.5, CacheWriteUSD: 6.25},
	{Model: "claude-opus-4-7", Vendor: "anthropic", InputUSD: 5, OutputUSD: 25, CacheReadUSD: 0.5, CacheWriteUSD: 6.25},
	{Model: "claude-opus-4-8", Vendor: "anthropic", InputUSD: 5, OutputUSD: 25, CacheReadUSD: 0.5, CacheWriteUSD: 6.25},
	{Model: "claude-opus-5", Vendor: "anthropic", InputUSD: 5, OutputUSD: 25, CacheReadUSD: 0.5, CacheWriteUSD: 6.25},
	{Model: "claude-sonnet-4-5", Vendor: "anthropic", InputUSD: 3, OutputUSD: 15, CacheReadUSD: 0.3, CacheWriteUSD: 3.75},
	{Model: "claude-sonnet-4-6", Vendor: "anthropic", InputUSD: 3, OutputUSD: 15, CacheReadUSD: 0.3, CacheWriteUSD: 3.75},
	{Model: "claude-sonnet-5", Vendor: "anthropic", InputUSD: 2, OutputUSD: 10, CacheReadUSD: 0.2, CacheWriteUSD: 2.5},

	// ---- OpenAI ----
	{Model: "gpt-4.1-mini", Vendor: "openai", InputUSD: 0.4, OutputUSD: 1.6, CacheReadUSD: 0.1, CacheWriteUSD: 0},
	{Model: "gpt-4o", Vendor: "openai", InputUSD: 2.5, OutputUSD: 10, CacheReadUSD: 1.25, CacheWriteUSD: 0},
	{Model: "gpt-4o-mini", Vendor: "openai", InputUSD: 0.15, OutputUSD: 0.6, CacheReadUSD: 0.075, CacheWriteUSD: 0},
	{Model: "gpt-5", Vendor: "openai", InputUSD: 1.25, OutputUSD: 10, CacheReadUSD: 0.125, CacheWriteUSD: 0,
		QuoteSource: "https://models.dev/api.json (openai/gpt-5)", QuoteDate: "2026-08-30"},
	{Model: "gpt-5-mini", Vendor: "openai", InputUSD: 0.25, OutputUSD: 2, CacheReadUSD: 0.025, CacheWriteUSD: 0},
	// Standard tier. OpenAI also publishes a higher band above a 272k context
	// ($5/$22.5); the catalog carries one price per model, and the standard
	// tier is what the relay bills, so that is what is recorded here.
	{Model: "gpt-5.4", Vendor: "openai", InputUSD: 2.5, OutputUSD: 15, CacheReadUSD: 0.25, CacheWriteUSD: 0,
		QuoteSource: "https://models.dev/api.json (openai/gpt-5.4)", QuoteDate: "2026-08-30"},
	// Standard tier. OpenAI also publishes a higher band above a 272k context
	// ($20/$75); the catalog carries one price per model, and the standard tier
	// is what the relay bills, so that is what is recorded here.
	//
	// Unlike the rest of the OpenAI range this one has a published cache-write
	// price, so it is the first OpenAI model whose cost depends on
	// CacheWriteUSD being honoured.
	{Model: "gpt-6-astra", Vendor: "openai", InputUSD: 10, OutputUSD: 50, CacheReadUSD: 1, CacheWriteUSD: 12.5,
		QuoteSource: "https://models.dev/api.json (openai/gpt-6-astra)", QuoteDate: "2026-09-11"},
	{Model: "gpt-image-2", Vendor: "openai", InputUSD: 5, OutputUSD: 30, CacheReadUSD: 1.25, CacheWriteUSD: 0},

	// ---- Moonshot ----
	// Moonshot retired kimi-k2.5 from its published list -- it is gone from
	// models.dev's moonshotai provider, which now carries only k2.6, k2.7-code,
	// k2.7-code-highspeed and k3. We still sell it and customers still call it
	// (111 billed requests, 4 in the last seven days), served through two
	// enabled channels, so dropping the row would send live traffic to the 37.5
	// fallback and get it refused.
	//
	// Unverified rather than removed, therefore: the price below is the last
	// published one and it keeps billing exactly as before, but nothing can
	// check it any more. Vendor is cleared because the catalog's invariant is
	// Unverified <=> no vendor -- a named vendor asserts "models.dev can verify
	// this", which is now false. Only a human re-reading Moonshot's pricing page
	// can catch the next change, or the model should be withdrawn.
	{Model: "kimi-k2.5", Vendor: "", InputUSD: 0.6, OutputUSD: 3, CacheReadUSD: 0.1, CacheWriteUSD: 0, Unverified: true,
		QuoteSource: "https://platform.moonshot.ai/docs/pricing (retired from the published list; last known price)", QuoteDate: "2026-09-07"},
	{Model: "kimi-k2.6", Vendor: "moonshotai", InputUSD: 0.95, OutputUSD: 4, CacheReadUSD: 0.16, CacheWriteUSD: 0},
	{Model: "kimi-k3", Vendor: "moonshotai", InputUSD: 3, OutputUSD: 15, CacheReadUSD: 0.3, CacheWriteUSD: 0},

	// ---- 智谱 ----
	{Model: "glm-4.7", Vendor: "zhipuai", InputUSD: 0.6, OutputUSD: 2.2, CacheReadUSD: 0.11, CacheWriteUSD: 0},
	{Model: "glm-5", Vendor: "zhipuai", InputUSD: 1, OutputUSD: 3.2, CacheReadUSD: 0.2, CacheWriteUSD: 0},
	// glm-5-turbo is CHINA-ONLY. Zhipu's international list (docs.z.ai, which is
	// what models.dev mirrors) does not carry it, so there is no official USD
	// price to copy. Zhipu's own CNY list, read 2026-08-30, is tiered by input
	// length:
	//
	//	input < 32K   in CNY 5   out CNY 22   cache CNY 1.2
	//	input >= 32K  in CNY 7   out CNY 26   cache CNY 1.8
	//
	// The figures below are the CHEAP tier at roughly 6.9 CNY/USD. Two open
	// questions, both business decisions rather than lookups: whether that rate
	// is still the one to use, and that a single USD number cannot express a
	// tier -- every request over 32K input costs 40% more than we charge for it.
	{Model: "glm-5-turbo", Vendor: "", InputUSD: 0.72, OutputUSD: 3.2, CacheReadUSD: 0.144, CacheWriteUSD: 0, Unverified: true},
	{Model: "glm-5.1", Vendor: "zhipuai", InputUSD: 1.4, OutputUSD: 4.4, CacheReadUSD: 0.26, CacheWriteUSD: 0},
	{Model: "glm-5.2", Vendor: "zhipuai", InputUSD: 1.4, OutputUSD: 4.4, CacheReadUSD: 0.26, CacheWriteUSD: 0},
	// Same price as 5.1 and 5.2, confirmed on Zhipu's own international list
	// (docs.z.ai) and by models.dev's zhipuai provider. Cached-input storage is
	// "limited-time free", hence CacheWriteUSD 0.
	{Model: "glm-5.3", Vendor: "zhipuai", InputUSD: 1.4, OutputUSD: 4.4, CacheReadUSD: 0.26, CacheWriteUSD: 0},

	// ---- Google ----
	{Model: "gemini-2.5-flash", Vendor: "google", InputUSD: 0.3, OutputUSD: 2.5, CacheReadUSD: 0.03, CacheWriteUSD: 0, AudioInputUSD: 1.0},
	{Model: "gemini-2.5-flash-image", Vendor: "google", InputUSD: 0.3, OutputUSD: 30, CacheReadUSD: 0.075, CacheWriteUSD: 0},
	{Model: "gemini-2.5-flash-lite", Vendor: "google", InputUSD: 0.1, OutputUSD: 0.4, CacheReadUSD: 0.01, CacheWriteUSD: 0, AudioInputUSD: 0.3},
	{Model: "gemini-2.5-pro", Vendor: "google", InputUSD: 1.25, OutputUSD: 10, CacheReadUSD: 0.125, CacheWriteUSD: 0,
		ContextTier: &ContextTier{ThresholdTokens: 200000, InputUSD: 2.5, OutputUSD: 15, CacheReadUSD: 0.25}},
	{Model: "gemini-3-flash-preview", Vendor: "google", InputUSD: 0.5, OutputUSD: 3, CacheReadUSD: 0.05, CacheWriteUSD: 0, AudioInputUSD: 1.0},
	{Model: "gemini-3-pro-image", Vendor: "google", InputUSD: 2, OutputUSD: 120, CacheReadUSD: 0, CacheWriteUSD: 0},
	{Model: "gemini-3.1-flash-image", Vendor: "google", InputUSD: 0.5, OutputUSD: 60, CacheReadUSD: 0, CacheWriteUSD: 0},
	// Nano Banana 2 Lite. Google quotes TWO output rates for every image model
	// -- text/thinking tokens and image tokens -- and this row carries the
	// IMAGE rate ($30.00/1M, "equivalent to $0.0336 per 1K resolution image"),
	// which is the convention the three image rows above already follow:
	// 3-pro-image 120 not 12, 3.1-flash-image 60 not 3.
	//
	// That deliberately overcharges the text/thinking tokens of an image
	// request rather than undercharging its image tokens. Splitting the two
	// needs a billing expression, not a flat row; until then this errs the
	// safe way. Google publishes no cache price for it.
	{Model: "gemini-3.1-flash-lite-image", Vendor: "", InputUSD: 0.25, OutputUSD: 30, CacheReadUSD: 0, CacheWriteUSD: 0, Unverified: true,
		QuoteSource: "https://ai.google.dev/gemini-api/docs/pricing (image output rate; $1.50/1M for text and thinking)", QuoteDate: "2026-09-10"},
	{Model: "gemini-3.1-flash-lite-preview", Vendor: "google", InputUSD: 0.25, OutputUSD: 1.5, CacheReadUSD: 0.025, CacheWriteUSD: 0, AudioInputUSD: 0.5},
	{Model: "gemini-3.1-pro-preview", Vendor: "google", InputUSD: 2, OutputUSD: 12, CacheReadUSD: 0.2, CacheWriteUSD: 0,
		ContextTier: &ContextTier{ThresholdTokens: 200000, InputUSD: 4, OutputUSD: 18, CacheReadUSD: 0.4}},
	{Model: "gemini-3.1-pro-preview-customtools", Vendor: "google", InputUSD: 2, OutputUSD: 12, CacheReadUSD: 0.2, CacheWriteUSD: 0,
		ContextTier: &ContextTier{ThresholdTokens: 200000, InputUSD: 4, OutputUSD: 18, CacheReadUSD: 0.4}},
	// gemini-3.7-flash and gemini-3.8-flash are on PROMOTIONAL pricing.
	//
	// Google lists $0.75 in / $3.75 out / $0.075 cached "through December 31,
	// 2026", then "$1.50 / $7.50 / $0.15 starting January 1, 2027" -- a
	// doubling, already published, with a date on it. The promotional figure is
	// the official price today, so it is what we sell at today; but nothing
	// about a price in a Go file expires by itself, and on 2027-01-01 our cost
	// doubles while the catalogue keeps quoting half of it.
	//
	// TestPromotionalPricesHaveNotExpired fails from 2026-12-15 so this cannot
	// be forgotten. When it does: set both rows to 1.5 / 7.5 / 0.15 and move
	// the deadline, or drop the models.
	{Model: "gemini-3.8-flash", Vendor: "", InputUSD: 0.75, OutputUSD: 3.75, CacheReadUSD: 0.075, CacheWriteUSD: 0, Unverified: true,
		QuoteSource: "https://ai.google.dev/gemini-api/docs/pricing (promotional through 2026-12-31; $1.50/$7.50 from 2027-01-01)", QuoteDate: "2026-09-10"},
	{Model: "gemini-3.7-flash", Vendor: "", InputUSD: 0.75, OutputUSD: 3.75, CacheReadUSD: 0.075, CacheWriteUSD: 0, Unverified: true,
		QuoteSource: "https://ai.google.dev/gemini-api/docs/pricing (promotional through 2026-12-31; $1.50/$7.50 from 2027-01-01)", QuoteDate: "2026-09-10"},
	// Vendor is deliberately blank: the pinned models.dev fixture (2026-09-02)
	// predates this model, so the drift checker cannot verify it and would
	// report it as retired. Unverified + a dated quote off Google's own page is
	// the designed way to say "priced from the vendor, not the aggregator".
	// Clear the flag and set Vendor: "google" once the fixture is refreshed.
	{Model: "gemini-3.5-flash", Vendor: "", InputUSD: 1.5, OutputUSD: 9, CacheReadUSD: 0.15, CacheWriteUSD: 0, Unverified: true,
		QuoteSource: "https://ai.google.dev/gemini-api/docs/pricing", QuoteDate: "2026-09-08"},
	{Model: "gemini-flash-latest", Vendor: "google", InputUSD: 0.75, OutputUSD: 3.75, CacheReadUSD: 0.075, CacheWriteUSD: 0},
	{Model: "gemini-flash-lite-latest", Vendor: "google", InputUSD: 0.3, OutputUSD: 2.5, CacheReadUSD: 0.03, CacheWriteUSD: 0},
	{Model: "gemini-pro-latest", Vendor: "google", UpstreamModel: "gemini-3.1-pro-preview", InputUSD: 2, OutputUSD: 12, CacheReadUSD: 0.2, CacheWriteUSD: 0},

	// ---- MiniMax ----
	{Model: "MiniMax-M2.5", Vendor: "minimax", InputUSD: 0.3, OutputUSD: 1.2, CacheReadUSD: 0.03, CacheWriteUSD: 0.375},
	{Model: "MiniMax-M2.7", Vendor: "minimax", InputUSD: 0.3, OutputUSD: 1.2, CacheReadUSD: 0.06, CacheWriteUSD: 0.375},

	// ---- 阿里巴巴 ----
	{Model: "qwen3.5-27b", Vendor: "alibaba", InputUSD: 0.3, OutputUSD: 2.4, CacheReadUSD: 0, CacheWriteUSD: 0},
	{Model: "qwen3.5-35b-a3b", Vendor: "alibaba", InputUSD: 0.25, OutputUSD: 2, CacheReadUSD: 0, CacheWriteUSD: 0},
	{Model: "qwen3.5-397b-a17b", Vendor: "alibaba", InputUSD: 0.6, OutputUSD: 3.6, CacheReadUSD: 0, CacheWriteUSD: 0},
	// Confirmed against Alibaba's own Singapore price list: $0.1 in / $0.4 out,
	// one flat 0-1M bracket. Still Unverified because models.dev does not carry
	// it, so only a human re-reading that page can catch the next change.
	// CacheReadUSD is a local estimate -- Alibaba publishes no separate cache
	// rate for this model.
	{Model: "qwen3.5-flash", Vendor: "", InputUSD: 0.1, OutputUSD: 0.4, CacheReadUSD: 0.05, CacheWriteUSD: 0, Unverified: true,
		QuoteSource: "https://www.alibabacloud.com/help/en/model-studio/model-pricing", QuoteDate: "2026-08-30"},
	{Model: "qwen3.5-plus", Vendor: "alibaba", InputUSD: 0.4, OutputUSD: 2.4, CacheReadUSD: 0, CacheWriteUSD: 0},
	{Model: "qwen3.6-max-preview", Vendor: "alibaba", InputUSD: 1.3, OutputUSD: 7.8, CacheReadUSD: 0.13, CacheWriteUSD: 1.625},
	{Model: "qwen3.6-plus", Vendor: "alibaba", InputUSD: 0.5, OutputUSD: 3, CacheReadUSD: 0.05, CacheWriteUSD: 0.625,
		ContextTier: &ContextTier{ThresholdTokens: 256000, InputUSD: 2, OutputUSD: 6, CacheReadUSD: 0.2, CacheWriteUSD: 2.5}},
	{Model: "qwen3.7-max", Vendor: "alibaba", InputUSD: 2.5, OutputUSD: 7.5, CacheReadUSD: 0.5, CacheWriteUSD: 3.125},
	{Model: "qwen3.7-plus", Vendor: "alibaba", InputUSD: 0.5, OutputUSD: 3, CacheReadUSD: 0.05, CacheWriteUSD: 0.625,
		ContextTier: &ContextTier{ThresholdTokens: 256000, InputUSD: 2, OutputUSD: 6, CacheReadUSD: 0.2, CacheWriteUSD: 2.5}},
	{Model: "qwen3.8-max", Vendor: "alibaba", InputUSD: 2, OutputUSD: 6, CacheReadUSD: 0.25, CacheWriteUSD: 2.5},

	// ---- DeepSeek ----
	// RETIRED BY THE VENDOR -- these three have no price because they no longer
	// exist as products. DeepSeek folded V3.2 into V4 in April 2026 and retired
	// the legacy names on 2026-07-24; its price list (checked 2026-08-30) shows
	// only the v4 generation. The prices below are whatever was true before
	// that and cannot be refreshed.
	//
	// They are still advertised on production to all three groups, so they are
	// sold and then fail upstream. Delisting them is a customer-visible change
	// and needs the same review as any other repricing, which is why they are
	// still here rather than quietly deleted.
	{Model: "deepseek-v3", Vendor: "", InputUSD: 0.287, OutputUSD: 1.147, CacheReadUSD: 0, CacheWriteUSD: 0, Unverified: true},
	{Model: "deepseek-v3.2", Vendor: "", InputUSD: 0.18, OutputUSD: 0.35, CacheReadUSD: 0.04, CacheWriteUSD: 0, Unverified: true},
	{Model: "deepseek-v3.2-thinking", Vendor: "", InputUSD: 0.29, OutputUSD: 0.43, CacheReadUSD: 0, CacheWriteUSD: 0, Unverified: true},
	// DeepSeek raised prices at 16:00 UTC on 2026-08-16 (announced 2026-08-13)
	// and moved to peak/off-peak billing, off-peak being half of peak. The PEAK
	// price is carried here deliberately: which tier a request lands in is the
	// vendor's clock, not something we can know when quoting a customer, so
	// pricing off-peak would sell below cost for part of every weekday.
	{Model: "deepseek-v4-flash", Vendor: "deepseek", InputUSD: 0.44, OutputUSD: 1.32, CacheReadUSD: 0.014, CacheWriteUSD: 0,
		QuoteSource: "https://api-docs.deepseek.com/quick_start/pricing (peak tier)", QuoteDate: "2026-08-30"},
	{Model: "deepseek-v4-pro", Vendor: "deepseek", InputUSD: 1.32, OutputUSD: 3.96, CacheReadUSD: 0.044, CacheWriteUSD: 0,
		QuoteSource: "https://api-docs.deepseek.com/quick_start/pricing (peak tier)", QuoteDate: "2026-08-30"}}
