package controller

// UNIFYAPI-FORK: the admin UI is only as truthful as this endpoint. Catalog
// tests alone would still pass if GetPricingBaseline filtered or mis-rendered a
// new model, so this test pins the exact row Official price & discount consumes.

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type pricingBaselineResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Models []struct {
			Model                 string  `json:"model"`
			OfficialInputUSD      float64 `json:"official_input_usd"`
			OfficialOutputUSD     float64 `json:"official_output_usd"`
			OfficialCacheReadUSD  float64 `json:"official_cache_read_usd"`
			OfficialCacheWriteUSD float64 `json:"official_cache_write_usd"`
			Discount              float64 `json:"discount"`
			ModelRatio            float64 `json:"model_ratio"`
			CompletionRatio       float64 `json:"completion_ratio"`
			GroupPrices           map[string]struct {
				InputUSD  float64 `json:"input_usd"`
				OutputUSD float64 `json:"output_usd"`
			} `json:"group_prices"`
		} `json:"models"`
	} `json:"data"`
}

func getPricingBaselineForTest(t *testing.T) pricingBaselineResponse {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	GetPricingBaseline(ctx)
	require.Equal(t, 200, recorder.Code)

	var response pricingBaselineResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	return response
}

func TestOfficialPriceAndDiscountIncludesFable51(t *testing.T) {
	previousDiscounts := ratio_setting.ModelDiscount2JSONString()
	previousGroups := ratio_setting.GroupRatio2JSONString()
	ratio_setting.InitRatioSettings()
	require.NoError(t, ratio_setting.UpdateModelDiscountByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelDiscountByJSONString(previousDiscounts))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousGroups))
	})

	response := getPricingBaselineForTest(t)
	for _, row := range response.Data.Models {
		if row.Model != "claude-fable-5.1" {
			continue
		}
		require.InDelta(t, 10, row.OfficialInputUSD, 1e-9)
		require.InDelta(t, 50, row.OfficialOutputUSD, 1e-9)
		require.InDelta(t, 0.25, row.OfficialCacheReadUSD, 1e-9)
		require.InDelta(t, 12.5, row.OfficialCacheWriteUSD, 1e-9)
		require.InDelta(t, 1, row.Discount, 1e-9)
		require.InDelta(t, 5, row.ModelRatio, 1e-9)
		require.InDelta(t, 5, row.CompletionRatio, 1e-9)
		require.InDelta(t, 10, row.GroupPrices["default"].InputUSD, 1e-9)
		require.InDelta(t, 50, row.GroupPrices["default"].OutputUSD, 1e-9)
		return
	}
	t.Fatal("claude-fable-5.1 is missing from the Official price & discount API")
}
