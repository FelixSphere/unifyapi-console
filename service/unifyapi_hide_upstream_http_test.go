package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// UNIFYAPI-FORK: the two writers every whole-body answer and every copied
// header go through, with and without the channel's hide_upstream setting.

func contextForChannelSettings(t *testing.T, channelSettings string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	var settings dto.ChannelOtherSettings
	require.NoError(t, common.UnmarshalJsonStr(channelSettings, &settings))

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, "20261126093015123456789a1b2c3d4")
	c.Set(string(constant.ContextKeyOriginalModel), "deepseek-v4-pro")
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, settings)
	return c, recorder
}

// The header names are the ones two suppliers returned on production on
// 2026-11-26.
func TestAHideUpstreamChannelCopiesOnlyTheHeadersNeededToReadTheBody(t *testing.T) {
	upstreamsOwn := []string{
		"X-Openrouter-Release", "X-Generation-Id", "Cf-Ray", "Set-Cookie", "Alt-Svc",
		"Permissions-Policy", "Referrer-Policy", "Access-Control-Expose-Headers",
		"Via", "X-Gw-Region", "X-Tt-Logid", "X-Dsa-Trace-Id", "X-Envoy-Upstream-Service-Time",
		"Server-Timing", "Upstream-Caught", "X-Client-Request-Id", "X-Request-Id", "X-Request-Ip",
		"Openai-Organization", "X-Ratelimit-Remaining-Requests", "Server", "Date",
	}
	neededToReadTheBody := []string{"Content-Type", "content-type", "Content-Encoding", "Content-Disposition", "Cache-Control"}

	hidden, _ := contextForChannelSettings(t, `{"hide_upstream":true}`)
	shown, _ := contextForChannelSettings(t, `{}`)

	for _, name := range upstreamsOwn {
		assert.False(t, ShouldCopyUpstreamHeader(hidden, name, []string{"value"}), "%s on a hide_upstream channel", name)
		assert.True(t, ShouldCopyUpstreamHeader(shown, name, []string{"value"}), "%s on any other channel, as before", name)
	}
	for _, name := range neededToReadTheBody {
		assert.True(t, ShouldCopyUpstreamHeader(hidden, name, []string{"value"}), "%s on a hide_upstream channel", name)
		assert.True(t, ShouldCopyUpstreamHeader(shown, name, []string{"value"}), "%s on any other channel", name)
	}
}

// An upstream that is itself a new-api sends its own X-Oneapi-Request-Id. It
// is never copied, and it is still recorded for the admin log on a
// hide_upstream channel.
func TestAHideUpstreamChannelStillRecordsTheUpstreamRequestIdForAdmins(t *testing.T) {
	c, _ := contextForChannelSettings(t, `{"hide_upstream":true}`)

	assert.False(t, ShouldCopyUpstreamHeader(c, common.RequestIdKey, []string{"2026112609301599upstream"}))
	assert.Equal(t, "2026112609301599upstream", c.GetString(common.UpstreamRequestIdKey))
}

// Speech and other binary answers leave through the same writer. A body that
// is not a JSON object is the upstream's bytes on every channel.
func TestAHideUpstreamChannelLeavesABodyThatIsNotJSONAlone(t *testing.T) {
	audio := []byte{0x49, 0x44, 0x33, 0x04, 0x00, 0x7b, 0x22, 0x6d, 0x6f, 0x64, 0x65, 0x6c, 0x22, 0xff, 0xfb}
	c, recorder := contextForChannelSettings(t, `{"hide_upstream":true}`)

	IOCopyBytesGracefully(c, &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"audio/mpeg"}, "X-Request-Id": {"req_upstream"}},
	}, audio)

	assert.Equal(t, audio, recorder.Body.Bytes())
	assert.Equal(t, http.Header{"Content-Type": {"audio/mpeg"}, "Content-Length": {"15"}}, recorder.Header())
}
