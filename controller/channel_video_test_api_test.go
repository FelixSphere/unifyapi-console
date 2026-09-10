package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestChannelVideoGenerationPersistsBillsPollsAndRefunds(t *testing.T) {
	for _, flatkey := range []bool{false, true} {
		t.Run(fmt.Sprintf("flatkey=%t", flatkey), func(t *testing.T) {
			initModelListColumnNames(t)
			db := setupCreditPoolControllerDB(t)
			require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Task{}, &model.Log{}, &model.CreditLot{}))
			savedCache, savedBatch := common.MemoryCacheEnabled, common.BatchUpdateEnabled
			common.MemoryCacheEnabled, common.BatchUpdateEnabled = false, false
			savedLimit := constant.TaskQueryLimit
			constant.TaskQueryLimit = 100
			t.Cleanup(func() { constant.TaskQueryLimit = savedLimit })
			savedPoller := service.GetTaskAdaptorFunc
			service.GetTaskAdaptorFunc = func(p constant.TaskPlatform) service.TaskPollingAdaptor { return relay.GetTaskAdaptor(p) }
			t.Cleanup(func() {
				common.MemoryCacheEnabled, common.BatchUpdateEnabled = savedCache, savedBatch
				service.GetTaskAdaptorFunc = savedPoller
			})
			service.InitHttpClient()
			state := "succeeded"
			posts, polls, downloads := 0, 0, 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "Bearer fixture-key", r.Header.Get("Authorization"))
				w.Header().Set("Content-Type", "application/json")
				if flatkey && strings.HasSuffix(r.URL.Path, "/content") {
					require.Equal(t, http.MethodGet, r.Method)
					require.Equal(t, "/v1/videos/generation-1/content", r.URL.Path)
					downloads++
					w.Header().Set("Content-Type", "video/mp4")
					w.Write([]byte("fixture-video-bytes"))
					return
				}
				switch r.Method + " " + r.URL.Path {
				case "POST /v2/video_generation", "POST /v1/videos":
					if flatkey {
						require.Equal(t, "/v1/videos", r.URL.Path)
					} else {
						require.Equal(t, "/v2/video_generation", r.URL.Path)
					}
					posts++
					var payload map[string]any
					require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
					require.Equal(t, "MiniMax-H3", payload["model"])
					require.Equal(t, "768P", payload["resolution"])
					require.EqualValues(t, 5, payload["duration"])
					require.NotEmpty(t, payload["content"])
					if flatkey {
						fmt.Fprintf(w, `{"id":"generation-%d","status":"queued"}`, posts)
					} else {
						fmt.Fprintf(w, `{"task_id":"generation-%d"}`, posts)
					}
				default:
					require.Equal(t, http.MethodGet, r.Method)
					if flatkey {
						require.Contains(t, r.URL.Path, "/v1/videos/generation-")
					} else {
						require.Contains(t, r.URL.Path, "/v2/query/video_generation/generation-")
					}
					polls++
					if flatkey {
						status := state
						if status == "succeeded" {
							status = "completed"
						}
						fmt.Fprintf(w, `{"id":"generation-%d","status":"%s","error":{"message":"fixture rejection"}}`, posts, status)
						return
					}
					fmt.Fprintf(w, `{"task":{"id":"generation-%d","model":"MiniMax-H3","status":"%s","content":{"url":"https://example.com/result.mp4"},"usage":{"output_seconds":5},"error":{"message":"fixture rejection"}}}`, posts, state)
				}
			}))
			defer upstream.Close()
			baseURL := upstream.URL
			if flatkey {
				baseURL = "https://router.flatkey.ai"
				fetch := system_setting.GetFetchSetting()
				savedProtection := fetch.EnableSSRFProtection
				fetch.EnableSSRFProtection = false
				t.Cleanup(func() { fetch.EnableSSRFProtection = savedProtection })
				client := service.GetHttpClient()
				previous := client.Transport
				client.Transport = flatkeyFixtureTransport{target: upstream.URL}
				t.Cleanup(func() { client.Transport = previous })
			}
			user := model.User{Id: 91234, Username: "video-test-operator", Status: 1, Role: 100, Group: "default", Quota: 10000000, Setting: `{"billing_preference":"wallet_only"}`}
			require.NoError(t, db.Create(&user).Error)
			ch := model.Channel{Id: 91234, Type: constant.ChannelTypeMiniMax, Key: "fixture-key", BaseURL: &baseURL, Models: "MiniMax-H3", Group: "default", Status: common.ChannelStatusManuallyDisabled}
			require.NoError(t, db.Create(&ch).Error)
			submit := func() string {
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(ch.Id)}}
				c.Set("id", user.Id)
				c.Request = httptest.NewRequest(http.MethodPost, "/api/channel/test/91234/video", bytes.NewBufferString(`{"model":"MiniMax-H3"}`))
				c.Request.Header.Set("Content-Type", "application/json")
				SubmitChannelVideoTest(c)
				require.Equal(t, http.StatusOK, w.Code, w.Body.String())
				var response struct {
					ID string `json:"id"`
				}
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
				require.NotEmpty(t, response.ID, w.Body.String())
				return response.ID
			}
			taskID := submit()
			task, exists, err := model.GetByTaskId(user.Id, taskID)
			require.NoError(t, err)
			require.True(t, exists)
			require.Equal(t, ch.Id, task.ChannelId)
			require.Equal(t, 0, task.PrivateData.TokenId)
			require.Equal(t, 200000, task.Quota) // 5 seconds * $0.08 * 500000
			require.Equal(t, "queued", videoTestStatus(task)["status"])
			service.RunTaskPollingOnce(context.Background(), nil)
			task, _, err = model.GetByTaskId(user.Id, taskID)
			require.NoError(t, err)
			require.Equal(t, model.TaskStatus(model.TaskStatusSuccess), task.Status)
			require.Equal(t, "completed", videoTestStatus(task)["status"])
			if flatkey {
				response := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(response)
				c.Set("id", user.Id)
				c.Params = gin.Params{{Key: "task_id", Value: taskID}}
				c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/"+taskID+"/content", nil)
				VideoProxy(c)
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				require.Equal(t, "fixture-video-bytes", response.Body.String())
				require.Equal(t, 1, downloads)
			}

			require.NoError(t, db.First(&user, user.Id).Error)
			require.Equal(t, 9800000, user.Quota)
			// Same path preserves failure refunds and does not enable a disabled channel.
			state = "failed"
			taskID = submit()
			service.RunTaskPollingOnce(context.Background(), nil)
			task, _, err = model.GetByTaskId(user.Id, taskID)
			require.NoError(t, err)
			require.Equal(t, model.TaskStatus(model.TaskStatusFailure), task.Status)
			require.Equal(t, "fixture rejection", videoTestStatus(task)["message"])
			require.NoError(t, db.First(&user, user.Id).Error)
			require.Equal(t, 9800000, user.Quota)
			require.NoError(t, db.First(&ch, ch.Id).Error)
			require.Equal(t, common.ChannelStatusManuallyDisabled, ch.Status)
			require.Equal(t, 2, posts)
			require.Equal(t, 2, polls)
			// Status cannot be read by another operator or through another channel ID.
			for _, ids := range [][2]int{{user.Id + 1, ch.Id}, {user.Id, ch.Id + 1}} {
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Set("id", ids[0])
				c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(ids[1])}, {Key: "task_id", Value: taskID}}
				FetchChannelVideoTest(c)
				require.Equal(t, http.StatusNotFound, w.Code)
			}
		})
	}
}

func TestChannelVideoSubmissionFailureKeepsChannelEnabled(t *testing.T) {
	initModelListColumnNames(t)
	db := setupCreditPoolControllerDB(t)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Task{}, &model.Log{}))
	savedDisable := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = savedDisable })
	service.InitHttpClient()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"fixture invalid key"}`)
	}))
	defer upstream.Close()
	user := model.User{Id: 91235, Username: "video-error-operator", Status: 1, Role: 100, Group: "default", Quota: 10000000, Setting: `{"billing_preference":"wallet_only"}`}
	require.NoError(t, db.Create(&user).Error)
	ch := model.Channel{Id: 91235, Type: constant.ChannelTypeMiniMax, Key: "fixture-key", BaseURL: &upstream.URL, Models: "MiniMax-H3", Group: "default", Status: common.ChannelStatusEnabled, AutoBan: common.GetPointer(1)}
	require.NoError(t, db.Create(&ch).Error)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(ch.Id)}}
	c.Set("id", user.Id)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/channel/test/91235/video", bytes.NewBufferString(`{"model":"MiniMax-H3"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	SubmitChannelVideoTest(c)
	require.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
	// The selected request-local channel suppresses auto-ban synchronously.
	autoBan, exists := common.GetContextKey(c, constant.ContextKeyChannelAutoBan)
	require.True(t, exists)
	require.Equal(t, false, autoBan)
	require.Eventually(t, func() bool {
		return db.First(&user, user.Id).Error == nil && user.Quota == 10000000
	}, time.Second*3, time.Millisecond*10)
	require.NoError(t, db.First(&ch, ch.Id).Error)
	require.Equal(t, common.ChannelStatusEnabled, ch.Status)
	require.True(t, ch.GetAutoBan())
	var tasks int64
	require.NoError(t, db.Model(&model.Task{}).Count(&tasks).Error)
	require.Zero(t, tasks)
}

type flatkeyFixtureTransport struct{ target string }

func (transport flatkeyFixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "router.flatkey.ai" {
		return nil, fmt.Errorf("unexpected upstream host %s", req.URL.Host)
	}
	request := req.Clone(req.Context())
	target, _ := url.Parse(transport.target)
	request.URL.Scheme, request.URL.Host = target.Scheme, target.Host
	return http.DefaultTransport.RoundTrip(request)
}
