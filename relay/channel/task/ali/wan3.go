package ali

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// https://help.aliyun.com/en/model-studio/wan3-video-generation-api-reference
func convertWan3Request(info *relaycommon.RelayInfo, req relaycommon.TaskSubmitReq) (*happyHorseRequest, error) {
	r := &happyHorseRequest{Model: req.Model}
	if info.IsModelMapped {
		r.Model = info.UpstreamModelName
	}
	name := r.Model
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
			return nil, err
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
	if r.Model != name {
		return nil, fmt.Errorf("can't change model with metadata")
	}
	if r.Parameters.Duration == nil || (*r.Parameters.Duration != -1 && (*r.Parameters.Duration < 2 || *r.Parameters.Duration > 30)) {
		return nil, fmt.Errorf("Wan3 duration must be -1 or 2 to 30 seconds")
	}
	if len(r.Input.Media) == 0 {
		images := req.Images
		if len(images) == 0 {
			if image := firstTaskImage(req); image != "" {
				images = []string{image}
			}
		}
		for i, image := range images {
			role := "reference_image"
			if len(images) <= 2 {
				role = "first_frame"
				if i == 1 {
					role = "last_frame"
				}
			}
			r.Input.Media = append(r.Input.Media, AliVideoMedia{Type: role, URL: image})
		}
	}
	if strings.TrimSpace(r.Input.Prompt) == "" && len(r.Input.Media) == 0 {
		return nil, fmt.Errorf("Wan3 requires a prompt or media")
	}
	counts := map[string]int{}
	for _, media := range r.Input.Media {
		if strings.TrimSpace(media.URL) == "" {
			return nil, fmt.Errorf("Wan3 media URL is required")
		}
		counts[media.Type]++
		limit := 0
		switch media.Type {
		case "first_frame", "last_frame", "file", "link":
			limit = 1
		case "reference_image":
			limit = 10
		case "reference_video", "reference_audio":
			limit = 5
		default:
			return nil, fmt.Errorf("unsupported Wan3 media type")
		}
		if counts[media.Type] > limit {
			return nil, fmt.Errorf("too many Wan3 %s inputs", media.Type)
		}
	}
	if counts["last_frame"] > 0 && counts["first_frame"] == 0 {
		return nil, fmt.Errorf("Wan3 last frame requires first frame")
	}
	if counts["first_frame"]+counts["last_frame"] > 0 && len(r.Input.Media) != counts["first_frame"]+counts["last_frame"] {
		return nil, fmt.Errorf("Wan3 frames and references cannot be mixed")
	}
	if counts["file"] > 0 && counts["link"] > 0 {
		return nil, fmt.Errorf("Wan3 file and link inputs cannot be mixed")
	}
	switch r.Parameters.Resolution {
	case "480P", "720P", "1080P":
	default:
		return nil, fmt.Errorf("unsupported Wan3 resolution")
	}
	switch r.Parameters.Ratio {
	case "", "adaptive", "16:9", "4:3", "1:1", "3:4", "9:16":
	default:
		return nil, fmt.Errorf("unsupported Wan3 ratio")
	}
	if r.Parameters.Seed != nil && (*r.Parameters.Seed < -1 || *r.Parameters.Seed > 2147483647) {
		return nil, fmt.Errorf("invalid Wan3 seed")
	}
	return r, nil
}

// Settle auto-duration and video-reference requests from measured input + output.
func (a *TaskAdaptor) AdjustBillingOnComplete(task *model.Task, result *relaycommon.TaskInfo) int {
	bc := task.PrivateData.BillingContext
	if bc == nil || bc.PriceUnit != "second" || !strings.HasPrefix(task.Properties.UpstreamModelName, "wan3.0-video") || result.Status != model.TaskStatusSuccess {
		return 0
	}
	var response AliVideoResponse
	if common.Unmarshal(task.Data, &response) != nil || response.Usage == nil {
		return 0
	}
	u := response.Usage
	seconds := u.InputVideoDuration + u.OutputVideoDuration
	if u.InputVideoDuration < 0 || u.OutputVideoDuration <= 0 || seconds > 30 {
		return 0
	}
	ratio := bc.OtherRatios["resolution"]
	if ratio <= 0 {
		return 0
	}
	quota, err := common.QuotaFromFloatStrict(bc.ModelPrice * seconds * ratio * bc.GroupRatio * common.QuotaPerUnit)
	if err != nil {
		return 0
	}
	return quota
}
