package kling

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestV3RequestAndRoute(t *testing.T) {
	for _, name := range []string{"kling-3.0", "kling-3.0-turbo", "kling-3.0-omni"} {
		info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: name}}
		body, err := makeV3Request(relaycommon.TaskSubmitReq{Prompt: "animate", Images: []string{"https://example.com/frame.png"}, Seconds: "12", Metadata: map[string]any{"settings": map[string]any{"multi_shot": false}}}, info)
		require.NoError(t, err)
		assert.Contains(t, string(body), `"duration":12`)
		assert.Contains(t, string(body), `"multi_shot":false`)
		assert.Contains(t, string(body), `"type":"first_frame"`)
		url, err := (&TaskAdaptor{baseURL: "https://api-singapore.klingai.com"}).BuildRequestURL(info)
		require.NoError(t, err)
		path := "/image-to-video/"
		if name == "kling-3.0-omni" {
			path = "/omni-video/"
		}
		assert.Equal(t, "https://api-singapore.klingai.com"+path+name, url)
	}
}
func TestV3PollAndLegacyCompatibility(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/tasks", r.URL.Path)
		assert.Equal(t, "123", r.URL.Query().Get("task_ids"))
		w.Write([]byte(`{"code":0,"data":[{"id":"123","status":"succeeded","outputs":[{"type":"video","url":"https://example.com/v.mp4"}]}]}`))
	}))
	defer server.Close()
	a := &TaskAdaptor{}
	response, err := a.FetchTask(server.URL, "test", map[string]any{"task_id": v3TaskPrefix + "123", "action": constant.TaskActionGenerate}, "")
	require.NoError(t, err)
	response.Body.Close()
	result, err := a.ParseTaskResult([]byte(`{"code":0,"data":[{"id":"123","status":"succeeded","outputs":[{"type":"video","url":"https://example.com/v.mp4"}]}]}`))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusSuccess, result.Status)
	assert.Equal(t, "https://example.com/v.mp4", result.Url)
	result, err = a.ParseTaskResult([]byte(`{"code":0,"data":{"task_id":"old","task_status":"processing"}}`))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusInProgress, result.Status)
}
