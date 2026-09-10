package jimeng

import (
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestJimengV3ChoosesMatchingRequestKey(t *testing.T) {
	for _, tc := range []struct {
		name   string
		images []string
		key    string
	}{
		{"jimeng_v30_720p", nil, "jimeng_t2v_v30_720p"},
		{"jimeng_v30_1080p", []string{"https://example.com/a.png"}, "jimeng_i2v_first_v30_1080"},
		{"jimeng_v30_pro", nil, "jimeng_ti2v_v30_pro"},
	} {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: tc.name}}
		body, err := (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{Prompt: "animate", Images: tc.images, Duration: 10}, info)
		require.NoError(t, err)
		assert.Equal(t, tc.key, body.ReqKey)
		assert.Equal(t, 241, body.Frames)
	}
}
