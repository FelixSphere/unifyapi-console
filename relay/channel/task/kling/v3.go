package kling

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

const v3TaskPrefix = "kling-v3:"

func isV3(name string) bool {
	return name == "kling-3.0" || name == "kling-3.0-turbo" || name == "kling-3.0-omni"
}

type v3Content struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	URL       string `json:"url,omitempty"`
	ID        string `json:"id,omitempty"`
	ElementID string `json:"element_id,omitempty"`
}
type v3Request struct {
	Prompt   string      `json:"prompt,omitempty"`
	Contents []v3Content `json:"contents,omitempty"`
	Settings struct {
		Duration    int    `json:"duration,omitempty"`
		Resolution  string `json:"resolution,omitempty"`
		AspectRatio string `json:"aspect_ratio,omitempty"`
		Audio       string `json:"audio,omitempty"`
		MultiShot   *bool  `json:"multi_shot,omitempty"`
	} `json:"settings"`
	Options map[string]any `json:"options,omitempty"`
}

// https://kling.ai/document-api/api/video/3-0-omni
func makeV3Request(req relaycommon.TaskSubmitReq, info *relaycommon.RelayInfo) ([]byte, error) {
	var r v3Request
	r.Settings.Duration = 5
	r.Settings.Resolution = "720p"
	if req.Duration != 0 {
		r.Settings.Duration = req.Duration
	} else if req.Seconds != "" {
		seconds, err := strconv.Atoi(req.Seconds)
		if err != nil {
			return nil, err
		}
		r.Settings.Duration = seconds
	}
	if req.Size != "" {
		r.Settings.Resolution = strings.ToLower(req.Size)
	}
	if req.Metadata != nil {
		data, err := common.Marshal(req.Metadata)
		if err != nil {
			return nil, err
		}
		if err := common.Unmarshal(data, &r); err != nil {
			return nil, err
		}
	}
	if r.Settings.Duration < 3 || r.Settings.Duration > 15 {
		return nil, fmt.Errorf("Kling 3 duration must be 3 to 15 seconds")
	}
	if r.Settings.Resolution != "720p" && r.Settings.Resolution != "1080p" && (r.Settings.Resolution != "4k" || info.UpstreamModelName == "kling-3.0-turbo") {
		return nil, fmt.Errorf("unsupported Kling resolution")
	}
	if len(r.Contents) == 0 {
		images := req.Images
		if len(images) == 0 && req.Image != "" {
			images = []string{req.Image}
		}
		if len(images) == 0 && info.UpstreamModelName != "kling-3.0-omni" {
			r.Prompt = req.Prompt
		} else {
			r.Contents = append(r.Contents, v3Content{Type: "prompt", Text: req.Prompt})
			for i, image := range images {
				role := "first_frame"
				if i == 1 {
					role = "last_frame"
				}
				if len(images) > 2 {
					role = "refer_image"
				}
				r.Contents = append(r.Contents, v3Content{Type: role, URL: image})
			}
		}
	}
	info.Action = constant.TaskActionTextGenerate
	for _, content := range r.Contents {
		if content.Type != "prompt" {
			info.Action = constant.TaskActionGenerate
		}
		if info.UpstreamModelName == "kling-3.0-turbo" && content.Type != "prompt" && content.Type != "first_frame" {
			return nil, fmt.Errorf("Kling Turbo supports only a first frame")
		}
	}
	return common.Marshal(r)
}

type v3Task struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Outputs []struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"outputs"`
}

func parseV3Response(data []byte) (*relaycommon.TaskInfo, bool, error) {
	var envelope struct {
		Code    int      `json:"code"`
		Message string   `json:"message"`
		Data    []v3Task `json:"data"`
	}
	if err := common.Unmarshal(data, &envelope); err != nil {
		return nil, false, nil
	}
	if len(envelope.Data) == 0 {
		return nil, false, nil
	}
	task := envelope.Data[0]
	if envelope.Code != 0 {
		return nil, true, fmt.Errorf("Kling: %s", envelope.Message)
	}
	result := &relaycommon.TaskInfo{TaskID: v3TaskPrefix + task.ID, Reason: task.Message}
	switch task.Status {
	case "submitted":
		result.Status = model.TaskStatusSubmitted
	case "processing":
		result.Status = model.TaskStatusInProgress
	case "failed":
		result.Status = model.TaskStatusFailure
	case "succeeded":
		for _, output := range task.Outputs {
			if output.Type == "video" && output.URL != "" {
				result.Url = output.URL
				break
			}
		}
		if result.Url == "" {
			return nil, true, fmt.Errorf("Kling task succeeded without video")
		}
		result.Status = model.TaskStatusSuccess
	default:
		return nil, true, fmt.Errorf("unknown Kling task status %s", task.Status)
	}
	return result, true, nil
}
