package claude

import (
	"fmt"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const interruptedStreamModel = "claude-sonnet-4-5"

func streamClaudeEvents(t *testing.T, events ...string) *ClaudeResponseInfo {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{
		OriginModelName: interruptedStreamModel,
		RelayFormat:     types.RelayFormatClaude,
		ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: interruptedStreamModel},
	}
	info.SetEstimatePromptTokens(1000)
	claudeInfo := &ClaudeResponseInfo{Usage: &dto.Usage{}}
	for _, event := range events {
		require.Nil(t, HandleStreamResponseData(c, info, claudeInfo, event))
	}
	HandleStreamFinalResponse(c, info, claudeInfo)
	require.NotNil(t, claudeInfo.Usage.BillingUsage)
	require.NotNil(t, claudeInfo.Usage.BillingUsage.ClaudeUsage)
	return claudeInfo
}

func messageStartWithPlaceholderOutput() string {
	return `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"` +
		interruptedStreamModel + `","content":[],"usage":{"input_tokens":1000,"cache_read_input_tokens":400,"output_tokens":1}}}`
}

func deliveredText(sentences int) []string {
	events := []string{`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`}
	for i := 0; i < sentences; i++ {
		events = append(events, fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`,
			"The quick brown fox jumps over the lazy dog. "))
	}
	return events
}

// Settlement bills BillingUsage. When a stream is cut off after message_start
// (client disconnect, network drop, gateway timeout), message_delta never
// arrives, so BillingUsage still holds message_start's placeholder
// output_tokens of 1. Anthropic bills for everything it generated, so the
// delivered text must reach the charge. 200 sentences of about ten tokens each
// were delivered here; billing one token was the defect.
func TestAnInterruptedClaudeStreamBillsTheDeliveredOutput(t *testing.T) {
	claudeInfo := streamClaudeEvents(t, append([]string{messageStartWithPlaceholderOutput()}, deliveredText(200)...)...)

	billed := claudeInfo.Usage.BillingUsage.ClaudeUsage
	assert.GreaterOrEqual(t, billed.OutputTokens, 2_000, "about 2,000 tokens of text were delivered")
	assert.Equal(t, claudeInfo.Usage.CompletionTokens, billed.OutputTokens)
	assert.True(t, claudeInfo.Usage.BillingUsage.Estimated, "the log must show this charge was estimated")
	assert.Equal(t, 1000, billed.InputTokens, "upstream input from message_start is kept")
	assert.Equal(t, 400, billed.CacheReadInputTokens, "upstream cache reads from message_start are kept")
}

// A stream that completes carries the real output count in message_delta.
// That number is the invoice and must not be replaced by an estimate, even
// when the estimate would be higher.
func TestACompletedClaudeStreamBillsTheUpstreamOutputNotAnEstimate(t *testing.T) {
	events := append([]string{messageStartWithPlaceholderOutput()}, deliveredText(200)...)
	events = append(events, `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1777}}`)

	claudeInfo := streamClaudeEvents(t, events...)

	billed := claudeInfo.Usage.BillingUsage.ClaudeUsage
	assert.Equal(t, 1777, billed.OutputTokens)
	assert.False(t, claudeInfo.Usage.BillingUsage.Estimated)
}
