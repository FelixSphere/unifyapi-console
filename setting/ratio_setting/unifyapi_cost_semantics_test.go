package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// claude-opus-5 in the catalog: $5 input, $25 output, $0.5 cached read,
// $6.25 cache write per 1M. Hand-typed, not read from the table under test.
const (
	opusIn    = 5.0
	opusOut   = 25.0
	opusRead  = 0.5
	opusWrite = 6.25
)

// The shape that exposed both defects, taken from production on 2026-09-11:
// 550 claude-opus-5 requests aggregated to prompt 349,222 / cached 12,821,067 /
// write 3,850,857 / completion 273,076. Anthropic reports fresh input only, so
// the prompt count is 349,222 and the two cache buckets sit outside it.
//
// The old model subtracted cached from prompt and clamped when cached was the
// larger, which drove fresh to zero and priced a 12.8M-token cache read as 838
// tokens; it also had no cache-write term at all. Cost came out at $7.00
// against a real $39.05, on $35.18 of revenue -- a line reported as +80%
// margin that was actually losing money.
func TestAnthropicSemanticsPriceEachBucketOnce(t *testing.T) {
	usage := TokenUsage{
		PromptTokens:     349_222,
		CachedTokens:     12_821_067,
		CacheWriteTokens: 3_850_857,
		CompletionTokens: 273_076,
		Semantic:         UsageSemanticAnthropic,
	}

	got, ok := ListPriceUSD("claude-opus-5", usage)
	require.True(t, ok)

	const m = 1_000_000.0
	want := 349_222/m*opusIn + 12_821_067/m*opusRead +
		3_850_857/m*opusWrite + 273_076/m*opusOut
	assert.InDelta(t, want, got, 0.000001)
	assert.InDelta(t, 39.05, got, 0.01, "the figure this defect was measured against")
	assert.Greater(t, got, 30.0, "a prompt count smaller than the cache read must not collapse the cost")
}

// OpenAI reports a prompt total that already contains the cached reads, so the
// buckets must come back out or they are charged twice.
func TestOpenAISemanticsSubtractTheBucketsFromTheTotal(t *testing.T) {
	usage := TokenUsage{
		PromptTokens:     1_000_000, // total, cache included
		CachedTokens:     400_000,
		CacheWriteTokens: 100_000,
		CompletionTokens: 0,
		Semantic:         "", // OpenAI rows carry no anthropic marker
	}

	got, ok := ListPriceUSD("claude-opus-5", usage)
	require.True(t, ok)

	const m = 1_000_000.0
	want := 500_000/m*opusIn + 400_000/m*opusRead + 100_000/m*opusWrite
	assert.InDelta(t, want, got, 0.000001)
}

// A cache write is dearer than fresh input, so omitting it understates cost.
// Same tokens, same model, only the bucket differs.
func TestCacheWriteCostsMoreThanFreshInput(t *testing.T) {
	write, ok := ListPriceUSD("claude-opus-5", TokenUsage{
		PromptTokens: 0, CacheWriteTokens: 1_000_000, Semantic: UsageSemanticAnthropic})
	require.True(t, ok)
	fresh, ok := ListPriceUSD("claude-opus-5", TokenUsage{
		PromptTokens: 1_000_000, Semantic: UsageSemanticAnthropic})
	require.True(t, ok)

	assert.InDelta(t, opusWrite, write, 0.000001)
	assert.InDelta(t, opusIn, fresh, 0.000001)
	assert.Greater(t, write, fresh, "a write priced as fresh input is the understatement this fixes")
}

// gpt-4o publishes a cached-read price but no write premium, so writes stay at
// input price instead of becoming free.
func TestNoPublishedWritePriceFallsBackToInput(t *testing.T) {
	got, ok := ListPriceUSD("gpt-4o", TokenUsage{
		PromptTokens: 0, CacheWriteTokens: 1_000_000, Semantic: UsageSemanticAnthropic})
	require.True(t, ok)
	assert.InDelta(t, 2.5, got, 0.000001, "gpt-4o input is $2.50/1M")
}

// Buckets larger than the total they are supposed to sit inside must not drive
// the fresh count negative and refund the completion cost.
func TestOverlargeBucketsCannotProduceNegativeCost(t *testing.T) {
	got, ok := ListPriceUSD("claude-opus-5", TokenUsage{
		PromptTokens:     100,
		CachedTokens:     900_000,
		CacheWriteTokens: 900_000,
		CompletionTokens: 0,
	})
	require.True(t, ok)
	assert.GreaterOrEqual(t, got, 0.0)
}

func TestUnpricedModelStaysUnknown(t *testing.T) {
	_, ok := ListPriceUSD("not-in-the-catalog", TokenUsage{PromptTokens: 1000})
	assert.False(t, ok)
}
