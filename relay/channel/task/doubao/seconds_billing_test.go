/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package doubao

// seedance 2.5 is priced per second of generated video. Between the 2026-10-17
// repricing and this file, nothing multiplied the per-call price by the length:
// EstimateBilling returned only a resolution ratio and the embedded
// taskcommon.BaseBilling settled nothing, so a 4-second clip and a 30-second
// one both cost $0.105 against a supplier cost of $0.336 to $2.52.
//
// The pre-existing tests did not catch it because they assert the ratio map
// CONTAINS "video_input" -- adding a key leaves them green. These assert the
// thing that was missing.

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func billingFor(t *testing.T, modelName string, req relaycommon.TaskSubmitReq) map[string]float64 {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("task_request", req)
	return (&TaskAdaptor{}).EstimateBilling(c, &relaycommon.RelayInfo{OriginModelName: modelName})
}

func TestSeedance25EstimateCarriesTheRequestedLength(t *testing.T) {
	for _, name := range []string{"seedance-2.5", "doubao-seedance-2-5-260628", "dreamina-seedance-2-5-260628"} {
		got := billingFor(t, name, relaycommon.TaskSubmitReq{Duration: 8})
		require.Contains(t, got, "seconds", "%s must bill per second", name)
		assert.InDelta(t, 8, got["seconds"], 1e-9)
	}
	// The same model at two lengths must not cost the same. This is the bug.
	short := billingFor(t, "seedance-2.5", relaycommon.TaskSubmitReq{Duration: 4})
	long := billingFor(t, "seedance-2.5", relaycommon.TaskSubmitReq{Duration: 30})
	assert.NotEqual(t, short["seconds"], long["seconds"])
}

func TestSeedance25AcceptsSecondsAsAString(t *testing.T) {
	got := billingFor(t, "seedance-2.5", relaycommon.TaskSubmitReq{Seconds: "12"})
	assert.InDelta(t, 12, got["seconds"], 1e-9)
}

func TestSeedance25ReservesTheMaximumWhenTheModelPicksTheLength(t *testing.T) {
	// duration -1 means "you decide", so no number at request time is right.
	// Reserving the maximum is the only safe direction; the real length is
	// settled in AdjustBillingOnComplete.
	for _, req := range []relaycommon.TaskSubmitReq{{Duration: -1}, {}, {Seconds: "not a number"}} {
		got := billingFor(t, "seedance-2.5", req)
		assert.InDelta(t, seedance25MaxSeconds, got["seconds"], 1e-9)
	}
}

func TestATokenPricedDoubaoModelGetsNoSecondsEntry(t *testing.T) {
	got := billingFor(t, "doubao-seedance-1-0-pro-250528", relaycommon.TaskSubmitReq{Duration: 8})
	assert.NotContains(t, got, "seconds",
		"only models the catalog prices per second may carry a seconds multiplier")
}

func TestSeedance25SettlesTheMeasuredLength(t *testing.T) {
	saved := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = saved })

	newTask := func(body string) *model.Task {
		task := &model.Task{Data: []byte(body)}
		task.PrivateData.BillingContext = &model.TaskBillingContext{
			PriceUnit: "second", PerCallBilling: true, ModelPrice: 0.105, GroupRatio: 0.85,
			OtherRatios: map[string]float64{"video_input": 0.5},
		}
		return task
	}
	success := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}

	// 8 s x 0.5 x $0.105 = $0.42, at 85% = $0.357.
	task := newTask(`{"id":"1","model":"seedance-2.5","status":"succeeded","duration":8}`)
	assert.Equal(t, 178500, (&TaskAdaptor{}).AdjustBillingOnComplete(task, success))

	// A reservation of 30 s that came back as 4 s must settle DOWN, not stand.
	task = newTask(`{"id":"1","model":"seedance-2.5","status":"succeeded","duration":4}`)
	assert.Equal(t, 89250, (&TaskAdaptor{}).AdjustBillingOnComplete(task, success))
}

func TestSeedance25RefusesToSettleWhatItCannotDefend(t *testing.T) {
	saved := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = saved })
	success := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}
	bc := &model.TaskBillingContext{PriceUnit: "second", PerCallBilling: true, ModelPrice: 0.105, GroupRatio: 1}

	build := func(body string, ctx *model.TaskBillingContext) *model.Task {
		task := &model.Task{Data: []byte(body)}
		task.PrivateData.BillingContext = ctx
		return task
	}
	ok := `{"id":"1","model":"seedance-2.5","status":"succeeded","duration":8}`

	// 0 means the pre-charge stands -- never a silent charge we cannot justify.
	assert.Zero(t, (&TaskAdaptor{}).AdjustBillingOnComplete(build(ok, nil), success),
		"no billing context")
	assert.Zero(t, (&TaskAdaptor{}).AdjustBillingOnComplete(
		build(ok, &model.TaskBillingContext{PriceUnit: "", ModelPrice: 0.105, GroupRatio: 1}), success),
		"a token-priced task must not be settled per second")
	assert.Zero(t, (&TaskAdaptor{}).AdjustBillingOnComplete(build(ok, bc),
		&relaycommon.TaskInfo{Status: model.TaskStatusFailure}), "unsuccessful task")
	for _, body := range []string{
		`{"id":"1","model":"seedance-2.5","status":"succeeded","duration":0}`,
		`{"id":"1","model":"seedance-2.5","status":"succeeded","duration":-1}`,
		`{"id":"1","model":"seedance-2.5","status":"succeeded","duration":31}`,
		`{"id":"1","model":"doubao-seedance-1-0-pro-250528","status":"succeeded","duration":8}`,
		`not json`,
	} {
		assert.Zero(t, (&TaskAdaptor{}).AdjustBillingOnComplete(build(body, bc), success), body)
	}
}
