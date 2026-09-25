package service

import (
	"net/http/httptest"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// OpenAI reports reasoning inside completion_tokens and cache hits inside
// prompt_tokens. Each item must be billed once, at its own rate. Prices are
// typed by hand ($1.25 in, $0.125 cached, $10 out per 1M) so the expected
// number does not come from the table under test.
//
//	fresh input  4,000 x $1.25/1M  = $0.00500
//	cache read   6,000 x $0.125/1M = $0.00075
//	output       2,000 x $10/1M    = $0.02000  (1,500 of it reasoning)
//	total                            $0.02575  = 12,875 quota at $1 = 500,000
//
// Counting reasoning a second time would bill $0.04075; charging the cache hit
// at the full input rate as well would bill $0.03325.
func TestOpenAIReasoningAndCachedTokensAreEachBilledOnceAtTheirOwnRate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:             types.RelayFormatOpenAI,
		FinalRequestRelayFormat: types.RelayFormatOpenAI,
		OriginModelName:         "gpt-5",
		PriceData: hosttypes.PriceData{
			ModelRatio:      0.625, // $1.25 per 1M input
			CompletionRatio: 8,     // $10 per 1M output
			CacheRatio:      0.1,   // $0.125 per 1M cached input
			GroupRatioInfo:  hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}
	usage := &dto.Usage{
		PromptTokens:     10_000,
		CompletionTokens: 2_000,
		TotalTokens:      12_000,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 6_000,
		},
		CompletionTokenDetails: dto.OutputTokenDetails{
			ReasoningTokens: 1_500,
		},
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, effectiveBillingUsage(usage))

	require.False(t, summary.IsClaudeUsageSemantic)
	assert.Equal(t, 6_000, summary.CacheTokens)
	assert.Equal(t, 2_000, summary.CompletionTokens)
	assert.Equal(t, 12_875, summary.Quota)
}
