package oaichat

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The schema is what makes the upstream answer in JSON, so it has to arrive
// whole rather than merely present. Pinning "the field is set" is what let the
// deprecated output_format version pass while production returned prose.
func TestOpenAIChatToClaudeDeliversTheWholeSchema(t *testing.T) {
	claudeRequest, err := OpenAIChatRequestToClaudeMessages(context.Background(), &convmeta.Values{}, schemaRequest(t))
	require.NoError(t, err)
	require.NotNil(t, claudeRequest)

	schema, ok := outputConfigFormat(t, claudeRequest.OutputConfig)["schema"].(map[string]any)
	require.True(t, ok, "output_config.format.schema must be an object")

	properties, ok := schema["properties"].(map[string]any)
	require.True(t, ok, "schema.properties must be an object, got %T", schema["properties"])
	assert.Contains(t, properties, "city")
	assert.Contains(t, properties, "c")
	assert.Equal(t, false, schema["additionalProperties"])
}

// A request with nothing to map must go upstream without an output_config at
// all, not with an empty or partial one: byte-identical to before the mapping
// existed.
func TestOpenAIChatToClaudeSendsNoOutputConfigWithoutASchema(t *testing.T) {
	for name, responseFormat := range map[string]*dto.ResponseFormat{
		"nil":         nil,
		"json_object": {Type: "json_object"},
		"text":        {Type: "text"},
	} {
		t.Run(name, func(t *testing.T) {
			request := schemaRequest(t)
			request.ResponseFormat = responseFormat

			claudeRequest, err := OpenAIChatRequestToClaudeMessages(context.Background(), &convmeta.Values{}, request)
			require.NoError(t, err)
			require.NotNil(t, claudeRequest)
			assert.Empty(t, claudeRequest.OutputConfig)
		})
	}
}
