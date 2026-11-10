package service

import (
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
)

// An upstream that reports cache_creation_input_tokens without the 5m/1h
// split must still have those tokens priced on the expression path, at the
// 5-minute rate the ratio path also uses for them.
func TestTieredParamsBillUnsplitClaudeCacheWritesAtTheFiveMinuteRate(t *testing.T) {
	expr := `tier("base", p * 0.1 + c * 0.5 + cr * 0.01 + cc * 0.125 + cc1h * 0.2)`
	usedVars := billingexpr.UsedVars(expr)

	unsplit := &dto.Usage{
		PromptTokens:     1000,
		CompletionTokens: 100,
		UsageSemantic:    "anthropic",
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         2000,
			CachedCreationTokens: 4000,
		},
	}
	params := BuildTieredTokenParams(unsplit, true, usedVars)
	assert.Equal(t, float64(4000), params.CC)
	assert.Equal(t, float64(0), params.CC1h)
	assert.Equal(t, float64(1000+2000+4000), params.Len, "unsplit writes count toward the tier threshold too")

	partlySplit := &dto.Usage{
		PromptTokens:  1000,
		UsageSemantic: "anthropic",
		PromptTokensDetails: dto.InputTokenDetails{
			CachedCreationTokens: 4000,
		},
		ClaudeCacheCreation5mTokens: 1000,
		ClaudeCacheCreation1hTokens: 2000,
	}
	params = BuildTieredTokenParams(partlySplit, true, usedVars)
	assert.Equal(t, float64(2000), params.CC, "the 1000 reported unsplit join the 5-minute writes")
	assert.Equal(t, float64(2000), params.CC1h)
}
