package claude

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The non-streaming body is the vendor's documented refusal, verbatim.
const refusalMessageBody = `{
  "id": "msg_01XFUDYJgAACzvnptvVoYEL",
  "type": "message",
  "role": "assistant",
  "model": "claude-fable-5",
  "content": [],
  "stop_reason": "refusal",
  "stop_details": {
    "type": "refusal",
    "category": "cyber",
    "explanation": "This request was declined because it could enable cyber harm."
  },
  "usage": {"input_tokens": 412, "output_tokens": 0}
}`

const refusalMessageDelta = `{"type":"message_delta","delta":{"stop_reason":"refusal","stop_sequence":null,"stop_details":{"type":"refusal","category":"reasoning_extraction","explanation":"declined"}},"usage":{"output_tokens":0}}`

func refusalTestContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	return c, w
}

func refusalTestInfo(model string, format types.RelayFormat) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName: model,
		RelayFormat:     format,
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeAnthropic},
	}
}

func rejectReason(c *gin.Context) string {
	return common.GetContextKeyString(c, constant.ContextKeyAdminRejectReason)
}

func TestClaudeRefusalRejectReasonFor(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"documented non-streaming refusal", refusalMessageBody, "claude_stop_reason=refusal, category=cyber"},
		{"streamed message_delta", refusalMessageDelta, "claude_stop_reason=refusal, category=reasoning_extraction"},
		{"explicit null category", `{"stop_reason":"refusal","stop_details":{"type":"refusal","category":null,"explanation":null}}`, "claude_stop_reason=refusal, category=none"},
		{"stop_details null", `{"stop_reason":"refusal","stop_details":null}`, "claude_stop_reason=refusal"},
		{"upstream dropped stop_details", `{"type":"message_delta","delta":{"stop_reason":"refusal"}}`, "claude_stop_reason=refusal"},
		{"a category the vendor adds later", `{"stop_reason":"refusal","stop_details":{"category":"general_harms"}}`, "claude_stop_reason=refusal, category=general_harms"},
		{"not JSON", `not json`, "claude_stop_reason=refusal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, claudeRefusalRejectReasonFor(tc.raw))
		})
	}
}

func TestHandleClaudeResponseDataRecordsRefusalCategory(t *testing.T) {
	c, _ := refusalTestContext(t)
	info := refusalTestInfo("claude-fable-5", types.RelayFormatClaude)
	claudeInfo := &ClaudeResponseInfo{Usage: &dto.Usage{}}

	err := HandleClaudeResponseData(c, info, claudeInfo, nil, []byte(refusalMessageBody))
	require.Nil(t, err)
	assert.Equal(t, "claude_stop_reason=refusal, category=cyber", rejectReason(c))
	assert.Equal(t, 412, claudeInfo.Usage.PromptTokens, "usage is still what the upstream reported")
	assert.Equal(t, 0, claudeInfo.Usage.CompletionTokens)
}

func TestHandleStreamResponseDataRecordsRefusalCategory(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI} {
		t.Run(string(format), func(t *testing.T) {
			c, _ := refusalTestContext(t)
			info := refusalTestInfo("claude-opus-5-5", format)
			claudeInfo := &ClaudeResponseInfo{Usage: &dto.Usage{}}

			err := HandleStreamResponseData(c, info, claudeInfo, refusalMessageDelta)
			require.Nil(t, err)
			assert.Equal(t, "claude_stop_reason=refusal, category=reasoning_extraction", rejectReason(c))
		})
	}
}

func TestStreamedRefusalIsForwardedUnchanged(t *testing.T) {
	c, w := refusalTestContext(t)
	info := refusalTestInfo("claude-opus-5-5", types.RelayFormatClaude)
	claudeInfo := &ClaudeResponseInfo{Usage: &dto.Usage{}}

	require.Nil(t, HandleStreamResponseData(c, info, claudeInfo, refusalMessageDelta))
	assert.Contains(t, w.Body.String(), `"stop_details":{"type":"refusal","category":"reasoning_extraction","explanation":"declined"}`)
}

func TestRefusalCategoryIsNotErasedByALaterEvent(t *testing.T) {
	c, _ := refusalTestContext(t)

	maybeMarkClaudeRefusal(c, "refusal", refusalMessageDelta)
	maybeMarkClaudeRefusal(c, "refusal", `{"type":"message_delta","delta":{"stop_reason":"refusal"}}`)

	assert.Equal(t, "claude_stop_reason=refusal, category=reasoning_extraction", rejectReason(c))
}

func TestNoRejectReasonWithoutARefusal(t *testing.T) {
	c, _ := refusalTestContext(t)
	info := refusalTestInfo("claude-opus-5-5", types.RelayFormatClaude)
	claudeInfo := &ClaudeResponseInfo{Usage: &dto.Usage{}}

	body := `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","stop_details":null,"usage":{"input_tokens":3,"output_tokens":1}}`
	require.Nil(t, HandleClaudeResponseData(c, info, claudeInfo, nil, []byte(body)))
	assert.Equal(t, "", rejectReason(c))

	delta := `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null,"stop_details":null},"usage":{"output_tokens":1}}`
	require.Nil(t, HandleStreamResponseData(c, info, claudeInfo, delta))
	assert.Equal(t, "", rejectReason(c))
}
