package hailuo

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel/task/sora"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// Flatkey documents H3 native fields over the OpenAI video task lifecycle:
// https://flatkey.ai/models/minimax-h3
// Persist the protocol so native MiniMax tasks keep their original poll path.
const flatkeyTaskPrefix = "flatkey-video:"

func isFlatkeyBaseURL(base string) bool {
	u, err := url.Parse(base)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") != "" {
		return false
	}
	return u.Scheme == "https" && (strings.EqualFold(u.Host, "router.flatkey.ai") || strings.EqualFold(u.Host, "console.flatkey.ai"))
}

func buildFlatkeyBody(body *h3Request, req *relaycommon.TaskSubmitReq) (io.Reader, error) {
	// Flatkey adds this route-specific field; never send it to MiniMax native API.
	var options struct {
		AIGCWatermark *bool `json:"aigc_watermark,omitempty"`
	}
	if err := req.UnmarshalMetadata(&options); err != nil {
		return nil, err
	}
	payload := struct {
		*h3Request
		AIGCWatermark *bool `json:"aigc_watermark,omitempty"`
	}{body, options.AIGCWatermark}
	data, err := common.Marshal(payload)
	return bytes.NewReader(data), err
}

func flatkeySubmitResponse(c *gin.Context, data []byte, info *relaycommon.RelayInfo) (string, []byte, *taskdto.TaskError) {
	var response struct {
		ID     string                `json:"id"`
		TaskID string                `json:"task_id"`
		Error  *dto.OpenAIVideoError `json:"error"`
	}
	if err := common.Unmarshal(data, &response); err != nil {
		return "", nil, service.TaskErrorWrapper(fmt.Errorf("Flatkey returned a non-JSON video response; check the channel Base URL and video endpoint"), "invalid_response", http.StatusBadGateway)
	}
	if response.Error != nil {
		return "", nil, service.TaskErrorWrapper(fmt.Errorf("Flatkey: %s", response.Error.Message), "upstream_error", http.StatusBadGateway)
	}
	id := response.ID
	if id == "" {
		id = response.TaskID
	}
	if id == "" {
		return "", nil, service.TaskErrorWrapper(fmt.Errorf("Flatkey video response has no task ID"), "invalid_response", http.StatusBadGateway)
	}
	ov := dto.NewOpenAIVideo()
	ov.ID, ov.TaskID, ov.Model = info.PublicTaskID, info.PublicTaskID, info.OriginModelName
	c.JSON(http.StatusOK, ov)
	return flatkeyTaskPrefix + id, data, nil
}

func parseFlatkeyTask(data []byte) (*relaycommon.TaskInfo, bool, error) {
	var response struct {
		ID     string `json:"id"`
		TaskID string `json:"task_id"`
		Status string `json:"status"`
	}
	if err := common.Unmarshal(data, &response); err != nil {
		return nil, false, err
	}
	// Native MiniMax uses title-case statuses (or the V2 task envelope).
	if response.ID == "" && response.TaskID == "" {
		return nil, false, nil
	}
	switch response.Status {
	case "queued", "pending", "processing", "in_progress", "completed", "failed", "cancelled":
		result, err := (&sora.TaskAdaptor{}).ParseTaskResult(data)
		return result, true, err
	default:
		return nil, false, nil
	}
}

// FlatkeyContentURL is used by the authenticated video proxy. Never expose the
// upstream bearer token or a private upstream content URL to clients.
func FlatkeyContentURL(base, taskID string) (string, bool) {
	if !strings.HasPrefix(taskID, flatkeyTaskPrefix) {
		return "", false
	}
	return strings.TrimRight(base, "/") + "/v1/videos/" + url.PathEscape(strings.TrimPrefix(taskID, flatkeyTaskPrefix)) + "/content", true
}
