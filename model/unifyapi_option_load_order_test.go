/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

// A per-model customer price for a model priced only by ExtraModelPricing must
// survive a load in which the GroupModelDiscount row happens to be stored
// first. Before the fix it was dropped as "a delisted model" on every boot,
// and the model sold at the plain group ratio until the next sync.
func TestACustomerPriceForAnExtraModelSurvivesAnyRowOrder(t *testing.T) {
	setupGroupRatioProvisionTest(t)
	ratio_setting.InitRatioSettings()
	require.NoError(t, ratio_setting.UpdateExtraModelsByJSONString(`{}`))
	t.Cleanup(func() {
		_ = ratio_setting.UpdateExtraModelsByJSONString(`{}`)
		_ = ratio_setting.UpdateGroupModelDiscountByJSONString(`{}`)
	})

	require.NoError(t, DB.Where("key IN ?", []string{"GroupModelDiscount", "ExtraModelPricing"}).Delete(&Option{}).Error)
	// Stored in the order that used to lose the price: discount row first.
	require.NoError(t, DB.Create(&Option{Key: "GroupModelDiscount", Value: `{"default":{"test-extra-model-x":1.15}}`}).Error)
	require.NoError(t, DB.Create(&Option{Key: "ExtraModelPricing",
		Value: `{"test-extra-model-x":{"vendor":"anthropic","input_usd":8,"output_usd":40}}`}).Error)

	loadOptionsFromDatabase()

	got := ratio_setting.GetGroupModelDiscountCopy()
	require.Equal(t, 1.15, got["default"]["test-extra-model-x"], "the customer price must not be dropped on load")
}

func TestOptionsThatOthersDependOnLoadFirst(t *testing.T) {
	options := []*Option{
		{Key: "GroupModelDiscount"}, {Key: "billing_setting.billing_mode"}, {Key: "GroupRatio"},
		{Key: "billing_setting.billing_expr"}, {Key: "ExtraModelPricing"}, {Key: "ModelRatio"},
	}
	orderOptionsForLoad(options)
	keys := make([]string, len(options))
	for i, o := range options {
		keys[i] = o.Key
	}
	require.Equal(t, []string{
		"billing_setting.billing_expr", "ExtraModelPricing", // what the others are checked against
		"GroupRatio", "ModelRatio", // everything else, in stored order
		"GroupModelDiscount", "billing_setting.billing_mode", // the rows checked against them
	}, keys)
}
