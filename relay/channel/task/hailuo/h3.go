package hailuo

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

// https://platform.minimax.io/docs/api-reference/video-generation-v2-create
// H3 uses the V2 API; legacy Hailuo requests and stored task IDs keep using V1.
const h3TaskPrefix = "minimax-v2:"

func isH3Model(name string) bool { return name == "MiniMax-H3" || name == "MiniMax-H3-Max" }

type h3MediaURL struct {
	URL string `json:"url"`
}
type h3Content struct {
	Type     string      `json:"type"`
	Text     string      `json:"text,omitempty"`
	ImageURL *h3MediaURL `json:"image_url,omitempty"`
	VideoURL *h3MediaURL `json:"video_url,omitempty"`
	AudioURL *h3MediaURL `json:"audio_url,omitempty"`
	Role     string      `json:"role,omitempty"`
}
type h3Request struct {
	Model       string      `json:"model"`
	Content     []h3Content `json:"content"`
	Duration    *int        `json:"duration,omitempty"`
	Resolution  string      `json:"resolution,omitempty"`
	Ratio       string      `json:"ratio,omitempty"`
	CallbackURL string      `json:"callback_url,omitempty"`
}
type h3Response struct {
	Task *struct {
		Usage struct {
			InputSeconds    float64 `json:"input_seconds"`
			OutputSeconds   float64 `json:"output_seconds"`
			InputImageCount int     `json:"input_image_count"`
		} `json:"usage"`
		ID      string `json:"id"`
		Model   string `json:"model"`
		Status  string `json:"status"`
		Content struct {
			URL string `json:"url"`
		} `json:"content"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	} `json:"task"`
}

func convertH3Request(req *relaycommon.TaskSubmitReq, info *relaycommon.RelayInfo) (*h3Request, error) {
	name := req.Model
	if info.IsModelMapped {
		name = info.UpstreamModelName
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
	r := &h3Request{Model: name, Duration: &duration, Resolution: "768P"}
	if req.Size != "" {
		r.Resolution = strings.ToUpper(req.Size)
	}
	if err := taskcommon.UnmarshalMetadata(req.Metadata, r); err != nil {
		return nil, err
	}
	// Keep the routed model authoritative even when metadata contains a model field.
	r.Model = name
	if len(r.Content) == 0 {
		images := req.Images
		if len(images) == 0 && req.Image != "" {
			images = []string{req.Image}
		}
		if len(images) == 0 && req.InputReference != "" {
			images = []string{req.InputReference}
		}
		if len(images) > 2 {
			return nil, fmt.Errorf("use metadata.content with reference_image roles for more than two images")
		}
		for i, img := range images {
			role := "first_frame"
			if i == 1 {
				role = "last_frame"
			}
			r.Content = append(r.Content, h3Content{Type: "image_url", ImageURL: &h3MediaURL{URL: img}, Role: role})
		}
	}
	textCount, imageCount, videoCount, audioCount, firstCount, lastCount := 0, 0, 0, 0, 0, 0
	hasFrame, hasReference := false, false
	for _, item := range r.Content {
		switch item.Type {
		case "text":
			if strings.TrimSpace(item.Text) == "" {
				return nil, fmt.Errorf("H3 text cannot be empty")
			}
			textCount++
		case "image_url":
			if item.ImageURL == nil || item.ImageURL.URL == "" {
				return nil, fmt.Errorf("H3 image_url is required")
			}
			imageCount++
			switch item.Role {
			case "", "first_frame":
				hasFrame = true
				firstCount++
			case "last_frame":
				hasFrame = true
				lastCount++
			case "reference_image":
				hasReference = true
			default:
				return nil, fmt.Errorf("invalid H3 image role")
			}
		case "video_url":
			if item.VideoURL == nil || item.VideoURL.URL == "" || item.Role != "reference_video" {
				return nil, fmt.Errorf("H3 video requires a reference_video URL")
			}
			hasReference = true
			videoCount++
		case "audio_url":
			if item.AudioURL == nil || item.AudioURL.URL == "" || item.Role != "reference_audio" {
				return nil, fmt.Errorf("H3 audio requires a reference_audio URL")
			}
			hasReference = true
			audioCount++
		default:
			return nil, fmt.Errorf("unsupported H3 content type")
		}
	}
	if textCount == 0 && strings.TrimSpace(req.Prompt) != "" {
		r.Content = append(r.Content, h3Content{Type: "text", Text: req.Prompt})
		textCount++
	}
	if textCount != 1 {
		return nil, fmt.Errorf("H3 requires exactly one text prompt")
	}
	if hasFrame && hasReference {
		return nil, fmt.Errorf("H3 frame and reference inputs cannot be mixed")
	}
	if imageCount > 9 || videoCount > 3 || audioCount > 3 || firstCount > 1 || lastCount > 1 {
		return nil, fmt.Errorf("H3 media count exceeds provider limits")
	}
	minDuration := 4
	if name == "MiniMax-H3-Max" {
		minDuration = 5
		if hasReference {
			return nil, fmt.Errorf("MiniMax-H3-Max does not support reference inputs")
		}
		if r.Resolution != "480P" && r.Resolution != "768P" {
			return nil, fmt.Errorf("MiniMax-H3-Max resolution must be 480P or 768P")
		}
	} else if r.Resolution != "768P" && r.Resolution != "2K" {
		return nil, fmt.Errorf("MiniMax-H3 resolution must be 768P or 2K")
	}
	if r.Duration == nil || *r.Duration < minDuration || *r.Duration > 15 {
		return nil, fmt.Errorf("%s duration must be between %d and 15 seconds", name, minDuration)
	}
	if r.Ratio == "" {
		if hasFrame || hasReference {
			r.Ratio = "adaptive"
		} else {
			r.Ratio = "16:9"
		}
	}
	if !hasFrame && !hasReference && r.Ratio == "adaptive" {
		return nil, fmt.Errorf("H3 text-to-video requires a concrete ratio")
	}
	switch r.Ratio {
	case "adaptive", "21:9", "16:9", "4:3", "1:1", "3:4", "9:16":
	default:
		return nil, fmt.Errorf("invalid H3 ratio")
	}
	return r, nil
}

// EstimateBilling preserves V1 per-video pricing. H3 prices are per output second.
// https://platform.minimax.io/docs/guides/pricing-paygo#video
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	if !isH3Model(info.UpstreamModelName) {
		return nil
	}
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}
	r, err := convertH3Request(&req, info)
	if err != nil {
		return nil
	}
	ratio := 1.0 // base is 768P, $0.08/second for both H3 models
	if r.Resolution == "2K" {
		ratio = 0.13 / 0.08
	}
	if r.Resolution == "480P" {
		ratio = 0.05 / 0.08
	}
	seconds := float64(*r.Duration)
	images, videos := 0, 0
	for _, item := range r.Content {
		if item.Type == "image_url" {
			images++
		}
		if item.Type == "video_url" {
			videos++
		}
	}
	// Reserve the provider's maximum aggregate input video duration (15 seconds).
	// Actual usage replaces this reservation on completion.
	if videos > 0 {
		seconds += 15
	}
	if r.Model == "MiniMax-H3" && images > 5 {
		seconds += float64(images-5) * 0.04 / (0.08 * ratio)
	}
	return map[string]float64{"seconds": seconds, "resolution": ratio}
}

func parseH3Task(data []byte) (*relaycommon.TaskInfo, error) {
	var response h3Response
	if err := common.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	if response.Task == nil || response.Task.ID == "" {
		return nil, fmt.Errorf("missing H3 task")
	}
	task := response.Task
	result := &relaycommon.TaskInfo{}
	switch task.Status {
	case "queued":
		result.Status = model.TaskStatusQueued
		result.Progress = "10%"
	case "running":
		result.Status = model.TaskStatusInProgress
		result.Progress = "50%"
	case "succeeded":
		if task.Content.URL == "" {
			return nil, fmt.Errorf("H3 succeeded without a video URL")
		}
		result.Status = model.TaskStatusSuccess
		result.Progress = "100%"
		result.Url = task.Content.URL
	case "failed", "cancelled":
		result.Status = model.TaskStatusFailure
		result.Progress = "100%"
		result.Reason = task.Error.Message
	default:
		return nil, fmt.Errorf("unknown H3 task status %q", task.Status)
	}
	return result, nil
}

// AdjustBillingOnComplete settles measured H3 usage at the request-time price.
func (a *TaskAdaptor) AdjustBillingOnComplete(task *model.Task, result *relaycommon.TaskInfo) int {
	bc := task.PrivateData.BillingContext
	if bc == nil || bc.PriceUnit != "second" || !strings.HasPrefix(task.GetUpstreamTaskID(), h3TaskPrefix) || result.Status != model.TaskStatusSuccess {
		return 0
	}
	var response h3Response
	if common.Unmarshal(task.Data, &response) != nil || response.Task == nil {
		return 0
	}
	u := response.Task.Usage
	if u.OutputSeconds <= 0 || u.OutputSeconds > 15 || u.InputSeconds < 0 || u.InputSeconds > 15 || u.InputImageCount < 0 || u.InputImageCount > 9 {
		return 0
	}
	resolution := bc.OtherRatios["resolution"]
	if resolution <= 0 {
		return 0
	}
	units := (u.InputSeconds + u.OutputSeconds) * resolution
	if response.Task.Model == "MiniMax-H3" && u.InputImageCount > 5 {
		units += float64(u.InputImageCount-5) * 0.5
	}
	quota, err := common.QuotaFromFloatStrict(bc.ModelPrice * units * bc.GroupRatio * common.QuotaPerUnit)
	if err != nil {
		common.SysError(err.Error())
		return 0
	}
	return quota
}
