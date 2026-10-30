package common

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A video-generation model must never be advertised as a chat model.
//
// GET /api/pricing published `supported_endpoint_types: ["openai"]` for
// MiniMax-H3 and seedance-2.5. Both are video models, so every caller that
// believed the catalogue sent a chat/completions request and got a 400 back --
// from OpenRouter for one and from the supplier's own instance for the other.
// Neither model was broken: both answer on POST /v1/videos.
//
// Pinned against the old behaviour: with the IsVideoGenerationModel branch
// removed, every case below fails reporting []EndpointType{"openai"}.
func TestVideoModelsDoNotAdvertiseChat(t *testing.T) {
	video := []string{
		"MiniMax-H3",
		"MiniMax-H3-Max",
		"minimax/hailuo-3",
		"seedance-2.5",
		"doubao-seedance-2-5-260628",
		"dreamina-seedance-2-5-260628",
		"wan3.0-video",
		"wan3.0-video-prime",
		"happyhorse-1.1-t2v",
		"happyhorse-1.1-i2v",
		"happyhorse-1.1-r2v",
	}
	// The channel types these models are actually on today: 20 is the
	// OpenRouter channel serving MiniMax-H3, 1 the OpenAI-format channel
	// serving seedance-2.5, 35 the native MiniMax channels.
	for _, channelType := range []int{1, 20, 35, constant.ChannelTypeAli} {
		for _, model := range video {
			got := GetEndpointTypesByChannelType(channelType, model)
			require.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAIVideo}, got,
				"model %q on channel type %d must advertise video and nothing else", model, channelType)
		}
	}
}

// The classifier must not reach models that are not video. Checked against the
// 54 models live on a channel on 2026-09-25: only MiniMax-H3 and seedance-2.5
// matched.
func TestVideoClassifierLeavesTextAndImageModelsAlone(t *testing.T) {
	notVideo := []string{
		"gpt-4o", "gpt-4o-mini", "gpt-5", "gpt-5-mini", "gpt-5.4", "gpt-6-astra",
		"claude-opus-5", "claude-sonnet-5", "claude-fable-5", "claude-opus-4-8",
		"gemini-3.8-flash", "gemini-flash-latest", "gemini-2.5-pro",
		"deepseek-v4-pro", "deepseek-flash", "glm-5.3", "kimi-k3",
		"qwen3.8-max", "qwen3.5-flash", "jev-1.13",
		"dall-e-3", "gpt-image-1", "flux-pro",
	}
	for _, model := range notVideo {
		assert.False(t, IsVideoGenerationModel(model), "%q must not be classified as video", model)
	}
}

// A text model on one of these channel types keeps exactly the endpoints it had
// before this change, so the switch above is not disturbed.
func TestNonVideoEndpointsAreUnchanged(t *testing.T) {
	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI},
		GetEndpointTypesByChannelType(constant.ChannelTypeOpenRouter, "claude-sonnet-5"))
	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeAnthropic, constant.EndpointTypeOpenAI},
		GetEndpointTypesByChannelType(constant.ChannelTypeAnthropic, "claude-opus-5"))
	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeGemini, constant.EndpointTypeOpenAI},
		GetEndpointTypesByChannelType(constant.ChannelTypeGemini, "gemini-3.8-flash"))
	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeImageGeneration, constant.EndpointTypeOpenAI},
		GetEndpointTypesByChannelType(constant.ChannelTypeOpenAI, "dall-e-3"))
}
