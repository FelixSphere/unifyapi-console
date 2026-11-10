package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Alibaba's Singapore explicit cache for qwen3.5-plus, typed by hand from
// https://www.alibabacloud.com/help/en/model-studio/context-cache (read
// 2026-11-08): a hit is 10% of the $0.40 input price, creation 125%.
func TestQwen35PlusBillsCacheHitsAndCreationAtAlibabasExplicitCacheRates(t *testing.T) {
	InitRatioSettings()

	hit, ok := GetCacheRatio("qwen3.5-plus")
	require.True(t, ok, "without a cache ratio a hit bills at the full input price")
	assert.InDelta(t, 0.1, hit, 1e-9)

	creation, ok := GetCreateCacheRatio("qwen3.5-plus")
	require.True(t, ok)
	assert.InDelta(t, 1.25, creation, 1e-9)

	entry, catalogued := CatalogEntryFor("qwen3.5-plus")
	require.True(t, catalogued)
	assert.InDelta(t, 0.04, entry.InputUSD*hit, 1e-9, "$ per 1M cached tokens")
	assert.InDelta(t, 0.5, entry.InputUSD*creation, 1e-9, "$ per 1M tokens written to the cache")
}
