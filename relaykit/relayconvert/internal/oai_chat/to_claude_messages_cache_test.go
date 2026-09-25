package oaichat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	relaymedia "github.com/QuantumNous/new-api/relaykit/relayconvert/internal/media"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The resolver below answers without fetching, so any URL will do.
const cacheTestPNG = "https://example.invalid/pixel.png"

// The body is decoded from JSON exactly as the relay receives it, so content
// parts arrive as map[string]any -- the shape that lost the marker.
func cacheControlRequest(t *testing.T) dto.GeneralOpenAIRequest {
	t.Helper()
	relaymedia.SetMediaResolver(relaymedia.MediaResolver{
		GetBase64Data: func(c context.Context, source types.FileSource, reason ...string) (string, string, error) {
			return "aGVsbG8=", "image/png", nil
		},
	})
	t.Cleanup(func() { relaymedia.SetMediaResolver(relaymedia.MediaResolver{}) })
	body := `{
		"model": "claude-sonnet-5",
		"max_tokens": 256,
		"messages": [
			{"role": "system", "content": [
				{"type": "text", "text": "long stable policy handbook", "cache_control": {"type": "ephemeral"}}
			]},
			{"role": "user", "content": [
				{"type": "text", "text": "reference document", "cache_control": {"type": "ephemeral", "ttl": "1h"}},
				{"type": "image_url", "image_url": {"url": "` + cacheTestPNG + `"}, "cache_control": {"type": "ephemeral"}},
				{"type": "text", "text": "the actual question"}
			]}
		]
	}`
	var request dto.GeneralOpenAIRequest
	require.NoError(t, json.Unmarshal([]byte(body), &request))
	return request
}

// Measured in production 2026-09-25: nine Claude models, the same ~9k-token
// prefix sent three times with cache_control over /v1/chat/completions, cached
// 0 tokens on every call, while OpenRouter cached 99% of calls 2 and 3 and our
// own /v1/messages path cached 9 of 10. The marker was dropped twice: when the
// content part was parsed, and again when it was copied into the Claude block.
func TestOpenAIChatToClaudeKeepsCacheControl(t *testing.T) {
	claudeRequest, err := OpenAIChatRequestToClaudeMessages(context.Background(), &convmeta.Values{}, cacheControlRequest(t))
	require.NoError(t, err)
	require.NotNil(t, claudeRequest)

	system, ok := claudeRequest.System.([]dto.ClaudeMediaMessage)
	require.True(t, ok, "system must be content blocks, got %T", claudeRequest.System)
	require.Len(t, system, 1)
	assert.JSONEq(t, `{"type":"ephemeral"}`, string(system[0].CacheControl), "system block lost its cache marker")

	require.Len(t, claudeRequest.Messages, 1)
	blocks, ok := claudeRequest.Messages[0].Content.([]dto.ClaudeMediaMessage)
	require.True(t, ok, "user content must be blocks, got %T", claudeRequest.Messages[0].Content)
	require.Len(t, blocks, 3)

	assert.Equal(t, "text", blocks[0].Type)
	assert.JSONEq(t, `{"type":"ephemeral","ttl":"1h"}`, string(blocks[0].CacheControl), "ttl must survive untouched")
	assert.Equal(t, "image", blocks[1].Type)
	assert.JSONEq(t, `{"type":"ephemeral"}`, string(blocks[1].CacheControl), "image block lost its cache marker")
	assert.Equal(t, "text", blocks[2].Type)
	assert.Empty(t, blocks[2].CacheControl, "an unmarked block must not gain a marker")
}

// Anthropic allows at most four cache breakpoints; inventing one would turn a
// valid request into a 400. The wire body must carry exactly the ones sent.
func TestOpenAIChatToClaudeAddsNoCacheControlOfItsOwn(t *testing.T) {
	claudeRequest, err := OpenAIChatRequestToClaudeMessages(context.Background(), &convmeta.Values{}, cacheControlRequest(t))
	require.NoError(t, err)
	wire, err := json.Marshal(claudeRequest)
	require.NoError(t, err)
	assert.Equal(t, 3, strings.Count(string(wire), `"cache_control"`))

	plain := dto.GeneralOpenAIRequest{
		Model:     "claude-sonnet-5",
		MaxTokens: kitutil.GetPointer(uint(256)),
		Messages:  []dto.Message{{Role: "system", Content: "be brief"}, {Role: "user", Content: "hi"}},
	}
	claudeRequest, err = OpenAIChatRequestToClaudeMessages(context.Background(), &convmeta.Values{}, plain)
	require.NoError(t, err)
	wire, err = json.Marshal(claudeRequest)
	require.NoError(t, err)
	assert.NotContains(t, string(wire), `"cache_control"`)
}
