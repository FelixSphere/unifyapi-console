package sora

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestFlatkeySeedanceWirePrompt(t *testing.T) {
	for _, tc := range []struct {
		base, model string
		converts    bool
	}{
		{"https://router.flatkey.ai", "seedance-2.5", true},
		{"https://console.flatkey.ai/", "seedance-2.0-pro", true},
		{"https://router.flatkey.ai", "MiniMax-H3", false},
		{"https://api.openai.com", "seedance-2.5", false},
		{"https://router.flatkey.ai.evil.example", "seedance-2.5", false},
		{"https://router.flatkey.ai/v1", "seedance-2.5", false},
	} {
		t.Run(tc.base+tc.model, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/videos", strings.NewReader(`{"model":"seedance-2.5","prompt":"rolling ball","duration":5,"size":"720p"}`))
			c.Request.Header.Set("Content-Type", "application/json")
			a := &TaskAdaptor{baseURL: tc.base}
			info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: tc.model}}
			require.Nil(t, a.ValidateRequestAndSetAction(c, info))
			reader, err := a.BuildRequestBody(c, info)
			require.NoError(t, err)
			data, err := io.ReadAll(reader)
			require.NoError(t, err)
			var body map[string]interface{}
			require.NoError(t, json.Unmarshal(data, &body))
			require.Equal(t, tc.model, body["model"])
			if !tc.converts {
				require.Equal(t, "rolling ball", body["prompt"])
				require.NotContains(t, body, "content")
				return
			}
			require.Equal(t, []interface{}{map[string]interface{}{"type": "text", "text": "rolling ball"}}, body["content"])
			require.Equal(t, "720p", body["resolution"])
			require.Equal(t, float64(5), body["duration"])
			require.NotContains(t, body, "prompt")
			require.NotContains(t, body, "size")
		})
	}
}

func TestFlatkeySeedanceNativeReferencesAndMetadata(t *testing.T) {
	native := []interface{}{
		map[string]interface{}{"type": "text", "text": "native prompt"},
		map[string]interface{}{"type": "video_url", "video_url": map[string]interface{}{"url": "https://example.com/video.mp4"}, "role": "reference_video"},
	}
	body := map[string]interface{}{"model": "seedance-2.5", "content": native, "resolution": "480p"}
	normalizeFlatkeySeedance(body, relaycommon.TaskSubmitReq{Prompt: "default must not replace native text", Seconds: "8", Duration: 5, Size: "720p", Images: []string{"https://example.com/image.png"}, Metadata: map[string]interface{}{"generate_audio": false, "model": "wrong", "content": []interface{}{}}})
	content := body["content"].([]interface{})
	require.Equal(t, native, content[:2])
	require.Equal(t, "image_url", content[2].(map[string]interface{})["type"])
	require.Equal(t, 8, body["duration"])
	require.Equal(t, "480p", body["resolution"])
	require.Equal(t, false, body["generate_audio"])
	require.Equal(t, "seedance-2.5", body["model"])
}

func TestFlatkeySeedanceMetadataContent(t *testing.T) {
	body := map[string]interface{}{"prompt": "animate", "metadata": map[string]interface{}{}}
	normalizeFlatkeySeedance(body, relaycommon.TaskSubmitReq{Prompt: "animate", Metadata: map[string]interface{}{"content": []interface{}{map[string]interface{}{"type": "image_url", "image_url": map[string]interface{}{"url": "https://example.com/frame.png"}, "role": "first_frame"}}}})
	content := body["content"].([]interface{})
	require.Len(t, content, 2)
	require.Equal(t, "first_frame", content[0].(map[string]interface{})["role"])
	require.Equal(t, "animate", content[1].(map[string]interface{})["text"])
	require.NotContains(t, body, "metadata")
}
