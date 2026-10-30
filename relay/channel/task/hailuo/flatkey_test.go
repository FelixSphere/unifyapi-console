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
	"github.com/stretchr/testify/require"
)

func TestFlatkeyProtocolSelectionAndH3Fields(t *testing.T) {
	for _, base := range []string{"https://router.flatkey.ai", "https://console.flatkey.ai/", "https://api.minimax.io", "https://router.flatkey.ai.evil.example", "https://router.flatkey.ai/v1"} {
		t.Run(base, func(t *testing.T) {
			info := h3Info("MiniMax-H3")
			info.ChannelBaseUrl = base
			a := &TaskAdaptor{}
			a.Init(info)
			endpoint, err := a.BuildRequestURL(info)
			require.NoError(t, err)
			hosted := base == "https://router.flatkey.ai" || base == "https://console.flatkey.ai/"
			suffix := "/v2/video_generation"
			if hosted {
				suffix = "/v1/videos"
			}
			require.Equal(t, strings.TrimRight(base, "/")+suffix, endpoint)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set("task_request", relaycommon.TaskSubmitReq{Model: "MiniMax-H3", Prompt: "A red ball rolls", Size: "768P", Duration: 5, Metadata: map[string]any{"aigc_watermark": false}})
			body, err := a.BuildRequestBody(c, info)
			require.NoError(t, err)
			data, err := io.ReadAll(body)
			require.NoError(t, err)
			var payload map[string]any
			require.NoError(t, common.Unmarshal(data, &payload))
			require.Equal(t, "768P", payload["resolution"])
			require.EqualValues(t, 5, payload["duration"])
			require.Equal(t, "16:9", payload["ratio"])
			require.NotEmpty(t, payload["content"])
			require.NotContains(t, payload, "size")
			if hosted {
				require.Equal(t, false, payload["aigc_watermark"])
			} else {
				require.NotContains(t, payload, "aigc_watermark")
			}
		})
	}
}

func TestFlatkeyResponsePollingAndContent(t *testing.T) {
	info := h3Info("MiniMax-H3")
	info.ChannelBaseUrl = "https://router.flatkey.ai"
	info.PublicTaskID = "task_public"
	a := &TaskAdaptor{}
	a.Init(info)
	for _, data := range []string{`{"id":"task_upstream","status":"queued"}`, `{"task_id":"task_upstream","status":"queued"}`} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		id, _, taskErr := a.DoResponse(c, &http.Response{Body: io.NopCloser(strings.NewReader(data))}, info)
		require.Nil(t, taskErr)
		require.Equal(t, flatkeyTaskPrefix+"task_upstream", id)
		require.NotContains(t, w.Body.String(), "task_upstream")
		require.Contains(t, w.Body.String(), "task_public")
	}
	for status, expected := range map[string]string{"queued": model.TaskStatusQueued, "in_progress": model.TaskStatusInProgress, "completed": model.TaskStatusSuccess, "failed": model.TaskStatusFailure} {
		result, err := a.ParseTaskResult([]byte(`{"id":"task_upstream","status":"` + status + `","error":{"message":"provider rejected"}}`))
		require.NoError(t, err)
		require.Equal(t, expected, result.Status)
		if status == "failed" {
			require.Equal(t, "provider rejected", result.Reason)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/videos/task_upstream", r.URL.Path)
		require.Equal(t, "Bearer fixture-key", r.Header.Get("Authorization"))
		w.Write([]byte(`{"id":"task_upstream","status":"completed"}`))
	}))
	defer server.Close()
	response, err := (&TaskAdaptor{}).FetchTask(server.URL, "fixture-key", map[string]any{"task_id": flatkeyTaskPrefix + "task_upstream"}, "")
	require.NoError(t, err)
	response.Body.Close()
	contentURL, ok := FlatkeyContentURL("https://router.flatkey.ai/", flatkeyTaskPrefix+"task_upstream")
	require.True(t, ok)
	require.Equal(t, "https://router.flatkey.ai/v1/videos/task_upstream/content", contentURL)
	_, ok = FlatkeyContentURL("https://api.minimax.io", h3TaskPrefix+"native")
	require.False(t, ok)
}

func TestFlatkeyRejectsHTMLAndMissingTaskID(t *testing.T) {
	info := h3Info("MiniMax-H3")
	info.ChannelBaseUrl = "https://router.flatkey.ai"
	a := &TaskAdaptor{}
	a.Init(info)
	for _, body := range []string{"<!doctype html><html>console</html>", `{}`, `{"error":{"message":"rejected"}}`} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		_, _, err := a.DoResponse(c, &http.Response{Body: io.NopCloser(strings.NewReader(body))}, info)
		require.NotNil(t, err)
		require.NotContains(t, err.Error.Error(), "<!doctype")
	}
}
