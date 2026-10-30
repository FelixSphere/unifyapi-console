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

	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Startup loads billing_setting from the options table after the catalog has
// installed its expressions. A hand-written expression stored there must not
// win that race: production's gpt-5.6-sol row did, and billed $5/$30.
func TestAStoredExpressionCannotOverrideTheCatalog(t *testing.T) {
	ratio_setting.InitRatioSettings()
	t.Cleanup(ratio_setting.InitRatioSettings)

	stored := `{"gpt-5.6-sol":"len <= 272000 ? tier(\"0_272k\", p * 5 + c * 30) : tier(\"272k_plus\", p * 10 + c * 45)","gpt-4o":"tier(\"base\", p * 9)"}`
	require.True(t, handleConfigUpdate("billing_setting.billing_expr", stored))

	installed, ok := billing_setting.GetBillingExpr("gpt-5.6-sol")
	require.True(t, ok)
	assert.Equal(t, ratio_setting.BillingExprs()["gpt-5.6-sol"], installed)

	_, ok = billing_setting.GetBillingExpr("gpt-4o")
	assert.False(t, ok, "a flat-priced catalogued model keeps no expression")
}
