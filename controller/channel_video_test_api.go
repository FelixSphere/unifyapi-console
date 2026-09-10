package controller

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// SubmitChannelVideoTest is explicitly invoked by the operator (never by the
// automatic health check). It uses the operator account without creating a key.
func SubmitChannelVideoTest(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	ch, err := model.GetChannelById(id, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var req relaycommon.TaskSubmitReq
	if err = c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	req.Model = strings.TrimSpace(req.Model)
	if req.Model == "" {
		common.ApiError(c, errors.New("model is required"))
		return
	}
	if err = validateSynchronousChannelTest(ch, req.Model); !errors.Is(err, errVideoChannelTestUnsupported) {
		common.ApiError(c, errors.New("select a video model for this test"))
		return
	}
	if err = applyVideoTestDefaults(ch, &req); err != nil {
		common.ApiError(c, err)
		return
	}
	userID, err := resolveChannelTestUserID(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	cache, err := model.GetUserCache(userID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	cache.WriteContext(c)
	c.Set("id", userID)
	group, err := model.GetUserGroup(userID, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.Set("group", group)
	c.Set("specific_channel_id", id) // no fallback or retry onto another channel
	if apiErr := middleware.SetupContextForSelectedChannel(c, ch, req.Model); apiErr != nil {
		common.ApiError(c, apiErr)
		return
	}
	body, err := common.Marshal(req)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	originalRequest := c.Request
	c.Request = originalRequest.Clone(originalRequest.Context())
	c.Request.URL.Path = "/v1/videos"
	c.Request.URL.RawPath = ""
	c.Request.URL.RawQuery = ""
	c.Request.RequestURI = "/v1/videos"
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	c.Request.ContentLength = int64(len(body))
	c.Request.Header.Set("Content-Type", "application/json")
	defer func() { common.CleanupBodyStorage(c); c.Request = originalRequest }()
	info, err := relaycommon.GenRelayInfo(c, types.RelayFormatTask, nil, nil)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	info.IsPlayground = true // session auth: charge account, no API token quota
	info.LockedChannel = ch
	relayTaskWithInfo(c, info)
}

func applyVideoTestDefaults(ch *model.Channel, req *relaycommon.TaskSubmitReq) error {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("model_mapping", ch.GetModelMapping())
	info := &relaycommon.RelayInfo{OriginModelName: req.Model, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: req.Model}}
	if err := helper.ModelMappedHelper(c, info, nil); err != nil {
		return err
	}
	name := strings.ToLower(info.UpstreamModelName)
	if req.Prompt == "" {
		req.Prompt = "A red ball slowly rolling across a wooden table, static camera."
	}
	duration, size := 5, "720p"
	switch {
	case strings.HasPrefix(name, "minimax-h3"):
		size = "768P"
	case strings.HasPrefix(name, "minimax-"), strings.HasPrefix(name, "t2v-"), strings.HasPrefix(name, "i2v-"), strings.HasPrefix(name, "s2v-"):
		duration, size = 6, "768P"
	case strings.HasPrefix(name, "wan"), strings.HasPrefix(name, "happyhorse-"):
		size = "720P"
	case strings.HasPrefix(name, "veo-"), strings.HasPrefix(name, "gemini-omni"):
		duration = 8
	case strings.HasPrefix(name, "sora-"):
		duration, size = 4, "1280x720"
	}
	if req.Duration == 0 && req.Seconds == "" {
		req.Duration = duration
	}
	if req.Size == "" {
		req.Size = size
	}
	return nil
}

// Poll the durable task, scoped to both the authenticated operator and channel.
// The normal task worker owns upstream polling and billing/refunds.
func FetchChannelVideoTest(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	userID, err := resolveChannelTestUserID(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	task, exists, err := model.GetByTaskId(userID, c.Param("task_id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !exists || task.ChannelId != id {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "video test task not found"})
		return
	}
	c.JSON(http.StatusOK, videoTestStatus(task))
}

func videoTestStatus(task *model.Task) gin.H {
	end := task.FinishTime
	if end == 0 {
		end = time.Now().Unix()
	}
	elapsed := max(int64(0), end-task.SubmitTime)
	message := task.FailReason
	if task.Status != model.TaskStatusSuccess && task.Status != model.TaskStatusFailure {
		message = fmt.Sprintf("Video task %s is %s (%s)", task.TaskID, task.Status, task.Progress)
	}
	status := task.Status.ToVideoStatus()
	if task.Status == model.TaskStatusNotStart {
		status = "queued"
	}
	return gin.H{"success": true, "task_id": task.TaskID, "status": status, "message": message, "time": elapsed}
}
