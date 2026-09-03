package controller

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGroupModelPricingListsCurrentGroupsAndEveryCatalogModel(t *testing.T) {
	previousGroups := ratio_setting.GroupRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"Customer B":0.9,"Customer A":1}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousGroups))
	})

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	GetGroupModelPricing(ctx)

	require.Equal(t, 200, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Groups []string                 `json:"groups"`
			Models []groupModelPricingModel `json:"models"`
			Ratios map[string]float64       `json:"group_ratios"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Equal(t, []string{"Customer A", "Customer B"}, response.Data.Groups)
	require.Len(t, response.Data.Models, len(ratio_setting.Catalog()))
	require.Equal(t, map[string]float64{"Customer A": 1, "Customer B": 0.9}, response.Data.Ratios)
}

func TestCustomerPricingDecoratesACopyWithoutLeakingAcrossCustomers(t *testing.T) {
	require.NoError(t, ratio_setting.UpdateGroupModelDiscountByJSONString(`{
		"GenAI":{"claude-opus-5":0.8},
		"UnifyAI":{"claude-opus-5":0.9}
	}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateGroupModelDiscountByJSONString(`{}`)) })

	cached := []model.Pricing{{ModelName: "claude-opus-5", ModelRatio: 2.25}}
	genAI := applyCustomerGroupModelPricing(cached, "GenAI")
	unifyAI := applyCustomerGroupModelPricing(cached, "UnifyAI")

	require.Nil(t, cached[0].CustomerGroupModelRatio, "the globally cached row must stay customer-neutral")
	require.InDelta(t, 2.25, cached[0].ModelRatio, 1e-12)
	require.NotNil(t, genAI[0].CustomerGroupModelRatio)
	require.InDelta(t, 0.8, *genAI[0].CustomerGroupModelRatio, 1e-12)
	require.InDelta(t, 2.5, genAI[0].ModelRatio, 1e-12, "display base is official $5 / 2")
	require.NotNil(t, unifyAI[0].CustomerGroupModelRatio)
	require.InDelta(t, 0.9, *unifyAI[0].CustomerGroupModelRatio, 1e-12)
}
