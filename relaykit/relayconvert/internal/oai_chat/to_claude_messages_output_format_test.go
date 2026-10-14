package oaichat

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func schemaRequest(t *testing.T) dto.GeneralOpenAIRequest {
	t.Helper()
	return dto.GeneralOpenAIRequest{
		Model:     "claude-sonnet-5",
		MaxTokens: kitutil.GetPointer(uint(256)),
		Messages:  []dto.Message{{Role: "user", Content: "Tokyo weather"}},
		ResponseFormat: &dto.ResponseFormat{
			Type: "json_schema",
			JsonSchema: json.RawMessage(`{
				"name": "w",
				"strict": true,
				"schema": {
					"type": "object",
					"properties": {"city": {"type": "string"}, "c": {"type": "number"}},
					"required": ["city", "c"],
					"additionalProperties": false
				}
			}`),
		},
	}
}

// A caller asking for json_schema over the OpenAI-compatible endpoint used to
// get HTTP 200 and free-form markdown prose from Anthropic models, because the
// field was dropped here with nothing to say it had been. Observed in
// production 2026-09-25 on claude-sonnet-5, claude-opus-4-8 and
// claude-fable-5, all with finish_reason "stop".
func TestOpenAIChatToClaudeCarriesJSONSchemaAsOutputFormat(t *testing.T) {
	claudeRequest, err := OpenAIChatRequestToClaudeMessages(context.Background(), &convmeta.Values{}, schemaRequest(t))
	require.NoError(t, err)
	require.NotNil(t, claudeRequest)

	// The schema now travels at output_config.format. Anthropic deprecated the
	// top-level output_format and rejects it with 400, so asserting the old
	// location would go green while every request failed in production.
	assert.Empty(t, claudeRequest.OutputFormat, "the deprecated field must not be sent")
	require.NotEmpty(t, claudeRequest.OutputConfig, "output_config must carry the caller's schema")

	got := outputConfigFormat(t, claudeRequest.OutputConfig)
	assert.Equal(t, "json_schema", got["type"])

	schema, ok := got["schema"].(map[string]any)
	require.True(t, ok, "schema must survive as an object, got %T", got["schema"])
	assert.Equal(t, "object", schema["type"])
	assert.Contains(t, schema, "properties")
	assert.Equal(t, []any{"city", "c"}, schema["required"])
}

// "json_object" names no shape, and Anthropic's output_format has no equivalent
// of it. Sending one without a schema would turn a request that works today
// into a 400, so it is deliberately left alone.
func TestOpenAIChatToClaudeLeavesUnmappableResponseFormatsAlone(t *testing.T) {
	cases := map[string]*dto.ResponseFormat{
		"nil":                  nil,
		"json_object":          {Type: "json_object"},
		"text":                 {Type: "text"},
		"json_schema no inner": {Type: "json_schema"},
		"json_schema no schema": {
			Type:       "json_schema",
			JsonSchema: json.RawMessage(`{"name":"w","strict":true}`),
		},
		"json_schema malformed": {
			Type:       "json_schema",
			JsonSchema: json.RawMessage(`{"name":`),
		},
	}
	for name, responseFormat := range cases {
		t.Run(name, func(t *testing.T) {
			request := schemaRequest(t)
			request.ResponseFormat = responseFormat
			claudeRequest, err := OpenAIChatRequestToClaudeMessages(context.Background(), &convmeta.Values{}, request)
			require.NoError(t, err)
			require.NotNil(t, claudeRequest)
			assert.Empty(t, claudeRequest.OutputFormat)
			// Nothing unmappable may leave a format key behind either.
			if len(claudeRequest.OutputConfig) > 0 {
				cfg := map[string]any{}
				require.NoError(t, json.Unmarshal(claudeRequest.OutputConfig, &cfg))
				assert.NotContains(t, cfg, "format")
			}
		})
	}
}

// outputConfigFormat pulls output_config.format out as an object, failing the
// test rather than returning a zero value if it is missing or the wrong shape.
func outputConfigFormat(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	cfg := map[string]any{}
	require.NoError(t, json.Unmarshal(raw, &cfg))
	format, ok := cfg["format"].(map[string]any)
	require.True(t, ok, "output_config.format must be an object, got %T", cfg["format"])
	return format
}

// The schema and the reasoning effort share one output_config object, and they
// are written by different branches -- format first, effort ~20 lines later.
// While both assigned the whole object, whichever ran last silently erased the
// other, and the caller got prose back from a json_schema request with nothing
// in the response explaining it. Only Opus models take an effort suffix, so a
// test on any other model cannot catch this.
func TestOpenAIChatToClaudeKeepsSchemaAndEffortTogether(t *testing.T) {
	for _, model := range []string{"claude-opus-4-6-high", "claude-opus-4-8-low"} {
		t.Run(model, func(t *testing.T) {
			request := schemaRequest(t)
			request.Model = model

			claudeRequest, err := OpenAIChatRequestToClaudeMessages(context.Background(), &convmeta.Values{}, request)
			require.NoError(t, err)
			require.NotNil(t, claudeRequest)
			require.NotEmpty(t, claudeRequest.OutputConfig)

			cfg := map[string]any{}
			require.NoError(t, json.Unmarshal(claudeRequest.OutputConfig, &cfg))

			assert.NotEmpty(t, cfg["effort"], "effort must survive the format write")
			format, ok := cfg["format"].(map[string]any)
			require.True(t, ok, "format must survive the effort write, got %T", cfg["format"])
			assert.Equal(t, "json_schema", format["type"])
			assert.Contains(t, format, "schema")
		})
	}
}
