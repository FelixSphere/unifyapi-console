package ali

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWan3ReservesAndSettlesInputVideo(t *testing.T) {
	info := testRelayInfo()
	info.UpstreamModelName = "wan3.0-video"
	req := relaycommon.TaskSubmitReq{Model: "wan3.0-video", Prompt: "extend", Metadata: map[string]any{
		"parameters": map[string]any{"duration": -1, "resolution": "1080P", "audio": false},
		"input":      map[string]any{"media": []any{map[string]any{"type": "reference_video", "url": "https://example.com/in.mp4"}}},
	}}
	body, err := convertWan3Request(info, req)
	require.NoError(t, err)
	require.NotNil(t, body.Parameters.Audio)
	assert.False(t, *body.Parameters.Audio)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("task_request", req)
	ratios := (&TaskAdaptor{}).EstimateBilling(c, info)
	assert.Equal(t, map[string]float64{"seconds": 30, "resolution": 2}, ratios)
	task := &model.Task{Properties: model.Properties{UpstreamModelName: "wan3.0-video"}, Data: []byte(`{"usage":{"duration":5.5,"input_video_duration":3.5,"output_video_duration":5.5,"video_count":1}}`)}
	task.PrivateData.BillingContext = &model.TaskBillingContext{PriceUnit: "second", ModelPrice: 0.1, GroupRatio: 0.9, OtherRatios: ratios}
	assert.Equal(t, 810000, (&TaskAdaptor{}).AdjustBillingOnComplete(task, &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}))
}

func TestWan3RejectsMixedFramesAndReferences(t *testing.T) {
	_, err := convertWan3Request(testRelayInfo(), relaycommon.TaskSubmitReq{Model: "wan3.0-video", Prompt: "animate", Metadata: map[string]any{"input": map[string]any{"media": []any{map[string]any{"type": "first_frame", "url": "a"}, map[string]any{"type": "reference_audio", "url": "b"}}}}})
	require.Error(t, err)
}
