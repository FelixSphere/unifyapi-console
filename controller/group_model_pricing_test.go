package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

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
