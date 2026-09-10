package doubao

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
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
}
