package controller

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"strconv"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/gin-gonic/gin"
)

var errVideoChannelTestUnsupported = errors.New("video models cannot be tested with the synchronous channel test; submit POST /v1/videos and poll GET /v1/videos/{id} to verify generation")

// Video generation is an asynchronous, billable task. The synchronous channel
// tester must neither send chat payloads to it nor claim a skipped test passed.
func validateSynchronousChannelTest(channel *model.Channel, modelName string) error {
	switch channel.Type {
	case constant.ChannelTypeKling, constant.ChannelTypeJimeng, constant.ChannelTypeDoubaoVideo,
		constant.ChannelTypeVidu, constant.ChannelTypeSora:
		return fmt.Errorf("%s: %w", modelName, errVideoChannelTestUnsupported)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("model_mapping", channel.GetModelMapping())
	info := &relaycommon.RelayInfo{OriginModelName: modelName, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: modelName}}
	if err := helper.ModelMappedHelper(c, info, nil); err != nil {
		return err
	}
	// Check every registered video model, including names served by an OpenAI
	// compatible proxy. Reuse adapter discovery so new model IDs stay covered.
	for _, channelType := range []int{constant.ChannelTypeAli, constant.ChannelTypeMiniMax,
		constant.ChannelTypeDoubaoVideo, constant.ChannelTypeKling, constant.ChannelTypeJimeng,
		constant.ChannelTypeVidu, constant.ChannelTypeGemini, constant.ChannelTypeVertexAi, constant.ChannelTypeSora, constant.ChannelTypeOpenRouter} {
		adaptor := relay.GetTaskAdaptor(constant.TaskPlatform(strconv.Itoa(channelType)))
		for _, videoModel := range adaptor.GetModelList() {
			if modelName == videoModel || info.UpstreamModelName == videoModel {
				return fmt.Errorf("%s: %w", modelName, errVideoChannelTestUnsupported)
			}
		}
	}
	return nil
}
