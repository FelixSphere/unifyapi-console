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

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupGroupRatioProvisionTest(t *testing.T) {
	t.Helper()
	setupPartnershipTestDB(t)
	// An in-memory SQLite database is per-connection, and the pricing snapshot
	// is written on the global handle while a transaction holds another. Pin
	// the pool to one connection so both see the same schema.
	sqlDB, err := DB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, DB.AutoMigrate(&PricingConfigHistory{}))
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
		t.Cleanup(func() { common.OptionMap = nil })
	}
	// The snapshot reads the previous value from the option cache, which a
	// running server keeps populated. Seed it so the test exercises an
	// overwrite rather than a first write.
	var option Option
	require.NoError(t, DB.Where("key = ?", "GroupRatio").First(&option).Error)
	common.OptionMapRWMutex.Lock()
	common.OptionMap["GroupRatio"] = option.Value
	common.OptionMapRWMutex.Unlock()
}

func currentGroupRatios(t *testing.T) map[string]float64 {
	t.Helper()
	var option Option
	require.NoError(t, DB.Where("key = ?", "GroupRatio").First(&option).Error)
	groups := map[string]float64{}
	require.NoError(t, common.Unmarshal([]byte(option.Value), &groups))
	return groups
}

// The property that matters most in this file. Group Pricing replaces rather
// than merges on save, and a save through a careless path once left production
// holding thousands of keys. Adding one team must leave every other entry
// exactly as it was.
func TestProvisioningATeamPreservesEveryExistingPrice(t *testing.T) {
	setupGroupRatioProvisionTest(t)
	before := currentGroupRatios(t)
	require.NotEmpty(t, before, "the fixture must start with prices to preserve")

	require.NoError(t, EnsurePartnershipGroupRatio("Nusa Labs"))

	after := currentGroupRatios(t)
	for group, ratio := range before {
		assert.InDelta(t, ratio, after[group], 1e-9, "existing group %q was altered or dropped", group)
	}
	assert.Len(t, after, len(before)+1, "exactly one group added")
	assert.InDelta(t, 1, after["Nusa Labs"], 1e-9, "a new team starts at list price")
}

// Reconnecting must never reprice a team. A member joining a discounted team
// would otherwise quietly restore it to list price.
func TestReconnectingNeverRepricesAnExistingTeam(t *testing.T) {
	setupGroupRatioProvisionTest(t)
	require.NoError(t, EnsurePartnershipGroupRatio("Nusa Labs"))

	// Somebody negotiates a discount.
	groups := currentGroupRatios(t)
	groups["Nusa Labs"] = 0.7
	encoded, err := common.Marshal(groups)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", "GroupRatio").
		Update("value", string(encoded)).Error)

	// A second member of the same team connects.
	require.NoError(t, EnsurePartnershipGroupRatio("Nusa Labs"))

	assert.InDelta(t, 0.7, currentGroupRatios(t)["Nusa Labs"], 1e-9,
		"a negotiated discount must survive another member connecting")
}

// Several teams provisioned in turn all survive; each write merges into the
// last rather than starting from the fixture.
func TestProvisioningSeveralTeamsKeepsThemAll(t *testing.T) {
	setupGroupRatioProvisionTest(t)
	before := len(currentGroupRatios(t))
	for _, team := range []string{"Nusa Labs", "Acme Robotics", "Vidora"} {
		require.NoError(t, EnsurePartnershipGroupRatio(team))
	}
	after := currentGroupRatios(t)
	assert.Len(t, after, before+3)
	for _, team := range []string{"Nusa Labs", "Acme Robotics", "Vidora"} {
		assert.Contains(t, after, team)
	}
}

// Overwriting Group Pricing keeps the same audit trail the admin path keeps,
// so an automatic write is as recoverable as a manual one.
func TestProvisioningRecordsTheOverwrittenPricingForAudit(t *testing.T) {
	setupGroupRatioProvisionTest(t)
	require.NoError(t, EnsurePartnershipGroupRatio("Nusa Labs"))

	var history []PricingConfigHistory
	require.NoError(t, DB.Where("key = ?", "GroupRatio").Find(&history).Error)
	require.NotEmpty(t, history, "the previous pricing must be snapshotted before it is replaced")
	assert.Equal(t, "builder-bridge", history[len(history)-1].ChangedBy)
}

// Codes are pattern-constrained while team names are free text, so the
// projection has to produce something valid from anything a product is called.
func TestARegistrationCodeIsDerivedFromAnyTeamName(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"Acme Robotics", "acme-robotics"},
		{"  NusaPay  ", "nusapay"},
		{"Orii/Kroolo", "oriikroolo"},
		{"A", "team-a"},
		{"投资", "team"},
	} {
		code := partnershipCodeFromName(tc.name)
		assert.Equal(t, tc.want, code, "name %q", tc.name)
		assert.True(t, partnershipCodePattern.MatchString(code),
			"derived code %q from %q must satisfy the registration pattern", code, tc.name)
	}
}
