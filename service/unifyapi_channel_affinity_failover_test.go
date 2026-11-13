package service

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// affinityFailoverRig is one affinity key and the knobs around it. Every call
// to request() is one finished request on that key.
type affinityFailoverRig struct {
	t        *testing.T
	cacheKey string
	backup   *model.Channel
	picks    [][]int
	group    string
}

func newAffinityFailoverRig(t *testing.T, threshold int) *affinityFailoverRig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rig := &affinityFailoverRig{
		t:        t,
		cacheKey: channelAffinityCacheNamespace + ":" + fmt.Sprintf("codex cli trace:default:failover-%d", time.Now().UnixNano()),
		backup:   &model.Channel{Id: 221},
	}

	setting := operation_setting.GetChannelAffinitySetting()
	prevEnabled, prevSwitch, prevThreshold := setting.Enabled, setting.SwitchOnSuccess, setting.FailoverAfterFailures
	setting.Enabled, setting.SwitchOnSuccess, setting.FailoverAfterFailures = true, true, threshold
	prevPick := pickFailoverChannel
	pickFailoverChannel = func(group, modelName, requestPath string, tried []int) *model.Channel {
		rig.group = group
		rig.picks = append(rig.picks, tried)
		return rig.backup
	}
	t.Cleanup(func() {
		setting.Enabled, setting.SwitchOnSuccess, setting.FailoverAfterFailures = prevEnabled, prevSwitch, prevThreshold
		pickFailoverChannel = prevPick
		_, _ = getChannelAffinityCache().DeleteMany([]string{rig.cacheKey})
		_, _ = getChannelAffinityFailuresCache().DeleteMany([]string{channelAffinityFailuresKey(rig.cacheKey)})
	})
	return rig
}

type affinityRequest struct {
	outcome  ChannelAffinityOutcome
	status   int
	channel  int
	tried    []string
	poolID   int
	noAffKey bool
}

func (rig *affinityFailoverRig) request(req affinityRequest) {
	rig.t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	if !req.noAffKey {
		setChannelAffinityContext(c, channelAffinityMeta{
			CacheKey:    rig.cacheKey,
			TTLSeconds:  600,
			RuleName:    "codex cli trace",
			KeyHint:     "abcd...wxyz",
			UsingGroup:  "default",
			ModelName:   "gpt-6-astra",
			RequestPath: "/v1/responses",
		})
	}
	if req.status == 0 {
		req.status = http.StatusOK
	}
	c.Status(req.status)
	if req.channel == 0 {
		req.channel = 189
	}
	c.Set("channel_id", req.channel)
	if req.tried != nil {
		c.Set("use_channel", req.tried)
	}
	if req.poolID != 0 {
		common.SetContextKey(c, constant.ContextKeyCreditPoolId, req.poolID)
	}
	if req.outcome != ChannelAffinityOutcomeUnknown {
		SetChannelAffinityOutcome(c, req.outcome)
	}
	SettleChannelAffinity(c, 189, "default")
}

func (rig *affinityFailoverRig) pin() int {
	rig.t.Helper()
	id, found, err := getChannelAffinityCache().Get(rig.cacheKey)
	require.NoError(rig.t, err)
	if !found {
		return 0
	}
	return id
}

func (rig *affinityFailoverRig) failures() int {
	rig.t.Helper()
	n, found, err := getChannelAffinityFailuresCache().Get(channelAffinityFailuresKey(rig.cacheKey))
	require.NoError(rig.t, err)
	if !found {
		return 0
	}
	return n
}

var (
	served        = affinityRequest{outcome: ChannelAffinityOutcomeServed}
	failedInside  = affinityRequest{outcome: ChannelAffinityOutcomeChannelFailed}
	failedWith500 = affinityRequest{outcome: ChannelAffinityOutcomeChannelFailed, status: http.StatusInternalServerError}
)

// 2026-11-13 replayed: a key pinned to 189, then ten requests that each
// answered 200 and delivered nothing. Before, all ten refreshed the pin.
func TestAffinityKeyMovesAfterConsecutiveFailures(t *testing.T) {
	rig := newAffinityFailoverRig(t, 3)
	rig.request(served)
	require.Equal(t, 189, rig.pin())

	rig.request(failedInside)
	rig.request(failedInside)
	require.Equal(t, 189, rig.pin(), "two failures are not enough")
	require.Equal(t, 2, rig.failures())
	require.Empty(t, rig.picks)

	rig.request(failedInside)
	require.Equal(t, 221, rig.pin())
	require.Zero(t, rig.failures(), "the count starts again on the new channel")
	require.Equal(t, [][]int{{189}}, rig.picks)
	require.Equal(t, "default", rig.group)
}

func TestAffinityFailureCountsHTTPErrorsAndStreamFailuresAlike(t *testing.T) {
	rig := newAffinityFailoverRig(t, 3)
	rig.request(served)
	rig.request(failedWith500)
	rig.request(failedInside)
	rig.request(failedWith500)
	require.Equal(t, 221, rig.pin())
}

func TestAServedRequestResetsTheCount(t *testing.T) {
	rig := newAffinityFailoverRig(t, 3)
	rig.request(served)
	for i := 0; i < 5; i++ {
		rig.request(failedInside)
		rig.request(failedWith500)
		rig.request(served)
		require.Zero(t, rig.failures())
	}
	require.Equal(t, 189, rig.pin())
	require.Empty(t, rig.picks)
}

// Unknown is every request the classifier has no opinion on. It must behave
// exactly as upstream does and leave the count alone in both directions.
func TestAnUnclassifiedRequestKeepsUpstreamBehaviour(t *testing.T) {
	rig := newAffinityFailoverRig(t, 3)
	rig.request(affinityRequest{status: http.StatusForbidden})
	require.Zero(t, rig.pin(), "an error response never created a pin")

	rig.request(affinityRequest{})
	require.Equal(t, 189, rig.pin(), "a response below 400 creates it")

	rig.request(failedInside)
	rig.request(failedInside)
	rig.request(affinityRequest{})
	rig.request(affinityRequest{status: http.StatusBadRequest})
	require.Equal(t, 2, rig.failures(), "neither counted nor reset")
	require.Equal(t, 189, rig.pin())

	rig.request(failedInside)
	require.Equal(t, 221, rig.pin())
}

func TestFailoverOffIsUpstreamBehaviour(t *testing.T) {
	for _, threshold := range []int{0, -1} {
		rig := newAffinityFailoverRig(t, threshold)
		for i := 0; i < 10; i++ {
			rig.request(failedInside)
		}
		require.Equal(t, 189, rig.pin(), "a 200 refreshes the pin whatever happened inside it")
		require.Zero(t, rig.failures())
		require.Empty(t, rig.picks)

		_, _ = getChannelAffinityCache().DeleteMany([]string{rig.cacheKey})
		rig.request(failedWith500)
		require.Zero(t, rig.pin())
	}
}

func TestAffinityDisabledDoesNothing(t *testing.T) {
	rig := newAffinityFailoverRig(t, 3)
	operation_setting.GetChannelAffinitySetting().Enabled = false
	for i := 0; i < 5; i++ {
		rig.request(failedInside)
	}
	rig.request(served)
	require.Zero(t, rig.pin())
	require.Zero(t, rig.failures())
	require.Empty(t, rig.picks)
}

func TestAFailedRequestDoesNotCreateOrRefreshAPin(t *testing.T) {
	rig := newAffinityFailoverRig(t, 3)
	rig.request(failedInside)
	require.Zero(t, rig.pin(), "upstream would have pinned the key to the channel that just failed it")
	require.Equal(t, 1, rig.failures())
}

// A key that has never been served has no pin, and its channel is chosen
// afresh each time. Three failures still point it at a channel that was not
// tried.
func TestANewKeyThatOnlyFailsIsPointedElsewhere(t *testing.T) {
	rig := newAffinityFailoverRig(t, 3)
	rig.request(failedWith500)
	rig.request(failedWith500)
	rig.request(failedWith500)
	require.Equal(t, 221, rig.pin())
}

func TestFailoverExcludesEveryChannelTheRequestTried(t *testing.T) {
	rig := newAffinityFailoverRig(t, 1)
	rig.request(affinityRequest{
		outcome: ChannelAffinityOutcomeChannelFailed,
		status:  http.StatusBadGateway,
		channel: 187,
		tried:   []string{"189", "187", "190"},
	})
	require.Equal(t, [][]int{{189, 187, 190}}, rig.picks)
	require.Equal(t, 221, rig.pin())
}

func TestNoBackupLeavesTheKeyAndAsksAgainNextTime(t *testing.T) {
	rig := newAffinityFailoverRig(t, 3)
	rig.backup = nil
	rig.request(served)
	for i := 0; i < 3; i++ {
		rig.request(failedInside)
	}
	require.Equal(t, 189, rig.pin())
	require.Len(t, rig.picks, 1)

	rig.request(failedInside)
	require.Len(t, rig.picks, 2, "asked again on the very next failure")
	require.Equal(t, 189, rig.pin())

	rig.backup = &model.Channel{Id: 221}
	rig.request(failedInside)
	require.Equal(t, 221, rig.pin(), "moves as soon as a channel is available")
}

// A promotional grant is tied to the channel that holds its inventory.
func TestACreditPoolRequestIsNeverMoved(t *testing.T) {
	rig := newAffinityFailoverRig(t, 3)
	rig.request(served)
	pooled := failedInside
	pooled.poolID = 7
	for i := 0; i < 6; i++ {
		rig.request(pooled)
	}
	require.Equal(t, 189, rig.pin())
	require.Empty(t, rig.picks)
}

func TestSwitchOnSuccessStillFollowsTheChannelThatAnswered(t *testing.T) {
	rig := newAffinityFailoverRig(t, 3)
	answeredBy187 := served
	answeredBy187.channel = 187
	rig.request(answeredBy187)
	require.Equal(t, 187, rig.pin())
}

func TestARequestWithoutAnAffinityKeyIsIgnored(t *testing.T) {
	rig := newAffinityFailoverRig(t, 1)
	keyless := failedInside
	keyless.noAffKey = true
	rig.request(keyless)
	keyless.outcome = ChannelAffinityOutcomeServed
	rig.request(keyless)
	require.Zero(t, rig.pin())
	require.Empty(t, rig.picks)
}

func TestTheNoOutputFlagIsPerRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.False(t, ChannelAffinityRequestHadNoOutput(c))
	require.False(t, ChannelAffinityRequestHadNoOutput(nil))
	noteChannelAffinityNoOutput(c)
	require.True(t, ChannelAffinityRequestHadNoOutput(c))
}
