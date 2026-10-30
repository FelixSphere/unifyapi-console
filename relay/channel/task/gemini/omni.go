package gemini

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

const omniModel = "gemini-omni-1.1-flash"

// Omni uses Interactions, not Veo's predictLongRunning protocol.
// https://ai.google.dev/api/interactions-api
func omniRequest(req relaycommon.TaskSubmitReq) ([]byte, error) {
	body := map[string]any{"model": omniModel, "input": req.Prompt}
	if req.Metadata != nil {
		for _, key := range []string{"input", "previous_interaction_id", "generation_config", "response_format"} {
			if value, ok := req.Metadata[key]; ok {
				body[key] = value
			}
		}
	}
	if _, ok := body["response_format"]; !ok {
		body["response_format"] = map[string]any{"type": "video", "delivery": "uri", "aspect_ratio": SizeToVeoAspectRatio(req.Size)}
	}
	format, ok := body["response_format"].(map[string]any)
	if !ok || format["type"] != "video" {
		return nil, fmt.Errorf("Omni requires a video response_format")
	}
	if _, ok := req.Metadata["input"]; !ok && len(req.Images) > 0 {
		content := []map[string]any{{"type": "text", "text": req.Prompt}}
		for _, image := range req.Images {
			content = append(content, map[string]any{"type": "image", "uri": image})
		}
		body["input"] = content
	}
	// Polling requires a stored background interaction. Clients cannot disable it.
	body["background"], body["store"], body["stream"] = true, true, false
	return common.Marshal(body)
}

type omniResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  struct {
		Message string `json:"message"`
	} `json:"error"`
	Steps []struct {
		Type    string `json:"type"`
		Content []struct {
			Type     string `json:"type"`
			URI      string `json:"uri"`
			Data     string `json:"data"`
			MimeType string `json:"mime_type"`
		} `json:"content"`
	} `json:"steps"`
}

func parseOmniResponse(data []byte) (*relaycommon.TaskInfo, bool, error) {
	var r omniResponse
	if err := common.Unmarshal(data, &r); err != nil {
		return nil, false, err
	}
	if r.ID == "" || r.Status == "" {
		return nil, false, nil
	}
	result := &relaycommon.TaskInfo{}
	switch r.Status {
	case "in_progress", "requires_action":
		result.Status = model.TaskStatusInProgress
	case "completed":
		for _, step := range r.Steps {
			if step.Type != "model_output" {
				continue
			}
			for _, content := range step.Content {
				if content.Type != "video" {
					continue
				}
				if content.URI != "" {
					result.RemoteUrl = content.URI
				} else if content.Data != "" {
					mime := content.MimeType
					if mime == "" {
						mime = "video/mp4"
					}
					result.Url = "data:" + mime + ";base64," + content.Data
				}
			}
		}
		if result.RemoteUrl == "" && result.Url == "" {
			return nil, true, fmt.Errorf("completed Omni interaction has no video")
		}
		result.Status = model.TaskStatusSuccess
	case "failed", "cancelled":
		result.Status = model.TaskStatusFailure
		result.Reason = r.Error.Message
	default:
		return nil, true, fmt.Errorf("unknown Omni status: %s", r.Status)
	}
	return result, true, nil
}

func isOmniInteraction(name string) bool { return strings.HasPrefix(name, "interactions/") }
