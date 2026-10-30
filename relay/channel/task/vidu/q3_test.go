package vidu

import (
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io"
	"net/http/httptest"
	"testing"
)

func TestQ3ReferenceWireContract(t *testing.T) {
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: constant.TaskActionReferenceGenerate}, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "viduq3-pro"}}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("task_request", relaycommon.TaskSubmitReq{Model: "viduq3-pro", Prompt: "animate", Seconds: "16", Images: []string{"https://example.com/a.png"}, Metadata: map[string]any{"audio": false, "off_peak": false, "seed": 0}})
	a := &TaskAdaptor{baseURL: "https://api.vidu.com"}
	body, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	for _, part := range []string{`"model":"viduq3"`, `"audio":false`, `"off_peak":false`, `"seed":0`, `"duration":16`} {
		assert.Contains(t, string(data), part)
	}
	url, err := a.BuildRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://api.vidu.com/ent/v2/reference2video", url)
}
