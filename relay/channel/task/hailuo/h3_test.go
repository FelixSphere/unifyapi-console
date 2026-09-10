package hailuo

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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func h3Info(name string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: name}, OriginModelName: name}
}

func TestH3BuildAndBillSameEffectiveParameters(t *testing.T) {
	for _, tc := range []struct {
		name, res string
		seconds   int
		ratio     float64
	}{
		{"MiniMax-H3", "768P", 4, 1}, {"MiniMax-H3", "2K", 15, 1.625}, {"MiniMax-H3-Max", "480P", 5, 0.625}, {"MiniMax-H3-Max", "768P", 15, 1},
	} {
		t.Run(tc.name+tc.res, func(t *testing.T) {
			info := h3Info(tc.name)
			req := relaycommon.TaskSubmitReq{Model: tc.name, Prompt: "a moving train", Metadata: map[string]any{"resolution": tc.res, "duration": tc.seconds}}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set("task_request", req)
			a := &TaskAdaptor{}
			body, err := a.BuildRequestBody(c, info)
			require.NoError(t, err)
			data, err := io.ReadAll(body)
			require.NoError(t, err)
			var wire h3Request
			require.NoError(t, common.Unmarshal(data, &wire))
			assert.Equal(t, tc.seconds, *wire.Duration)
			assert.Equal(t, "16:9", wire.Ratio)
			assert.Equal(t, "text", wire.Content[0].Type)
			assert.Equal(t, map[string]float64{"seconds": float64(tc.seconds), "resolution": tc.ratio}, a.EstimateBilling(c, info))
		})
	}
}

func TestH3MapsFramesAndProtectsModel(t *testing.T) {
	info := h3Info("MiniMax-H3")
	info.IsModelMapped = true
	r, err := convertH3Request(&relaycommon.TaskSubmitReq{Model: "customer-alias", Prompt: "animate", Images: []string{"https://example.com/a.png", "https://example.com/b.png"}, Metadata: map[string]any{"model": "MiniMax-H3-Max"}}, info)
	require.NoError(t, err)
	assert.Equal(t, "MiniMax-H3", r.Model)
	assert.Equal(t, "adaptive", r.Ratio)
	assert.Equal(t, "first_frame", r.Content[0].Role)
	assert.Equal(t, "last_frame", r.Content[1].Role)
}

func TestH3RejectsUnsupportedParameters(t *testing.T) {
	for _, tc := range []struct {
		name, res string
		duration  int
	}{
		{"MiniMax-H3", "768P", 0}, {"MiniMax-H3", "768P", 16}, {"MiniMax-H3", "1080P", 5}, {"MiniMax-H3-Max", "2K", 5}, {"MiniMax-H3-Max", "768P", 4},
	} {
		_, err := convertH3Request(&relaycommon.TaskSubmitReq{Model: tc.name, Prompt: "animate", Metadata: map[string]any{"duration": tc.duration, "resolution": tc.res}}, h3Info(tc.name))
		require.Error(t, err)
	}
}

func TestH3PollingKeepsV1TasksCompatible(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		w.Write([]byte(`{}`))
	}))
	defer server.Close()
	a := &TaskAdaptor{}
	for _, id := range []string{"123", h3TaskPrefix + "456"} {
		resp, err := a.FetchTask(server.URL, "test-key", map[string]any{"task_id": id}, "")
		require.NoError(t, err)
		resp.Body.Close()
	}
	assert.Equal(t, []string{"/v1/query/video_generation?task_id=123", "/v2/query/video_generation/456"}, paths)
}

func TestH3TaskTerminalStates(t *testing.T) {
	for _, tc := range []struct{ status, want string }{
		{"queued", model.TaskStatusQueued}, {"running", model.TaskStatusInProgress}, {"succeeded", model.TaskStatusSuccess}, {"failed", model.TaskStatusFailure}, {"cancelled", model.TaskStatusFailure},
	} {
		result, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{"task":{"id":"123","status":"` + tc.status + `","content":{"url":"https://example.com/video.mp4"},"error":{"code":"1026","message":"rejected"}}}`))
		require.NoError(t, err)
		assert.Equal(t, tc.want, result.Status)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := h3Info("MiniMax-H3")
	id, _, err := (&TaskAdaptor{}).DoResponse(c, &http.Response{Body: io.NopCloser(strings.NewReader(`{"task_id":"123"}`))}, info)
	require.Nil(t, err)
	assert.Equal(t, h3TaskPrefix+"123", id)
}

func TestH3SettlesReferenceInputsAtSnapshottedCustomerPrice(t *testing.T) {
	saved := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = saved })
	task := &model.Task{Data: []byte(`{"task":{"id":"123","model":"MiniMax-H3","status":"succeeded","usage":{"input_seconds":3,"output_seconds":5,"input_image_count":7}}}`)}
	task.PrivateData.UpstreamTaskID = h3TaskPrefix + "123"
	task.PrivateData.BillingContext = &model.TaskBillingContext{PriceUnit: "second", PerCallBilling: true, ModelPrice: 0.08, GroupRatio: 0.9, OtherRatios: map[string]float64{"resolution": 1.625}}
	quota := (&TaskAdaptor{}).AdjustBillingOnComplete(task, &relaycommon.TaskInfo{Status: model.TaskStatusSuccess})
	// 8 seconds * $0.13 + 2 extra images * $0.04 = $1.12, at 90% = $1.008.
	assert.Equal(t, 504000, quota)
	task.Data = []byte(`{"task":{"id":"123","model":"MiniMax-H3","usage":{"output_seconds":1000000000000000000}}}`)
	assert.Zero(t, (&TaskAdaptor{}).AdjustBillingOnComplete(task, &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}))
}
