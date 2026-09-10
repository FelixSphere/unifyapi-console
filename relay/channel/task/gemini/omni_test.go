package gemini

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestOmniBackgroundRequest(t *testing.T) {
	data, err := omniRequest(relaycommon.TaskSubmitReq{Prompt: "animate", Images: []string{"https://example.com/a.png"}, Metadata: map[string]any{"model": "other", "store": false, "background": false}})
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, common.Unmarshal(data, &body))
	assert.Equal(t, omniModel, body["model"])
	assert.Equal(t, true, body["store"])
	assert.Equal(t, true, body["background"])
	assert.Len(t, body["input"], 2)
}
func TestOmniResultsAndVeoCompatibility(t *testing.T) {
	a := &TaskAdaptor{}
	for _, tc := range []struct {
		body   string
		status model.TaskStatus
	}{
		{`{"id":"a","status":"in_progress"}`, model.TaskStatusInProgress},
		{`{"id":"a","status":"failed","error":{"message":"safety"}}`, model.TaskStatusFailure},
		{`{"id":"a","status":"completed","steps":[{"type":"model_output","content":[{"type":"video","data":"YWJj","mime_type":"video/mp4"}]}]}`, model.TaskStatusSuccess},
		{`{"name":"models/veo/operations/old","done":false}`, model.TaskStatusInProgress},
	} {
		result, err := a.ParseTaskResult([]byte(tc.body))
		require.NoError(t, err)
		assert.EqualValues(t, tc.status, result.Status)
		if tc.status == model.TaskStatusSuccess {
			assert.Equal(t, "data:video/mp4;base64,YWJj", result.Url)
		}
	}
	_, err := a.ParseTaskResult([]byte(`{"id":"a","status":"completed"}`))
	require.Error(t, err)
}
func TestDeveloperAndVertexVeoPricingDiffer(t *testing.T) {
	assert.Equal(t, 1.6, GeminiDeveloperVeoRatio("veo-3.1-lite-generate-preview", "1080p"))
	assert.Equal(t, 3.0, GeminiDeveloperVeoRatio("veo-3.1-fast-generate-preview", "4k"))
	assert.InDelta(t, 0.35/0.15, VeoResolutionRatio("veo-3.1-fast-generate-preview", "4k"), 0.000001)
}
