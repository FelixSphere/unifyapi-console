package openrouter

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func testInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_public"}, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "google/veo-3.1", ChannelBaseUrl: "https://openrouter.ai/api", ApiKey: "fixture-key"}, OriginModelName: "my-video"}
}
func TestNativeFieldsAndMetadataMatchBilling(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{"model":"my-video","prompt":"A train","duration":4,"resolution":"1080p","generate_audio":false,"frame_images":[{"type":"image_url","image_url":{"url":"https://example.com/a.png"},"frame_type":"first_frame"}],"metadata":{"duration":6,"model":"wrong"}}`))
	c.Request.Header.Set("Content-Type", "application/json")
	a := &TaskAdaptor{}
	info := testInfo()
	a.Init(info)
	require.Nil(t, a.ValidateRequestAndSetAction(c, info))
	// Model mapping occurs after validation in the real relay.
	info.UpstreamModelName = "google/veo-3.1"
	r, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	raw, err := io.ReadAll(r)
	require.NoError(t, err)
	var p map[string]any
	require.NoError(t, common.Unmarshal(raw, &p))
	require.Equal(t, "google/veo-3.1", p["model"])
	require.Equal(t, "1080p", p["resolution"])
	require.Equal(t, false, p["generate_audio"])
	require.EqualValues(t, 6, p["duration"])
	require.NotEmpty(t, p["frame_images"])
	require.Equal(t, map[string]float64{"seconds": 6}, a.EstimateBilling(c, info))
	require.NotContains(t, p, "metadata")
	endpoint, err := a.BuildRequestURL(info)
	require.NoError(t, err)
	require.Equal(t, "https://openrouter.ai/api/v1/videos", endpoint)
}
func TestFrameConversionAndDurationValidation(t *testing.T) {
	info := testInfo()
	a := &TaskAdaptor{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := relaycommon.TaskSubmitReq{Model: "my-video", Prompt: "animate", Images: []string{"https://example.com/a.png", "https://example.com/b.png"}, Seconds: "8", Size: "768P"}
	c.Set("task_request", req)
	p, err := payload(c, info)
	require.NoError(t, err)
	frames := p["frame_images"].([]map[string]any)
	require.Equal(t, "last_frame", frames[1]["frame_type"])
	require.Equal(t, map[string]string{"url": "https://example.com/a.png"}, frames[0]["image_url"])
	require.Equal(t, "768p", p["resolution"])
	req.Metadata = map[string]any{"duration": 0}
	c.Set("task_request", req)
	_, err = a.BuildRequestBody(c, info)
	require.Error(t, err)
	req.Metadata = map[string]any{"duration": relaycommon.MaxTaskDurationSeconds + 1}
	c.Set("task_request", req)
	_, err = a.BuildRequestBody(c, info)
	require.Error(t, err)
}
func TestOpenRouterJobResponses(t *testing.T) {
	a := &TaskAdaptor{}
	info := testInfo()
	a.Init(info)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	id, _, taskErr := a.DoResponse(c, &http.Response{StatusCode: 202, Body: io.NopCloser(strings.NewReader(`{"id":"upstream","status":"pending","error":""}`))}, info)
	require.Nil(t, taskErr)
	require.Equal(t, "upstream", id)
	require.Contains(t, w.Body.String(), "task_public")
	require.NotContains(t, w.Body.String(), "upstream")
	for state, expected := range map[string]string{"pending": model.TaskStatusQueued, "processing": model.TaskStatusInProgress, "completed": model.TaskStatusSuccess, "failed": model.TaskStatusFailure, "expired": model.TaskStatusFailure} {
		result, err := a.ParseTaskResult([]byte(`{"id":"upstream","status":"` + state + `","error":"provider rejected"}`))
		require.NoError(t, err)
		require.Equal(t, expected, result.Status)
		if expected == model.TaskStatusFailure {
			require.Equal(t, "provider rejected", result.Reason)
		}
	}
	_, _, taskErr = a.DoResponse(c, &http.Response{Body: io.NopCloser(strings.NewReader(`{"id":"","error":"not accepted"}`))}, info)
	require.NotNil(t, taskErr)
}
func TestRegisteredDefaults(t *testing.T) {
	require.Contains(t, (&TaskAdaptor{}).GetModelList(), "minimax/hailuo-3")
	d, r, ok := TestDefaults("minimax/hailuo-3")
	require.True(t, ok)
	require.GreaterOrEqual(t, d, 4)
	require.Equal(t, "768p", r)
}
