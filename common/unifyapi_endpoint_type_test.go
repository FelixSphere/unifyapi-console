package common

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
)

// A TypeSafe channel serves POST /v1/systemone and nothing else -- it has no
// chat, embedding, rerank or audio endpoint. Until this case existed the switch
// fell through to the OpenAI default, so GET /api/pricing advertised
// `supported_endpoint_types: ["openai"]` for jev-1.13 and every customer who
// believed the catalogue called chat/completions and got a 500 back from the
// adaptor. Verified against production on 2026-09-25: the model works on
// /v1/systemone and had never once served a real request over chat.
func TestTypeSafeChannelsAdvertiseSystemOneAndNotChat(t *testing.T) {
	got := GetEndpointTypesByChannelType(constant.ChannelTypeTypeSafe, "jev-1.13")

	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeSystemOne}, got)
	assert.NotContains(t, got, constant.EndpointTypeOpenAI,
		"advertising the OpenAI endpoint sends customers to chat/completions, which a TypeSafe channel cannot serve")
}

// The fix is one case in a shared switch, so the neighbouring channel types are
// pinned here: a mistake that widened or narrowed the wrong branch would change
// what the public catalogue promises for every model on that channel type.
func TestEndpointTypesForNeighbouringChannelTypesAreUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name        string
		channelType int
		model       string
		want        []constant.EndpointType
	}{
		{
			name:        "Sora stays video only",
			channelType: constant.ChannelTypeSora,
			model:       "sora-2",
			want:        []constant.EndpointType{constant.EndpointTypeOpenAIVideo},
		},
		{
			name:        "OpenRouter stays OpenAI only",
			channelType: constant.ChannelTypeOpenRouter,
			model:       "gpt-5",
			want:        []constant.EndpointType{constant.EndpointTypeOpenAI},
		},
		{
			name:        "Anthropic keeps both its native and the OpenAI endpoint",
			channelType: constant.ChannelTypeAnthropic,
			model:       "claude-sonnet-5",
			want:        []constant.EndpointType{constant.EndpointTypeAnthropic, constant.EndpointTypeOpenAI},
		},
		{
			name:        "an unlisted channel type still defaults to OpenAI",
			channelType: constant.ChannelTypeOpenAI,
			model:       "gpt-4o",
			want:        []constant.EndpointType{constant.EndpointTypeOpenAI},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, GetEndpointTypesByChannelType(tc.channelType, tc.model))
		})
	}
}
