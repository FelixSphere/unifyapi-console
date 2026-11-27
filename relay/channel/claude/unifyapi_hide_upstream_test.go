package claude

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// UNIFYAPI-FORK: what a client of a Claude-type channel pointed at a reseller
// receives on /v1/messages, with and without the channel's hide_upstream
// setting.
//
// The fixtures carry the three things production showed on 2026-11-26 -- the
// reseller's model id, its "provider" key and its own response id -- in an
// otherwise ordinary Messages answer. They are written out by hand, not
// recorded.

const (
	hideUpstreamRequestID      = "20261126093015123456789a1b2c3d4"
	hideUpstreamRequestedModel = "claude-fable-5.1"

	resellerClaudeMessage = `{"id":"gen-1795741100-QwErTy","type":"message","role":"assistant","model":"anthropic/claude-fable-5.1","content":[{"type":"text","text":"Let me check."},{"type":"tool_use","id":"toolu_01A","name":"get_weather","input":{"city":"Paris"}}],"stop_reason":"tool_use","stop_sequence":null,"usage":{"input_tokens":412,"cache_creation_input_tokens":0,"cache_read_input_tokens":300,"output_tokens":57,"cost":0.00213,"is_byok":false},"provider":"Anthropic"}`

	hiddenClaudeMessage = `{"id":"msg_20261126093015123456789a1b2c3d4","type":"message","role":"assistant","model":"claude-fable-5.1","content":[{"type":"text","text":"Let me check."},{"type":"tool_use","id":"toolu_01A","name":"get_weather","input":{"city":"Paris"}}],"stop_reason":"tool_use","stop_sequence":null,"usage":{"input_tokens":412,"cache_creation_input_tokens":0,"cache_read_input_tokens":300,"output_tokens":57}}`
)

var resellerClaudeStream = []string{
	`data: {"type":"message_start","message":{"id":"gen-1795741101-AsDfGh","type":"message","role":"assistant","model":"anthropic/claude-fable-5.1","provider":"Anthropic","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":412,"cache_creation_input_tokens":0,"cache_read_input_tokens":300,"output_tokens":1}}}`,
	`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
	`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
	`data: {"type":"content_block_stop","index":0}`,
	`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":412,"cache_creation_input_tokens":0,"cache_read_input_tokens":300,"output_tokens":57}}`,
	`data: {"type":"message_stop"}`,
}

func hideUpstreamClaudeTest(t *testing.T, stream bool, channelSettings string) (*gin.Context, *httptest.ResponseRecorder, *relaycommon.RelayInfo) {
	t.Helper()
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	var settings dto.ChannelOtherSettings
	require.NoError(t, common.UnmarshalJsonStr(channelSettings, &settings))

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	// What the request-id middleware and the distributor leave in the context.
	c.Set(common.RequestIdKey, hideUpstreamRequestID)
	c.Header(common.RequestIdKey, hideUpstreamRequestID)
	c.Set(string(constant.ContextKeyOriginalModel), hideUpstreamRequestedModel)
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, settings)

	info := &relaycommon.RelayInfo{
		OriginModelName: hideUpstreamRequestedModel,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:          constant.ChannelTypeAnthropic,
			UpstreamModelName:    "anthropic/claude-fable-5.1",
			IsModelMapped:        true,
			ChannelOtherSettings: settings,
		},
		RelayFormat: types.RelayFormatClaude,
		IsStream:    stream,
		DisablePing: true,
	}
	info.SetEstimatePromptTokens(412)
	return c, recorder, info
}

func relayResellerClaudeMessage(t *testing.T, channelSettings string) (*httptest.ResponseRecorder, *dto.Usage) {
	t.Helper()
	c, recorder, info := hideUpstreamClaudeTest(t, false, channelSettings)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":         {"application/json"},
			"X-Openrouter-Release": {"2026.11.26-1"},
			"X-Generation-Id":      {"gen-1795741100-QwErTy"},
			"Cf-Ray":               {"9a1b2c3d4e5f6a7b-SJC"},
		},
		Body: io.NopCloser(strings.NewReader(resellerClaudeMessage)),
	}
	usage, apiErr := ClaudeHandler(c, resp, info)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	return recorder, usage
}

func relayResellerClaudeStream(t *testing.T, channelSettings string) (string, *dto.Usage) {
	t.Helper()
	c, recorder, info := hideUpstreamClaudeTest(t, true, channelSettings)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(strings.Join(append(resellerClaudeStream, ""), "\n"))),
	}
	usage, apiErr := ClaudeStreamHandler(c, resp, info)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	return recorder.Body.String(), usage
}

func TestAHideUpstreamClaudeChannelAnswersAMessageAsItsOwn(t *testing.T) {
	recorder, _ := relayResellerClaudeMessage(t, `{"hide_upstream":true}`)

	assert.Equal(t, hiddenClaudeMessage, recorder.Body.String())
	assert.Equal(t, http.Header{
		"Content-Type":      {"application/json"},
		"Content-Length":    {strconv.Itoa(len(hiddenClaudeMessage))},
		common.RequestIdKey: {hideUpstreamRequestID},
	}, recorder.Header())
}

func TestAClaudeChannelWithoutHideUpstreamPassesTheMessageThroughUntouched(t *testing.T) {
	recorder, _ := relayResellerClaudeMessage(t, `{}`)

	assert.Equal(t, resellerClaudeMessage, recorder.Body.String())
	assert.Equal(t, http.Header{
		"Content-Type":         {"application/json"},
		"Content-Length":       {strconv.Itoa(len(resellerClaudeMessage))},
		"X-Openrouter-Release": {"2026.11.26-1"},
		"X-Generation-Id":      {"gen-1795741100-QwErTy"},
		"Cf-Ray":               {"9a1b2c3d4e5f6a7b-SJC"},
		common.RequestIdKey:    {hideUpstreamRequestID},
	}, recorder.Header())
}

// claudeEventsAsWritten is how ClaudeChunkData frames upstream events for the
// client: the event name, the data, and the blank lines it has always sent.
func claudeEventsAsWritten(events []string) string {
	var written strings.Builder
	for _, event := range events {
		data := strings.TrimPrefix(event, "data: ")
		eventType := strings.SplitN(strings.TrimPrefix(data, `{"type":"`), `"`, 2)[0]
		written.WriteString("event: " + eventType + "\ndata: " + data + "\n\n\n")
	}
	return written.String()
}

func TestAHideUpstreamClaudeChannelStreamsAMessageAsItsOwn(t *testing.T) {
	stream, _ := relayResellerClaudeStream(t, `{"hide_upstream":true}`)

	// Only message_start names the upstream; the other five events are the
	// upstream's bytes.
	want := append([]string{
		`data: {"type":"message_start","message":{"id":"msg_20261126093015123456789a1b2c3d4","type":"message","role":"assistant","model":"claude-fable-5.1","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":412,"cache_creation_input_tokens":0,"cache_read_input_tokens":300,"output_tokens":1}}}`,
	}, resellerClaudeStream[1:]...)
	assert.Equal(t, claudeEventsAsWritten(want), stream)
}

func TestAClaudeChannelWithoutHideUpstreamStreamsTheUpstreamsEventsUntouched(t *testing.T) {
	stream, _ := relayResellerClaudeStream(t, `{}`)

	assert.Equal(t, claudeEventsAsWritten(resellerClaudeStream), stream)
}

// What is billed is read from the upstream's events, so it cannot depend on
// what the client is shown. The figures are the upstream's own.
func TestHideUpstreamDoesNotChangeTheClaudeUsageThatIsBilled(t *testing.T) {
	for name, relay := range map[string]func(*testing.T, string) *dto.Usage{
		"whole answer": func(t *testing.T, channelSettings string) *dto.Usage {
			_, usage := relayResellerClaudeMessage(t, channelSettings)
			return usage
		},
		"stream": func(t *testing.T, channelSettings string) *dto.Usage {
			_, usage := relayResellerClaudeStream(t, channelSettings)
			return usage
		},
	} {
		t.Run(name, func(t *testing.T) {
			shown := relay(t, `{}`)
			hidden := relay(t, `{"hide_upstream":true}`)

			assert.Equal(t, shown, hidden)
			assert.Equal(t, 412, hidden.PromptTokens)
			assert.Equal(t, 57, hidden.CompletionTokens)
			assert.Equal(t, 300, hidden.PromptTokensDetails.CachedTokens)
			require.NotNil(t, hidden.BillingUsage)
			require.NotNil(t, hidden.BillingUsage.ClaudeUsage)
			assert.Equal(t, 57, hidden.BillingUsage.ClaudeUsage.OutputTokens)
		})
	}
}
