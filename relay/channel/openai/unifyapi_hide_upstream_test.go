package openai

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
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// UNIFYAPI-FORK: what a client of an OpenRouter channel receives, with and
// without the channel's hide_upstream setting.
//
// The bodies and headers are what production returned on 2026-11-26 for a
// request that asked for glm-5.3 on a channel that maps it to z-ai/glm-5.3,
// with the ids shortened. The setting is read from the channel's settings JSON
// the way the distributor reads it, so these tests say what an operator gets
// from writing {"hide_upstream":true} there.

const (
	hideUpstreamRequestID      = "20261126093015123456789a1b2c3d4"
	hideUpstreamRequestedModel = "glm-5.3"

	openRouterChatCompletion = `{"id":"gen-1795741019-AbCdEfGhIjKlMnOpQrSt","provider":"CoreWeave","model":"z-ai/glm-5.3","object":"chat.completion","created":1795741019,"choices":[{"logprobs":null,"finish_reason":"tool_calls","native_finish_reason":"tool_calls","index":0,"message":{"role":"assistant","content":"Let me check.","refusal":null,"reasoning":"The user wants the weather in Paris.","tool_calls":[{"id":"call_9f2c","index":0,"type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}],"reasoning_details":[{"type":"reasoning.text","text":"The user wants the weather in Paris.","format":"unknown","index":0}]}}],"system_fingerprint":null,"usage":{"prompt_tokens":21,"completion_tokens":34,"total_tokens":55,"cost":0.0000413,"is_byok":false,"prompt_tokens_details":{"cached_tokens":8,"audio_tokens":0},"cost_details":{"upstream_inference_cost":null,"upstream_inference_prompt_cost":0.0000105,"upstream_inference_completions_cost":0.0000308},"completion_tokens_details":{"reasoning_tokens":12,"image_tokens":0}}}`

	// The same answer as the client of a hide_upstream channel receives it:
	// the requested model, our id, no provider, no native_finish_reason, no
	// cost, reasoning under the name the vendor's own API uses. Content, tool
	// call, finish_reason and every token count are as the upstream sent them.
	hiddenChatCompletion = `{"id":"chatcmpl-20261126093015123456789a1b2c3d4","model":"glm-5.3","object":"chat.completion","created":1795741019,"choices":[{"logprobs":null,"finish_reason":"tool_calls","index":0,"message":{"role":"assistant","content":"Let me check.","refusal":null,"tool_calls":[{"id":"call_9f2c","index":0,"type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}],"reasoning_content":"The user wants the weather in Paris."}}],"system_fingerprint":null,"usage":{"prompt_tokens":21,"completion_tokens":34,"total_tokens":55,"prompt_tokens_details":{"cached_tokens":8,"audio_tokens":0},"completion_tokens_details":{"reasoning_tokens":12,"image_tokens":0}}}`
)

var openRouterChatCompletionStream = []string{
	`data: {"id":"gen-1795741020-ZyXwVu","provider":"CoreWeave","model":"z-ai/glm-5.3","object":"chat.completion.chunk","created":1795741020,"choices":[{"index":0,"delta":{"role":"assistant","content":"","reasoning":"Thinking.","reasoning_details":[{"type":"reasoning.text","text":"Thinking.","format":"unknown","index":0}]},"finish_reason":null,"native_finish_reason":null,"logprobs":null}]}`,
	`data: {"id":"gen-1795741020-ZyXwVu","provider":"CoreWeave","model":"z-ai/glm-5.3","object":"chat.completion.chunk","created":1795741020,"choices":[{"index":0,"delta":{"role":"assistant","content":"Hello","reasoning":null,"reasoning_details":[]},"finish_reason":null,"native_finish_reason":null,"logprobs":null}]}`,
	`data: {"id":"gen-1795741020-ZyXwVu","provider":"CoreWeave","model":"z-ai/glm-5.3","object":"chat.completion.chunk","created":1795741020,"choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":"stop","native_finish_reason":"stop","logprobs":null}]}`,
	`data: {"id":"gen-1795741020-ZyXwVu","provider":"CoreWeave","model":"z-ai/glm-5.3","object":"chat.completion.chunk","created":1795741020,"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18,"cost":0.0000094,"is_byok":false,"prompt_tokens_details":{"cached_tokens":0},"cost_details":{"upstream_inference_cost":null},"completion_tokens_details":{"reasoning_tokens":3}}}`,
	`data: [DONE]`,
}

var openRouterResponseHeaders = http.Header{
	"Content-Type":                  {"application/json"},
	"X-Openrouter-Release":          {"2026.11.26-1"},
	"X-Generation-Id":               {"gen-1795741019-AbCdEfGhIjKlMnOpQrSt"},
	"Cf-Ray":                        {"9a1b2c3d4e5f6a7b-SJC"},
	"Set-Cookie":                    {"__cf_bm=abc123; path=/; HttpOnly"},
	"Alt-Svc":                       {`h3=":443"; ma=86400`},
	"Permissions-Policy":            {"payment=(self)"},
	"Referrer-Policy":               {"no-referrer, strict-origin-when-cross-origin"},
	"Access-Control-Expose-Headers": {"X-Generation-Id"},
}

func hideUpstreamTestContext(t *testing.T, path string, channelSettings string) (*gin.Context, *httptest.ResponseRecorder, dto.ChannelOtherSettings) {
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
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	// What the request-id middleware and the distributor leave in the context.
	c.Set(common.RequestIdKey, hideUpstreamRequestID)
	c.Header(common.RequestIdKey, hideUpstreamRequestID)
	c.Set(string(constant.ContextKeyOriginalModel), hideUpstreamRequestedModel)
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, settings)
	return c, recorder, settings
}

func hideUpstreamTestRelayInfo(format types.RelayFormat, stream bool, settings dto.ChannelOtherSettings) *relaycommon.RelayInfo {
	info := &relaycommon.RelayInfo{
		OriginModelName: hideUpstreamRequestedModel,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:          constant.ChannelTypeOpenRouter,
			UpstreamModelName:    "z-ai/glm-5.3",
			IsModelMapped:        true,
			ChannelOtherSettings: settings,
		},
		RelayMode:          relayconstant.RelayModeChatCompletions,
		RelayFormat:        format,
		IsStream:           stream,
		ShouldIncludeUsage: true,
		DisablePing:        true,
	}
	info.SetEstimatePromptTokens(21)
	if format == types.RelayFormatClaude {
		info.EnsureClaudeConvertInfo()
	}
	return info
}

func relayOpenRouterChatCompletion(t *testing.T, format types.RelayFormat, channelSettings string) (*httptest.ResponseRecorder, *dto.Usage) {
	t.Helper()
	c, recorder, settings := hideUpstreamTestContext(t, "/v1/chat/completions", channelSettings)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     openRouterResponseHeaders.Clone(),
		Body:       io.NopCloser(strings.NewReader(openRouterChatCompletion)),
	}
	usage, apiErr := OpenaiHandler(c, hideUpstreamTestRelayInfo(format, false, settings), resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	return recorder, usage
}

func relayOpenRouterChatCompletionStream(t *testing.T, format types.RelayFormat, channelSettings string) (string, *dto.Usage) {
	t.Helper()
	c, recorder, settings := hideUpstreamTestContext(t, "/v1/chat/completions", channelSettings)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}, "X-Generation-Id": {"gen-1795741020-ZyXwVu"}},
		Body:       io.NopCloser(strings.NewReader(strings.Join(append(openRouterChatCompletionStream, ""), "\n"))),
	}
	usage, apiErr := OaiStreamHandler(c, hideUpstreamTestRelayInfo(format, true, settings), resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	return recorder.Body.String(), usage
}

func TestAHideUpstreamChannelAnswersAChatCompletionAsItsOwn(t *testing.T) {
	recorder, _ := relayOpenRouterChatCompletion(t, types.RelayFormatOpenAI, `{"hide_upstream":true}`)

	assert.Equal(t, hiddenChatCompletion, recorder.Body.String())
	for _, leak := range []string{"z-ai", "CoreWeave", "gen-", "native_finish_reason", "cost", "is_byok", "reasoning_details"} {
		assert.NotContains(t, recorder.Body.String(), leak)
	}
}

func TestAHideUpstreamChannelPassesOnNoneOfTheUpstreamsHeaders(t *testing.T) {
	recorder, _ := relayOpenRouterChatCompletion(t, types.RelayFormatOpenAI, `{"hide_upstream":true}`)

	assert.Equal(t, http.Header{
		"Content-Type":      {"application/json"},
		"Content-Length":    {strconv.Itoa(len(hiddenChatCompletion))},
		common.RequestIdKey: {hideUpstreamRequestID},
	}, recorder.Header())
}

// The no-regression line: a channel without the setting behaves as it did
// before the setting existed. The body is the upstream's bytes and every
// upstream header is copied.
func TestAChannelWithoutHideUpstreamPassesTheAnswerThroughUntouched(t *testing.T) {
	for name, channelSettings := range map[string]string{
		"setting absent": `{}`,
		"setting false":  `{"hide_upstream":false}`,
	} {
		t.Run(name, func(t *testing.T) {
			recorder, _ := relayOpenRouterChatCompletion(t, types.RelayFormatOpenAI, channelSettings)

			assert.Equal(t, openRouterChatCompletion, recorder.Body.String())

			want := openRouterResponseHeaders.Clone()
			want.Set("Content-Length", strconv.Itoa(len(openRouterChatCompletion)))
			want.Set(common.RequestIdKey, hideUpstreamRequestID)
			assert.Equal(t, want, recorder.Header())
		})
	}
}

// What is billed is read from the upstream's answer, so it cannot depend on
// what the client is shown. The figures are the upstream's own.
func TestHideUpstreamDoesNotChangeTheUsageThatIsBilled(t *testing.T) {
	for name, relay := range map[string]func(*testing.T, string) *dto.Usage{
		"whole answer": func(t *testing.T, channelSettings string) *dto.Usage {
			_, usage := relayOpenRouterChatCompletion(t, types.RelayFormatOpenAI, channelSettings)
			assert.Equal(t, 21, usage.PromptTokens)
			assert.Equal(t, 34, usage.CompletionTokens)
			assert.Equal(t, 55, usage.TotalTokens)
			assert.Equal(t, 8, usage.PromptTokensDetails.CachedTokens)
			assert.Equal(t, 12, usage.CompletionTokenDetails.ReasoningTokens)
			return usage
		},
		"stream": func(t *testing.T, channelSettings string) *dto.Usage {
			_, usage := relayOpenRouterChatCompletionStream(t, types.RelayFormatOpenAI, channelSettings)
			assert.Equal(t, 11, usage.PromptTokens)
			assert.Equal(t, 7, usage.CompletionTokens)
			assert.Equal(t, 18, usage.TotalTokens)
			assert.Equal(t, 3, usage.CompletionTokenDetails.ReasoningTokens)
			return usage
		},
	} {
		t.Run(name, func(t *testing.T) {
			shown := relay(t, `{}`)
			hidden := relay(t, `{"hide_upstream":true}`)
			assert.Equal(t, shown, hidden)
		})
	}
}

func TestAHideUpstreamChannelStreamsAChatCompletionAsItsOwn(t *testing.T) {
	stream, _ := relayOpenRouterChatCompletionStream(t, types.RelayFormatOpenAI, `{"hide_upstream":true}`)

	assert.Equal(t, strings.Join([]string{
		`data: {"id":"chatcmpl-20261126093015123456789a1b2c3d4","model":"glm-5.3","object":"chat.completion.chunk","created":1795741020,"choices":[{"index":0,"delta":{"role":"assistant","content":"","reasoning_content":"Thinking."},"finish_reason":null,"logprobs":null}]}`,
		`data: {"id":"chatcmpl-20261126093015123456789a1b2c3d4","model":"glm-5.3","object":"chat.completion.chunk","created":1795741020,"choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null,"logprobs":null}]}`,
		`data: {"id":"chatcmpl-20261126093015123456789a1b2c3d4","model":"glm-5.3","object":"chat.completion.chunk","created":1795741020,"choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":"stop","logprobs":null}]}`,
		`data: {"id":"chatcmpl-20261126093015123456789a1b2c3d4","model":"glm-5.3","object":"chat.completion.chunk","created":1795741020,"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":3}}}`,
		`data: [DONE]`,
		``,
	}, "\n\n"), stream)
}

func TestAChannelWithoutHideUpstreamStreamsTheUpstreamsChunksUntouched(t *testing.T) {
	stream, _ := relayOpenRouterChatCompletionStream(t, types.RelayFormatOpenAI, `{}`)

	assert.Equal(t, strings.Join(append(openRouterChatCompletionStream, ""), "\n\n"), stream)
}

// An OpenAI-type channel also serves /v1/messages, through the converter.
func TestAHideUpstreamChannelAnswersInClaudeFormatAsItsOwn(t *testing.T) {
	recorder, usage := relayOpenRouterChatCompletion(t, types.RelayFormatClaude, `{"hide_upstream":true}`)

	message := gjson.ParseBytes(recorder.Body.Bytes())
	assert.Equal(t, "message", message.Get("type").String())
	assert.Equal(t, hideUpstreamRequestedModel, message.Get("model").String())
	assert.Equal(t, "msg_"+hideUpstreamRequestID, message.Get("id").String())
	assert.False(t, message.Get("provider").Exists())
	assert.Equal(t, "tool_use", message.Get("stop_reason").String())
	assert.Equal(t, "get_weather", message.Get(`content.#(type=="tool_use").name`).String())
	assert.Equal(t, int64(usage.CompletionTokens), message.Get("usage.output_tokens").Int())
	assert.Equal(t, int64(21), message.Get("usage.input_tokens").Int())
	// The converter serialises the usage it settles from next to the Claude
	// one, and that copy carries the reseller's cost.
	for _, leak := range []string{"z-ai", "CoreWeave", "gen-", "cost", "billing_usage", "oai_chat"} {
		assert.NotContains(t, recorder.Body.String(), leak)
	}

	shown, _ := relayOpenRouterChatCompletion(t, types.RelayFormatClaude, `{}`)
	assert.Equal(t, "z-ai/glm-5.3", gjson.GetBytes(shown.Body.Bytes(), "model").String(),
		"without the setting the converted answer names the upstream's model, as before")
}

func TestAHideUpstreamChannelStreamsInClaudeFormatAsItsOwn(t *testing.T) {
	stream, _ := relayOpenRouterChatCompletionStream(t, types.RelayFormatClaude, `{"hide_upstream":true}`)

	var messageStart gjson.Result
	for _, line := range strings.Split(stream, "\n") {
		data := strings.TrimPrefix(line, "data: ")
		if gjson.Get(data, "type").String() == "message_start" {
			messageStart = gjson.Parse(data)
		}
	}
	require.True(t, messageStart.Exists(), "stream: %s", stream)
	assert.Equal(t, hideUpstreamRequestedModel, messageStart.Get("message.model").String())
	assert.Equal(t, "msg_"+hideUpstreamRequestID, messageStart.Get("message.id").String())
	assert.Contains(t, stream, `"text":"Hello"`)
	assert.Contains(t, stream, `"thinking":"Thinking."`)
	assert.Contains(t, stream, `"output_tokens":7`)
	for _, leak := range []string{"z-ai", "CoreWeave", "gen-", "cost", "billing_usage", "oai_chat"} {
		assert.NotContains(t, stream, leak)
	}

	shown, _ := relayOpenRouterChatCompletionStream(t, types.RelayFormatClaude, `{}`)
	assert.Contains(t, shown, `"model":"z-ai/glm-5.3"`,
		"without the setting the converted stream names the upstream's model, as before")
}

// The Responses API answers are written out by hand in the shape a reseller
// uses, not recorded: the reseller's id and model, a provider key and a cost.
const (
	resellerResponse = `{"id":"gen-1795741400-RsPn","object":"response","created_at":1795741400,"model":"z-ai/glm-5.3","provider":"CoreWeave","status":"completed","output":[{"id":"msg_tmp_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Hello","annotations":[]}]}],"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18,"cost":0.0000094,"is_byok":false,"cost_details":{"upstream_inference_cost":null},"input_tokens_details":{"cached_tokens":4},"output_tokens_details":{"reasoning_tokens":3}}}`

	hiddenResponse = `{"id":"resp_20261126093015123456789a1b2c3d4","object":"response","created_at":1795741400,"model":"glm-5.3","status":"completed","output":[{"id":"msg_tmp_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Hello","annotations":[]}]}],"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18,"input_tokens_details":{"cached_tokens":4},"output_tokens_details":{"reasoning_tokens":3}}}`
)

var resellerResponseStream = []string{
	`data: {"type":"response.created","sequence_number":0,"response":{"id":"gen-1795741400-RsPn","object":"response","created_at":1795741400,"model":"z-ai/glm-5.3","provider":"CoreWeave","status":"in_progress","output":[]}}`,
	`data: {"type":"response.output_text.delta","sequence_number":1,"item_id":"msg_tmp_1","output_index":0,"content_index":0,"delta":"Hello"}`,
	`data: {"type":"response.completed","sequence_number":2,"response":` + resellerResponse + `}`,
}

func relayResellerResponse(t *testing.T, stream bool, channelSettings string) (string, *dto.Usage) {
	t.Helper()
	c, recorder, settings := hideUpstreamTestContext(t, "/v1/responses", channelSettings)
	info := hideUpstreamTestRelayInfo(types.RelayFormatOpenAIResponses, stream, settings)
	info.RelayMode = relayconstant.RelayModeResponses

	body, handler := resellerResponse, OaiResponsesHandler
	if stream {
		body, handler = strings.Join(append(resellerResponseStream, ""), "\n"), OaiResponsesStreamHandler
	}
	usage, apiErr := handler(c, info, &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	})
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	return recorder.Body.String(), usage
}

// responsesEventsAsWritten is how ResponseChunkData frames upstream events for
// the client.
func responsesEventsAsWritten(events []string) string {
	var written strings.Builder
	for _, event := range events {
		data := strings.TrimPrefix(event, "data: ")
		written.WriteString("event: " + gjson.Get(data, "type").String() + "\ndata: " + data + "\n\n")
	}
	return written.String()
}

func TestAHideUpstreamChannelAnswersTheResponsesAPIAsItsOwn(t *testing.T) {
	body, hiddenUsage := relayResellerResponse(t, false, `{"hide_upstream":true}`)
	assert.Equal(t, hiddenResponse, body)

	stream, hiddenStreamUsage := relayResellerResponse(t, true, `{"hide_upstream":true}`)
	assert.Equal(t, responsesEventsAsWritten([]string{
		`data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_20261126093015123456789a1b2c3d4","object":"response","created_at":1795741400,"model":"glm-5.3","status":"in_progress","output":[]}}`,
		resellerResponseStream[1],
		`data: {"type":"response.completed","sequence_number":2,"response":` + hiddenResponse + `}`,
	}), stream)

	shownBody, shownUsage := relayResellerResponse(t, false, `{}`)
	assert.Equal(t, resellerResponse, shownBody, "without the setting the answer is the upstream's bytes")
	shownStream, shownStreamUsage := relayResellerResponse(t, true, `{}`)
	assert.Equal(t, responsesEventsAsWritten(resellerResponseStream), shownStream, "without the setting the events are the upstream's bytes")

	assert.Equal(t, shownUsage, hiddenUsage)
	assert.Equal(t, shownStreamUsage, hiddenStreamUsage)
	assert.Equal(t, 11, hiddenUsage.PromptTokens)
	assert.Equal(t, 7, hiddenUsage.CompletionTokens)
	assert.Equal(t, 4, hiddenUsage.PromptTokensDetails.CachedTokens)
	assert.Equal(t, 11, hiddenStreamUsage.PromptTokens)
	assert.Equal(t, 7, hiddenStreamUsage.CompletionTokens)
}
