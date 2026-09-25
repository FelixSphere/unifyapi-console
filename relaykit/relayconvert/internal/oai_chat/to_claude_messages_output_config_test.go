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

// A caller asking for json_schema over the OpenAI-compatible endpoint gets HTTP
// 200 and free-form markdown prose from Anthropic models unless the schema is
// carried across. Observed in production 2026-09-25 on claude-sonnet-5,
// claude-opus-4-8 and claude-fable-5, all with finish_reason "stop".
//
// The field is output_config, not output_format. An earlier version of this
// test pinned output_format, which Anthropic has deprecated: measured the same
// day across five models and both upstream paths, output_format is accepted and
// silently ignored (prose), while output_config.format returns JSON matching the
// schema. Pinning output_format meant pinning a field that does nothing.
func TestOpenAIChatToClaudeCarriesJSONSchemaAsOutputConfig(t *testing.T) {
	claudeRequest, err := OpenAIChatRequestToClaudeMessages(context.Background(), &convmeta.Values{}, schemaRequest(t))
	require.NoError(t, err)
	require.NotNil(t, claudeRequest)
	require.NotEmpty(t, claudeRequest.OutputConfig, "output_config must carry the caller's schema")
	assert.Empty(t, claudeRequest.OutputFormat, "output_format is deprecated upstream and must not be sent")

	var got map[string]any
	require.NoError(t, json.Unmarshal(claudeRequest.OutputConfig, &got))

	format, ok := got["format"].(map[string]any)
	require.True(t, ok, "output_config must nest the schema under \"format\", got %T", got["format"])
	assert.Equal(t, "json_schema", format["type"])

	schema, ok := format["schema"].(map[string]any)
	require.True(t, ok, "schema must survive as an object, got %T", format["schema"])
	assert.Equal(t, "object", schema["type"])
	assert.Contains(t, schema, "properties")
	assert.Equal(t, []any{"city", "c"}, schema["required"])

	// The schema is what makes the upstream answer in JSON, so it has to arrive
	// whole rather than merely present. Pinning "the field is set" is what let
	// the deprecated-field version pass while production returned prose.
	properties, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, properties, "city")
	assert.Contains(t, properties, "c")
	assert.Equal(t, false, schema["additionalProperties"])
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
			assert.Empty(t, claudeRequest.OutputConfig)
			assert.Empty(t, claudeRequest.OutputFormat)
		})
	}
}
