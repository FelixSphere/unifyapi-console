/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package openrouter

// MiniMax-H3 on the OpenRouter channel is mapped to minimax/hailuo-3, which
// OpenRouter renders at 2K only, at $0.13 per second. The catalog prices H3 at
// its 768p rate of $0.08, and before this test the adaptor billed only
// duration, so every OpenRouter H3 second was sold at $0.08 x group ratio
// ($0.068 at 0.85) against a $0.13 upstream charge.
//
// The dollar figures below are typed from MiniMax's pay-as-you-go page
// (https://platform.minimax.io/docs/guides/pricing-paygo, checked 2026-10-17),
// not derived from resolutionPricesUSD, so a mutated table fails them.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetH3Pricing(t *testing.T) {
	t.Helper()
	savedQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	ratio_setting.InitRatioSettings()
	require.NoError(t, ratio_setting.UpdateModelDiscountByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateGroupModelDiscountByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":0.85,"list":1}`))
	t.Cleanup(func() {
		common.QuotaPerUnit = savedQuotaPerUnit
		require.NoError(t, ratio_setting.UpdateModelDiscountByJSONString(`{}`))
		require.NoError(t, ratio_setting.UpdateGroupModelDiscountByJSONString(`{}`))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{}`))
	})
}

// submitH3 prices a request the way RelayTaskSubmit does (steps 4-6): the
// per-call catalog price at the group ratio, then the adaptor's OtherRatios.
// It returns the quota pre-consumed, which is also the settled charge, since
// this adaptor makes no completion-time adjustment.
func submitH3(t *testing.T, origin, upstream, group, body string) (quota int, buildErr error) {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("group", group)
	a := &TaskAdaptor{}
	info := testInfo()
	info.OriginModelName = origin
	info.UserGroup, info.UsingGroup = group, group
	a.Init(info)
	require.Nil(t, a.ValidateRequestAndSetAction(c, info))
	info.UpstreamModelName = upstream

	priceData, err := helper.ModelPriceHelperPerCall(c, info)
	require.NoError(t, err)
	require.InDelta(t, 0.08, priceData.ModelPrice, 1e-9, "both H3 catalog rows are MiniMax's 768p price")
	info.PriceData = priceData
	for k, v := range a.EstimateBilling(c, info) {
		info.PriceData.AddOtherRatio(k, v)
	}
	quota, _ = common.QuotaFromFloatChecked(info.PriceData.ApplyOtherRatiosToFloat(float64(info.PriceData.Quota)))
	_, buildErr = a.BuildRequestBody(c, info)
	return quota, buildErr
}

func quotaForUSD(usd float64) int {
	quota, _ := common.QuotaFromFloatStrict(usd * common.QuotaPerUnit)
	return quota
}

func TestOpenRouterH3BillsEachResolutionAtMiniMaxsPricePerSecond(t *testing.T) {
	resetH3Pricing(t)
	for _, tc := range []struct {
		name, origin, upstream, group, body string
		usd                                 float64
	}{
		{"H3 768p at 0.85", "MiniMax-H3", "minimax/hailuo-3", "default", `{"prompt":"a train","duration":5,"resolution":"768p"}`, 5 * 0.08 * 0.85},
		{"H3 2K at 0.85", "MiniMax-H3", "minimax/hailuo-3", "default", `{"prompt":"a train","duration":5,"resolution":"2K"}`, 5 * 0.13 * 0.85},
		{"H3 2K as size shorthand", "MiniMax-H3", "minimax/hailuo-3", "default", `{"prompt":"a train","seconds":"10","size":"2k"}`, 10 * 0.13 * 0.85},
		{"H3 2K at list", "MiniMax-H3", "minimax/hailuo-3", "list", `{"prompt":"a train","duration":5,"resolution":"2K"}`, 5 * 0.13},
		{"H3 Max 480p at 0.85", "MiniMax-H3-Max", "minimax/hailuo-3-max", "default", `{"prompt":"a train","duration":5,"resolution":"480p"}`, 5 * 0.05 * 0.85},
		{"H3 Max 768p at 0.85", "MiniMax-H3-Max", "minimax/hailuo-3-max", "default", `{"prompt":"a train","duration":5,"resolution":"768P"}`, 5 * 0.08 * 0.85},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quota, err := submitH3(t, tc.origin, tc.upstream, tc.group, tc.body)
			require.NoError(t, err)
			assert.Equal(t, quotaForUSD(tc.usd), quota)
		})
	}
}

// Whatever the request leaves upstream to decide, H3 over OpenRouter renders
// 2K, so at list price it must bill at least MiniMax's $0.13 per 2K second.
// A resolution label we hold no price for is refused before anything is sent.
func TestOpenRouterH3NeverBillsAnUnpricedResolutionBelowThe2KPrice(t *testing.T) {
	resetH3Pricing(t)
	for _, body := range []string{
		`{"prompt":"a train","duration":5}`,
		`{"prompt":"a train","duration":5,"size":"1920x1080"}`,
		`{"prompt":"a train","duration":5,"resolution":2}`,
	} {
		t.Run(body, func(t *testing.T) {
			quota, err := submitH3(t, "MiniMax-H3", "minimax/hailuo-3", "list", body)
			require.NoError(t, err)
			assert.GreaterOrEqual(t, quota, quotaForUSD(5*0.13))
		})
	}
	for _, body := range []string{
		`{"prompt":"a train","duration":5,"resolution":"1080p"}`,
		`{"prompt":"a train","duration":5,"resolution":"4K"}`,
		`{"prompt":"a train","duration":5,"size":"720p"}`,
		`{"prompt":"a train","duration":5,"metadata":{"resolution":"1440p"}}`,
	} {
		t.Run(body, func(t *testing.T) {
			_, err := submitH3(t, "MiniMax-H3", "minimax/hailuo-3", "list", body)
			require.Error(t, err)
		})
	}
	_, err := submitH3(t, "MiniMax-H3-Max", "minimax/hailuo-3-max", "list", `{"prompt":"a train","duration":5,"resolution":"2K"}`)
	require.Error(t, err, "H3 Max has no 2K price")
}
