package controller

import (
	"context"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	"github.com/stretchr/testify/require"
)

func TestVideoChannelTestStopsBeforeChatRequest(t *testing.T) {
	for _, channelType := range []int{constant.ChannelTypeAli, constant.ChannelTypeMiniMax,
		constant.ChannelTypeDoubaoVideo, constant.ChannelTypeKling, constant.ChannelTypeJimeng,
		constant.ChannelTypeVidu, constant.ChannelTypeGemini, constant.ChannelTypeVertexAi, constant.ChannelTypeSora} {
		adaptor := relay.GetTaskAdaptor(constant.TaskPlatform(strconv.Itoa(channelType)))
		for _, name := range adaptor.GetModelList() {
			t.Run(strconv.Itoa(channelType)+"/"+name, func(t *testing.T) {
				// No database/user setup: rejecting a video must happen before
				// loading billing state or making any upstream request.
				for _, selectedType := range []int{channelType, constant.ChannelTypeOpenAI} {
					ch := &model.Channel{Type: selectedType, Models: name}
					result := testChannel(context.Background(), ch, 0, "", "openai", true)
					require.ErrorIs(t, result.localErr, errVideoChannelTestUnsupported)
					require.Nil(t, result.newAPIError)
					require.Contains(t, result.localErr.Error(), "POST /v1/videos")
				}
			})
		}
	}
}

func TestVideoChannelTestResolvesAliasesAndConfiguredTestModel(t *testing.T) {
	mapping := `{"movie":"alias","alias":"MiniMax-H3"}`
	testModel := "movie"
	ch := &model.Channel{Type: constant.ChannelTypeMiniMax, Models: "MiniMax-M2.5", TestModel: &testModel, ModelMapping: &mapping}
	result := testChannel(context.Background(), ch, 0, "", "", false)
	require.ErrorIs(t, result.localErr, errVideoChannelTestUnsupported)
	for _, name := range []string{"MiniMax-M2.5", "qwen-plus", "gemini-2.5-pro", "gpt-4o-mini"} {
		require.NoError(t, validateSynchronousChannelTest(ch, name))
	}
}

func TestAutomaticVideoChannelTestSkipsHealthAndStatusUpdates(t *testing.T) {
	channels := []*model.Channel{
		{Type: constant.ChannelTypeMiniMax, Models: "MiniMax-H3", Status: common.ChannelStatusEnabled},
		{Type: constant.ChannelTypeMiniMax, Models: "MiniMax-H3", Status: common.ChannelStatusAutoDisabled},
	}
	processed := 0
	summary := performChannelTests(context.Background(), channels, 0, true, func(done, total int) {
		processed = done
		require.Equal(t, len(channels), total)
	})
	require.Equal(t, channelTestSummary{}, summary)
	require.Equal(t, len(channels), processed)
}
