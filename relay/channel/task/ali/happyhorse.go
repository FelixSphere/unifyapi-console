package ali

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// HappyHorse uses resolution/ratio and input.media, not Wan's legacy size/img_url.
// https://help.aliyun.com/en/model-studio/happyhorse-text-to-video-api-reference
// https://help.aliyun.com/en/model-studio/happyhorse-reference-to-video-api-reference
type happyHorseRequest struct {
	Model string `json:"model"`
	Input struct {
		Prompt string          `json:"prompt,omitempty"`
		Media  []AliVideoMedia `json:"media,omitempty"`
	} `json:"input"`
	Parameters struct {
		Audio        *bool  `json:"audio,omitempty"`
		PromptExtend *bool  `json:"prompt_extend,omitempty"`
		Resolution   string `json:"resolution,omitempty"`
		Ratio        string `json:"ratio,omitempty"`
		Duration     *int   `json:"duration,omitempty"`
		Watermark    *bool  `json:"watermark,omitempty"`
		Seed         *int64 `json:"seed,omitempty"`
	} `json:"parameters"`
}

func convertHappyHorseRequest(info *relaycommon.RelayInfo, req relaycommon.TaskSubmitReq) (*happyHorseRequest, error) {
	r := &happyHorseRequest{Model: req.Model}
	if info.IsModelMapped {
		r.Model = info.UpstreamModelName
	}
	r.Input.Prompt = req.Prompt
	r.Parameters.Resolution = "1080P"
	if req.Size != "" {
		r.Parameters.Resolution = strings.ToUpper(req.Size)
	}
	duration := 5
	if req.Duration != 0 {
		duration = req.Duration
	} else if req.Seconds != "" {
		var err error
		duration, err = strconv.Atoi(req.Seconds)
		if err != nil {
			return nil, fmt.Errorf("invalid seconds: %w", err)
		}
	}
	r.Parameters.Duration = &duration
	if req.Metadata != nil {
		data, err := common.Marshal(req.Metadata)
		if err != nil {
			return nil, err
		}
		if err := common.Unmarshal(data, r); err != nil {
			return nil, err
		}
	}
	expectedModel := req.Model
	if info.IsModelMapped {
		expectedModel = info.UpstreamModelName
	}
	if r.Model != expectedModel {
		return nil, fmt.Errorf("can't change model with metadata")
	}
	if r.Parameters.Duration == nil || *r.Parameters.Duration < 3 || *r.Parameters.Duration > 15 {
		return nil, fmt.Errorf("HappyHorse duration must be between 3 and 15 seconds")
	}
	switch r.Parameters.Resolution {
	case "480P", "720P", "1080P":
	default:
		return nil, fmt.Errorf("HappyHorse resolution must be 480P, 720P or 1080P")
	}
	if r.Parameters.Seed != nil && (*r.Parameters.Seed < 0 || *r.Parameters.Seed > 2147483647) {
		return nil, fmt.Errorf("HappyHorse seed must be between 0 and 2147483647")
	}
	if len(r.Input.Media) == 0 {
		images := req.Images
		if len(images) == 0 {
			if img := firstTaskImage(req); img != "" {
				images = []string{img}
			}
		}
		role := "first_frame"
		if strings.HasSuffix(r.Model, "-r2v") {
			role = "reference_image"
		}
		for _, img := range images {
			r.Input.Media = append(r.Input.Media, AliVideoMedia{Type: role, URL: img})
		}
	}
	switch {
	case strings.HasSuffix(r.Model, "-t2v"):
		if strings.TrimSpace(r.Input.Prompt) == "" || len(r.Input.Media) != 0 {
			return nil, fmt.Errorf("HappyHorse t2v requires a prompt and no images")
		}
	case strings.HasSuffix(r.Model, "-i2v"):
		if len(r.Input.Media) != 1 || r.Input.Media[0].Type != "first_frame" {
			return nil, fmt.Errorf("HappyHorse i2v requires exactly one first_frame image")
		}
		if r.Parameters.Ratio != "" {
			return nil, fmt.Errorf("HappyHorse i2v does not accept ratio")
		}
	case strings.HasSuffix(r.Model, "-r2v"):
		if strings.TrimSpace(r.Input.Prompt) == "" || len(r.Input.Media) < 1 || len(r.Input.Media) > 9 {
			return nil, fmt.Errorf("HappyHorse r2v requires a prompt and 1 to 9 reference images")
		}
		for _, media := range r.Input.Media {
			if media.Type != "reference_image" {
				return nil, fmt.Errorf("HappyHorse r2v only accepts reference_image media")
			}
		}
	default:
		return nil, fmt.Errorf("unsupported HappyHorse model: %s", r.Model)
	}
	for _, media := range r.Input.Media {
		if strings.TrimSpace(media.URL) == "" {
			return nil, fmt.Errorf("HappyHorse image URL is required")
		}
	}
	switch r.Parameters.Ratio {
	case "", "16:9", "9:16", "1:1", "4:3", "3:4", "4:5", "5:4", "9:21", "21:9":
	default:
		return nil, fmt.Errorf("unsupported HappyHorse ratio")
	}
	return r, nil
}
