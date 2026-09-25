package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runOaiStream(t *testing.T, estimatedPromptTokens int, lines ...string) (int, int, bool) {
	t.Helper()
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, "stream-usage-fallback")
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"},
		RelayMode:   relayconstant.RelayModeChatCompletions,
		IsStream:    true,
		RelayFormat: types.RelayFormatOpenAI,
		DisablePing: true,
	}
	info.SetEstimatePromptTokens(estimatedPromptTokens)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(strings.Join(append(lines, ""), "\n"))),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}

	usage, apiErr := OaiStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	return usage.PromptTokens, usage.CompletionTokens, c.GetBool(string(constant.ContextKeyLocalCountTokens))
}

func contentChunk(text string) string {
	return `data: {"id":"c1","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":` + quoteJSON(text) + `}}]}`
}

func quoteJSON(s string) string {
	b, _ := common.Marshal(s)
	return string(b)
}

// Upstream usage is the invoice. When the final chunk carries it, those
// numbers are billed exactly and no local estimate is involved.
func TestAnOpenAIStreamBillsTheUpstreamUsageChunkNotAnEstimate(t *testing.T) {
	prompt, completion, estimated := runOaiStream(t, 999,
		contentChunk("Hello"),
		contentChunk(" world"),
		`data: {"id":"c1","object":"chat.completion.chunk","model":"gpt-4o","choices":[],"usage":{"prompt_tokens":1234,"completion_tokens":567,"total_tokens":1801}}`,
		`data: [DONE]`,
	)

	assert.Equal(t, 1234, prompt)
	assert.Equal(t, 567, completion)
	assert.False(t, estimated, "billed from upstream usage, not the local tokenizer")
}

// A stream that ends without a usage chunk (a proxy that strips
// stream_options, an upstream that drops the connection before the last
// chunk) must still bill what was delivered. Upstream charges for those
// tokens either way; billing zero here is pure loss.
func TestAnOpenAIStreamWithoutAUsageChunkStillBillsTheDeliveredText(t *testing.T) {
	sentence := "The quick brown fox jumps over the lazy dog. "
	lines := make([]string, 0, 51)
	for i := 0; i < 50; i++ {
		lines = append(lines, contentChunk(sentence))
	}
	lines = append(lines, `data: [DONE]`)

	prompt, completion, estimated := runOaiStream(t, 800, lines...)

	assert.Equal(t, 800, prompt, "prompt falls back to the pre-request estimate")
	assert.GreaterOrEqual(t, completion, 450, "50 sentences of ten tokens each were delivered")
	assert.True(t, estimated, "the log must show this charge came from a local estimate")
}
