package doubao

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSeedance25EditWireContract(t *testing.T) {
	r, err := (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{Model: "doubao-seedance-2-5-260628", Prompt: "edit the background", Metadata: map[string]any{
		"omni_reference_task_type": "edit", "duration": -1, "ratio": "adaptive", "output_format": "mov", "generate_audio": false, "seed": 0,
		"content": []any{map[string]any{"type": "video_url", "video_url": map[string]any{"url": "https://example.com/input.mp4"}, "role": "reference_video"}},
	}})
	require.NoError(t, err)
	data, err := common.Marshal(r)
	require.NoError(t, err)
	for _, fragment := range []string{`"omni_reference_task_type":"edit"`, `"duration":-1`, `"output_format":"mov"`, `"generate_audio":false`, `"seed":0`, `"role":"reference_video"`} {
		assert.Contains(t, string(data), fragment)
	}
}

func TestSeedance25OfficialModelAndLASRoutes(t *testing.T) {
	a := &TaskAdaptor{baseURL: "https://operator.las.ap-southeast-1.bytepluses.com"}
	endpoint, err := a.BuildRequestURL(nil)
	require.NoError(t, err)
	assert.Equal(t, "https://operator.las.ap-southeast-1.bytepluses.com/api/v1/contents/generations/tasks", endpoint)
	require.Contains(t, a.GetModelList(), "dreamina-seedance-2-5-260628")

	r, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Model: "dreamina-seedance-2-5-260628", Prompt: "animate", Duration: 30,
	})
	require.NoError(t, err)
	assert.Equal(t, "dreamina-seedance-2-5-260628", r.Model)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("task_request", relaycommon.TaskSubmitReq{Model: "doubao-seedance-2-5-260628", Prompt: "animate", Duration: 4})
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	body, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"model":"dreamina-seedance-2-5-260628"`)
	assert.Equal(t, "dreamina-seedance-2-5-260628", info.UpstreamModelName)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		assert.Equal(t, "/api/v1/contents/generations/tasks/task%2Fwith%2Fslashes", req.URL.EscapedPath())
		assert.Equal(t, "Bearer test-key", req.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"succeeded"}`))
	}))
	defer server.Close()

	_, err = a.FetchTask(server.URL+"/api/v1", "test-key", map[string]any{"task_id": "task/with/slashes"}, "")
	require.NoError(t, err)
}

func TestSeedance25PricingRatios(t *testing.T) {
	for _, model := range []string{"doubao-seedance-2-5-260628", "dreamina-seedance-2-5-260628"} {
		withoutVideo, ok := GetVideoInputRatio(model, "720p", false)
		require.True(t, ok)
		assert.InDelta(t, 1, withoutVideo, 1e-12)

		withVideo, ok := GetVideoInputRatio(model, "720p", true)
		require.True(t, ok)
		assert.InDelta(t, 6.4/10.7, withVideo, 1e-12)
	}
}

func TestSeedance25EstimateBillingDetectsVideoInput(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("task_request", relaycommon.TaskSubmitReq{Metadata: map[string]any{
		"resolution": "720p",
		"content": []any{map[string]any{
			"type": "video_url", "video_url": map[string]any{"url": "https://example.com/reference.mp4"},
		}},
	}})
	info := &relaycommon.RelayInfo{OriginModelName: "dreamina-seedance-2-5-260628"}

	ratios := (&TaskAdaptor{}).EstimateBilling(c, info)
	require.Contains(t, ratios, "video_input")
	assert.InDelta(t, 6.4/10.7, ratios["video_input"], 1e-12)
}

func TestSeedance25DurationBoundsAndTerminalExpiry(t *testing.T) {
	for _, duration := range []int{-2, 0, 3, 31, 1000000} {
		_, err := (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{Model: "doubao-seedance-2-5-260628", Prompt: "animate", Metadata: map[string]any{"duration": duration}})
		require.Error(t, err)
	}
	r, err := (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{Model: "doubao-seedance-2-5-260628", Prompt: "animate", Duration: 30})
	require.NoError(t, err)
	assert.EqualValues(t, 30, *r.Duration)
	result, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{"id":"123","status":"expired","error":{"message":"deadline exceeded"}}`))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusFailure, result.Status)

	result, err = (&TaskAdaptor{}).ParseTaskResult([]byte(`{"id":"123","status":"succeeded","usage":{"completion_tokens":648900,"total_tokens":648900}}`))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusSuccess, result.Status)
	assert.Equal(t, 648900, result.CompletionTokens)
	assert.Equal(t, 648900, result.TotalTokens)
}
