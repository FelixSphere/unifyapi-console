package openrouter

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/task/sora"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// OpenRouter uses HTTP 202 on submit and a string error in its job response.
// https://openrouter.ai/docs/api/api-reference/video-generation/create-videos
// Embedding keeps standard task settlement hooks; no upstream cost is billed as
// customer quota, since usage.cost belongs to the upstream account.
type TaskAdaptor struct {
	sora.TaskAdaptor
	baseURL string
	key     string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.TaskAdaptor.Init(info)
	a.baseURL = strings.TrimRight(info.ChannelBaseUrl, "/")
	a.key = info.ApiKey
}
func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *taskdto.TaskError {
	return relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate)
}
func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	return a.baseURL + "/v1/videos", nil
}
func (a *TaskAdaptor) BuildRequestHeader(c *gin.Context, r *http.Request, info *relaycommon.RelayInfo) error {
	r.Header.Set("Authorization", "Bearer "+a.key)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")
	return nil
}
func payload(c *gin.Context, info *relaycommon.RelayInfo) (map[string]any, error) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"prompt": req.Prompt, "model": info.UpstreamModelName}
	duration := req.Duration
	if duration == 0 && req.Seconds != "" {
		duration, err = strconv.Atoi(req.Seconds)
		if err != nil {
			return nil, fmt.Errorf("invalid seconds")
		}
	}
	if duration > 0 {
		body["duration"] = duration
	}
	if req.Size != "" {
		body["size"] = req.Size
	}

	images := req.Images
	if len(images) == 0 && req.InputReference != "" {
		images = []string{req.InputReference}
	}
	if len(images) > 0 {
		frames := []map[string]any{}
		for i, img := range images {
			if i > 1 {
				return nil, fmt.Errorf("use metadata.input_references for more than two reference images")
			}
			frame := "first_frame"
			if i == 1 {
				frame = "last_frame"
			}
			frames = append(frames, map[string]any{"frame_type": frame, "type": "image_url", "image_url": map[string]string{"url": img}})
		}
		body["frame_images"] = frames
	}
	// Accept native OpenRouter JSON fields as well as gateway metadata.
	native := map[string]any{}
	if c.Request != nil {
		if storage, e := common.GetBodyStorage(c); e == nil {
			if raw, e := storage.Bytes(); e == nil {
				_ = common.Unmarshal(raw, &native)
			}
		}
	}
	for _, key := range []string{"aspect_ratio", "resolution", "size", "duration", "frame_images", "input_references", "generate_audio", "seed", "provider", "callback_url"} {
		if value, ok := native[key]; ok {
			body[key] = value
		}
		if value, ok := req.Metadata[key]; ok {
			body[key] = value
		}
	}
	if _, ok := body["duration"]; !ok {
		if d, _, known := TestDefaults(info.UpstreamModelName); known && d > 0 {
			body["duration"] = d
		}
	}
	// Normalize after merging the raw body and metadata: otherwise raw size
	// reintroduces shorthand (768p/2K) after it was converted to resolution.
	if value, ok := body["size"]; ok {
		if value == nil {
			delete(body, "size")
		} else {
			size, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("size must be a resolution label or WIDTHxHEIGHT")
			}
			size = strings.ToLower(strings.TrimSpace(size))
			if size == "" {
				delete(body, "size")
			} else if resolutionLabel.MatchString(size) {
				if _, explicit := body["resolution"]; !explicit {
					body["resolution"] = size
				}
				delete(body, "size")
			} else if pixelDimensions.MatchString(size) {
				body["size"] = size
			} else {
				return nil, fmt.Errorf("invalid size: expected a resolution label or WIDTHxHEIGHT")
			}
		}
	}
	if r, ok := body["resolution"].(string); ok {
		r = strings.ToLower(r)
		if strings.HasSuffix(r, "k") {
			r = strings.ToUpper(r)
		}
		body["resolution"] = r
	}
	// Parse effective metadata duration once so wire parameters and billing agree.
	if value, ok := body["duration"]; ok {
		n, err := strconv.Atoi(fmt.Sprint(value))
		if err != nil || n < 1 || n > relaycommon.MaxTaskDurationSeconds {
			return nil, fmt.Errorf("invalid video duration")
		}
		body["duration"] = n
	}
	return body, nil
}
func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	p, err := payload(c, info)
	if err != nil {
		return nil, err
	}
	data, err := common.Marshal(p)
	return bytes.NewReader(data), err
}
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	p, err := payload(c, info)
	if err != nil {
		return nil
	}
	if n, ok := p["duration"].(int); ok {
		return map[string]float64{"seconds": float64(n)}
	}
	return nil
}
func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, body io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, body)
}
func (a *TaskAdaptor) FetchTask(base, key string, body map[string]any, proxy string) (*http.Response, error) {
	id, ok := body["task_id"].(string)
	if !ok || id == "" {
		return nil, fmt.Errorf("missing task ID")
	}
	return a.TaskAdaptor.FetchTask(strings.TrimRight(base, "/"), key, map[string]any{"task_id": url.PathEscape(id)}, proxy)
}
func (a *TaskAdaptor) ParseTaskResult(data []byte) (*relaycommon.TaskInfo, error) {
	var job struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if err := common.Unmarshal(data, &job); err != nil {
		return nil, err
	}
	result := &relaycommon.TaskInfo{TaskID: job.ID}
	switch job.Status {
	case "pending", "queued":
		result.Status = model.TaskStatusQueued
	case "processing", "in_progress":
		result.Status = model.TaskStatusInProgress
	case "completed":
		result.Status = model.TaskStatusSuccess
	case "failed", "cancelled", "expired":
		result.Status = model.TaskStatusFailure
		result.Reason = job.Error
	default:
		return nil, fmt.Errorf("unknown OpenRouter video status %q", job.Status)
	}
	return result, nil
}
func (a *TaskAdaptor) ConvertToOpenAIVideo(task *model.Task) ([]byte, error) {
	video := task.ToOpenAIVideo()
	if task.Status == model.TaskStatusFailure {
		video.Error = &dto.OpenAIVideoError{Message: task.FailReason}
	}
	return common.Marshal(video)
}
func (a *TaskAdaptor) GetModelList() []string {
	models := make([]string, 0, len(defaults))
	for name := range defaults {
		models = append(models, name)
	}
	sort.Strings(models)
	return models
}
func (a *TaskAdaptor) GetChannelName() string { return "openrouter" }

func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (string, []byte, *taskdto.TaskError) {
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, service.TaskErrorWrapper(err, "read_response_failed", http.StatusBadGateway)
	}
	var job struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if err = common.Unmarshal(data, &job); err != nil {
		return "", nil, service.TaskErrorWrapper(fmt.Errorf("invalid OpenRouter video JSON response"), "invalid_response", http.StatusBadGateway)
	}
	if job.ID == "" || job.Status == "failed" || job.Status == "cancelled" || job.Status == "expired" {
		return "", nil, service.TaskErrorWrapper(fmt.Errorf("OpenRouter did not accept video task: %s", job.Error), "invalid_response", http.StatusBadGateway)
	}
	video := dto.NewOpenAIVideo()
	video.ID = info.PublicTaskID
	video.TaskID = info.PublicTaskID
	video.Model = info.OriginModelName
	c.JSON(http.StatusOK, video)
	return job.ID, data, nil
}

var resolutionLabel = regexp.MustCompile(`^[1-9][0-9]*[pk]$`)
var pixelDimensions = regexp.MustCompile(`^[1-9][0-9]*x[1-9][0-9]*$`)
