package ali

import (
	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestHappyHorseWireRequests(t *testing.T) {
	for _, tc := range []struct {
		name, model, role string
		images            []string
	}{
		{"text", "happyhorse-1.1-t2v", "", nil},
		{"first frame", "happyhorse-1.1-i2v", "first_frame", []string{"https://example.com/a.png"}},
		{"reference", "happyhorse-1.1-r2v", "reference_image", []string{"https://example.com/a.png", "https://example.com/b.png"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := relaycommon.TaskSubmitReq{Model: tc.model, Prompt: "animate", Images: tc.images, Seconds: "8", Size: "720p", Metadata: map[string]any{"parameters": map[string]any{"watermark": false, "seed": 0}}}
			r, err := convertHappyHorseRequest(testRelayInfo(), req)
			require.NoError(t, err)
			data, err := common.Marshal(r)
			require.NoError(t, err)
			assert.Contains(t, string(data), `"duration":8`)
			assert.Contains(t, string(data), `"watermark":false`)
			assert.Contains(t, string(data), `"seed":0`)
			assert.NotContains(t, string(data), `"img_url"`)
			assert.NotContains(t, string(data), `"size"`)
			assert.NotContains(t, string(data), `"prompt_extend"`)
			if tc.role != "" {
				assert.Equal(t, tc.role, r.Input.Media[0].Type)
			}
		})
	}
}

func TestHappyHorseRejectsInvalidMetadataAndInputs(t *testing.T) {
	for _, tc := range []struct {
		name     string
		metadata map[string]any
		model    string
		images   []string
	}{
		{name: "duration bypass", metadata: map[string]any{"parameters": map[string]any{"duration": 999999}}},
		{name: "null duration", metadata: map[string]any{"parameters": map[string]any{"duration": nil}}},
		{name: "negative duration", metadata: map[string]any{"parameters": map[string]any{"duration": -1}}},
		{name: "model override", metadata: map[string]any{"model": "other"}},
		{name: "missing image", model: "happyhorse-1.1-i2v"},
		{name: "image ratio", model: "happyhorse-1.1-i2v", images: []string{"https://example.com/a.png"}, metadata: map[string]any{"parameters": map[string]any{"ratio": "16:9"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := tc.model
			if name == "" {
				name = "happyhorse-1.1-t2v"
			}
			_, err := convertHappyHorseRequest(testRelayInfo(), relaycommon.TaskSubmitReq{Model: name, Prompt: "animate", Metadata: tc.metadata, Images: tc.images})
			require.Error(t, err)
		})
	}
}
