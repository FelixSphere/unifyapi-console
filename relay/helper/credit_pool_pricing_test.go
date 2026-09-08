/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package helper

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/stretchr/testify/require"
)

func TestCreditPoolRoutingGroupCannotRepriceCustomerGrant(t *testing.T) {
	resetPricingState(t)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"customer":0.7,"promo-openai":0.05}`))

	ctx, info := billingContextFor(t, "gpt-4o", "customer")
	info.UsingGroup = "promo-openai"
	info.CreditPoolId = 1
	info.CreditPricingGroup = "customer"

	group := HandleGroupRatio(ctx, info)
	require.InDelta(t, 0.7, group.GroupRatio, 1e-12)
	require.Equal(t, "promo-openai", info.UsingGroup, "routing still uses the pool group")
}
