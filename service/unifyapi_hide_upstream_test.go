package service

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// UNIFYAPI-FORK: the rules of unifyapi_hide_upstream.go that the adaptor tests
// (relay/channel/openai and relay/channel/claude) do not reach.

const hideUpstreamRequestID = "20261126093015123456789a1b2c3d4"

func TestHideUpstreamRewritesEachShapeAnUpstreamAnswersIn(t *testing.T) {
	cases := map[string]struct {
		upstream string
		client   string
	}{
		"a Responses API event carries the model, id and cost under response": {
			upstream: `{"type":"response.completed","sequence_number":9,"response":{"id":"gen-1795741200-ReSp","object":"response","model":"deepseek/deepseek-v4-pro-0813","provider":"Fireworks","status":"completed","output":[{"id":"msg_0a1","type":"message","role":"assistant","content":[{"type":"output_text","text":"Hi"}]}],"usage":{"input_tokens":9,"output_tokens":2,"total_tokens":11,"cost":0.0000031,"is_byok":false,"cost_details":{"upstream_inference_cost":null}}}}`,
			client:   `{"type":"response.completed","sequence_number":9,"response":{"id":"resp_20261126093015123456789a1b2c3d4","object":"response","model":"deepseek-v4-pro","status":"completed","output":[{"id":"msg_0a1","type":"message","role":"assistant","content":[{"type":"output_text","text":"Hi"}]}],"usage":{"input_tokens":9,"output_tokens":2,"total_tokens":11}}}`,
		},
		"a Gemini answer names the model in modelVersion": {
			upstream: `{"candidates":[{"content":{"role":"model","parts":[{"text":"Hi"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":1,"totalTokenCount":4},"modelVersion":"deepseek-v4-pro-ga-260813","responseId":"Qx9zaL3fK"}`,
			client:   `{"candidates":[{"content":{"role":"model","parts":[{"text":"Hi"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":1,"totalTokenCount":4},"modelVersion":"deepseek-v4-pro","responseId":"20261126093015123456789a1b2c3d4"}`,
		},
		"an error frame inside a stream loses the reseller's metadata, its name and its request id": {
			upstream: `{"id":"gen-1795741300-ErRr","object":"chat.completion.chunk","provider":"Fireworks","model":"deepseek/deepseek-v4-pro-0813","error":{"code":502,"message":"OpenRouter: provider disconnected while serving deepseek/deepseek-v4-pro-0813 (request id: 2026112609upstream)","metadata":{"provider_name":"Fireworks","raw":"upstream connect error"}},"choices":[{"index":0,"delta":{"content":""},"finish_reason":"error","native_finish_reason":"error"}]}`,
			client:   `{"id":"chatcmpl-20261126093015123456789a1b2c3d4","object":"chat.completion.chunk","model":"deepseek-v4-pro","error":{"code":502,"message":"upstream: provider disconnected while serving deepseek-v4-pro"},"choices":[{"index":0,"delta":{"content":""},"finish_reason":"error"}]}`,
		},
		"a message that already has reasoning_content keeps both fields": {
			upstream: `{"id":"chatcmpl-up","object":"chat.completion","model":"deepseek-v4-pro-ga-260813","choices":[{"index":0,"message":{"role":"assistant","content":"Hi","reasoning_content":"Greet.","reasoning":"Greet."},"finish_reason":"stop"}]}`,
			client:   `{"id":"chatcmpl-20261126093015123456789a1b2c3d4","object":"chat.completion","model":"deepseek-v4-pro","choices":[{"index":0,"message":{"role":"assistant","content":"Hi","reasoning_content":"Greet.","reasoning":"Greet."},"finish_reason":"stop"}]}`,
		},
		"a payload with none of the upstream's fields is not touched": {
			upstream: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"the model said \"provider\": \"CoreWeave\""}}`,
			client:   `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"the model said \"provider\": \"CoreWeave\""}}`,
		},
		"the end-of-stream marker is not JSON": {
			upstream: `[DONE]`,
			client:   `[DONE]`,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c, _ := contextForChannelSettings(t, `{"hide_upstream":true}`)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, "https://openrouter.ai/api")
			common.SetContextKey(c, constant.ContextKeyChannelModelMapping, `{"deepseek-v4-pro":"deepseek/deepseek-v4-pro-0813"}`)

			assert.Equal(t, tc.client, HideUpstreamInBody(c, tc.upstream))

			shown, _ := contextForChannelSettings(t, `{}`)
			assert.Equal(t, tc.upstream, HideUpstreamInBody(shown, tc.upstream), "a channel without the setting")
		})
	}
}

// Handlers keep reading the upstream's bytes after they are written (cached
// token counts, tool calls), so the rewrite must work on a copy.
func TestHideUpstreamLeavesTheUpstreamsBytesAsTheyWere(t *testing.T) {
	const upstream = `{"id":"gen-1","object":"chat.completion","provider":"CoreWeave","model":"z-ai/glm-5.3","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6,"cost":0.01}}`
	c, _ := contextForChannelSettings(t, `{"hide_upstream":true}`)
	body := []byte(upstream)

	rewritten := HideUpstreamInBody(c, body)

	assert.Equal(t, upstream, string(body))
	assert.NotContains(t, string(rewritten), "CoreWeave")
}

// The model a client asked for is whatever reached the distributor first. A
// retry sets up the next channel with RelayInfo's name for the model, which
// the compaction path has by then rebuilt from the upstream's.
func TestTheRequestedModelIsTheFirstOneSeenNotARetrysRename(t *testing.T) {
	c, _ := contextForChannelSettings(t, `{"hide_upstream":true}`)

	RememberRequestedModel(c, "gpt-5.6-luna-openai-compact")
	RememberRequestedModel(c, "openai/gpt-5.6-luna-2026-10-01-openai-compact")

	assert.Equal(t,
		`{"id":"resp_20261126093015123456789a1b2c3d4","object":"response","model":"gpt-5.6-luna"}`,
		HideUpstreamInBody(c, `{"id":"resp_upstream","object":"response","model":"openai/gpt-5.6-luna-2026-10-01"}`))
}

func TestAHideUpstreamChannelDoesNotNameItsUpstreamInAnError(t *testing.T) {
	cases := map[string]struct {
		baseURL      string
		modelMapping string
		upstream     types.OpenAIError
		status       int
		message      string
	}{
		// OpenRouter wraps the provider's refusal; relaykit unwraps it and
		// attributes it ("Alibaba rejected the request: ..."). The sentence is
		// what the caller needs, the attribution is what is hidden.
		"OpenRouter relaying a provider's refusal": {
			baseURL: "https://openrouter.ai/api",
			upstream: types.OpenAIError{
				Message:  "Provider returned error",
				Code:     float64(400),
				Metadata: []byte(`{"raw":"{\"error\":{\"code\":\"invalid_parameter_error\",\"message\":\"Range of max_tokens should be [1, 8192]\"}}","provider_name":"Alibaba","is_byok":false}`),
			},
			status:  http.StatusBadRequest,
			message: "Range of max_tokens should be [1, 8192]",
		},
		"OpenRouter metadata with nothing better inside it": {
			baseURL: "https://openrouter.ai/api",
			upstream: types.OpenAIError{
				Message:  "Provider returned error",
				Code:     float64(429),
				Metadata: []byte(`{"provider_name":"CoreWeave","is_byok":false}`),
			},
			status:  http.StatusTooManyRequests,
			message: "Provider returned error",
		},
		"OpenRouter naming its own model id": {
			baseURL:      "https://openrouter.ai/api",
			modelMapping: `{"glm-5.3":"z-ai/glm-5.3"}`,
			upstream: types.OpenAIError{
				Message: "No endpoints found for z-ai/glm-5.3. Check your OpenRouter privacy settings.",
				Code:    float64(404),
			},
			status:  http.StatusNotFound,
			message: "No endpoints found for glm-5.3. Check your upstream privacy settings.",
		},
		"a new-api reseller ending its message with its own request id": {
			baseURL: "https://api.flatkey.ai",
			upstream: types.OpenAIError{
				Message: "Flatkey: the upstream group is saturated, retry later (request id: 2026112609301500flatkey)",
				Type:    "flatkey_api_error",
				Code:    "group_saturated",
			},
			status:  http.StatusServiceUnavailable,
			message: "upstream: the upstream group is saturated, retry later",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c, _ := contextForChannelSettings(t, `{"hide_upstream":true}`)
			RememberRequestedModel(c, "glm-5.3")
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, tc.baseURL)
			common.SetContextKey(c, constant.ContextKeyChannelModelMapping, tc.modelMapping)
			apiErr := types.WithOpenAIError(tc.upstream, tc.status)

			HideUpstreamInError(c, apiErr)

			assert.Equal(t, tc.status, apiErr.StatusCode)
			assert.Equal(t, tc.message, apiErr.Error())
			assert.Equal(t, tc.message, apiErr.ToOpenAIError().Message)
			assert.Equal(t, tc.message, apiErr.ToClaudeError().Message)

			written, err := common.Marshal(gin.H{"error": apiErr.ToOpenAIError()})
			require.NoError(t, err)
			for _, leak := range []string{"metadata", "provider_name", "Alibaba", "CoreWeave", "OpenRouter", "z-ai", "latkey", "request id"} {
				assert.NotContains(t, string(written), leak)
			}
		})
	}
}

func TestAChannelWithoutHideUpstreamKeepsTheErrorAsItIsToday(t *testing.T) {
	c, _ := contextForChannelSettings(t, `{}`)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, "https://openrouter.ai/api")
	upstream := types.OpenAIError{
		Message:  "Provider returned error",
		Code:     float64(400),
		Metadata: []byte(`{"raw":"{\"error\":{\"message\":\"Range of max_tokens should be [1, 8192]\"}}","provider_name":"Alibaba"}`),
	}
	apiErr := types.WithOpenAIError(upstream, http.StatusBadRequest)
	want := types.WithOpenAIError(upstream, http.StatusBadRequest)

	HideUpstreamInError(c, apiErr)

	assert.Equal(t, want.ToOpenAIError(), apiErr.ToOpenAIError())
	assert.Equal(t, "Alibaba rejected the request: Range of max_tokens should be [1, 8192]", apiErr.Error())
	assert.NotEmpty(t, apiErr.Metadata)
}

func TestAHideUpstreamChannelDoesNotNameItsUpstreamInAClaudeError(t *testing.T) {
	c, _ := contextForChannelSettings(t, `{"hide_upstream":true}`)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, "https://openrouter.ai/api")
	apiErr := types.WithClaudeError(types.ClaudeError{
		Type:    "overloaded_error",
		Message: "Overloaded. OpenRouter will retry (request id: 2026112609upstream)",
	}, http.StatusServiceUnavailable)

	HideUpstreamInError(c, apiErr)

	assert.Equal(t, http.StatusServiceUnavailable, apiErr.StatusCode)
	assert.Equal(t, types.ClaudeError{Type: "overloaded_error", Message: "Overloaded. upstream will retry"}, apiErr.ToClaudeError())
}
