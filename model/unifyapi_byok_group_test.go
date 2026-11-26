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

// A BYOK channel holds a customer's own provider key. These tests pin the two
// directions of its isolation on both routing paths (the abilities table and
// the in-memory channel cache), because the rule next door -- every pricing
// group routes through every channel -- breaks both of them by default.

func createChannelAt(t *testing.T, name, groups, models string, priority int64) *Channel {
	t.Helper()
	channel := &Channel{Name: name, Type: 1, Key: "sk-" + name, Group: groups, Models: models,
		Status: common.ChannelStatusEnabled, Priority: &priority}
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, channel.AddAbilities(nil))
	return channel
}

// routedChannelIDs asks for a channel many times, at several retry depths,
// and returns every distinct channel the group was ever given. Selection is
// random among equal priorities and a retry walks down to lower ones, so one
// call proves nothing about which channels are reachable.
func routedChannelIDs(t *testing.T, group, model string) map[int]struct{} {
	t.Helper()
	seen := make(map[int]struct{})
	for retry := 0; retry <= 3; retry++ {
		for i := 0; i < 40; i++ {
			routed, err := GetRandomSatisfiedChannel(group, model, retry, "")
			if err != nil {
				// Upstream's database path answers a retry for a group with
				// no ability row at all with an error instead of "no
				// channel". Either way nothing was reachable.
				require.Nil(t, routed, "an error must not come with a channel")
				continue
			}
			if routed != nil {
				seen[routed.Id] = struct{}{}
			}
		}
	}
	return seen
}

func onBothRoutingPaths(t *testing.T, run func(t *testing.T)) {
	t.Helper()
	t.Run("abilities table", func(t *testing.T) {
		common.MemoryCacheEnabled = false
		run(t)
	})
	t.Run("channel cache", func(t *testing.T) {
		common.MemoryCacheEnabled = true
		InitChannelCache()
		run(t)
	})
}

func TestOnlyTheByokCustomerReachesTheirOwnKey(t *testing.T) {
	setupChannelAccessTest(t, `{"default":1,"GenAI":1,"byok-acme":0.05}`)
	// Our supply sits at the higher priority and the customer's key below it,
	// which is the arrangement where a retry would walk down onto their key.
	ours := createChannelAt(t, "anthropic-main", "default", "claude-opus-5-5", 10)
	theirs := createChannelAt(t, "acme-own-key", "byok-acme", "claude-opus-5-5", 0)

	assert.Equal(t, []string{"byok-acme"}, abilityGroupsFor(t, theirs, "claude-opus-5-5"),
		"the customer's key gets no ability row for anybody else")
	assert.Equal(t, []string{"GenAI", "default"}, abilityGroupsFor(t, ours, "claude-opus-5-5"),
		"and the BYOK group gets no row on a channel we pay for")

	onBothRoutingPaths(t, func(t *testing.T) {
		for _, group := range []string{"default", "GenAI"} {
			assert.Equal(t, map[int]struct{}{ours.Id: {}}, routedChannelIDs(t, group, "claude-opus-5-5"),
				"%s must never be served from the customer's key, at any retry depth", group)
		}
		assert.Equal(t, map[int]struct{}{theirs.Id: {}}, routedChannelIDs(t, "byok-acme", "claude-opus-5-5"),
			"the BYOK customer must never be served from our supply, at any retry depth")
	})
}

// The same arrangement with the priorities the other way round: the
// customer's key on top, where the union would have made it everybody's
// first choice.
func TestAByokKeyAtTheTopPriorityIsStillNobodyElses(t *testing.T) {
	setupChannelAccessTest(t, `{"default":1,"byok-acme":0}`)
	ours := createChannelAt(t, "anthropic-main", "default", "claude-opus-5-5", 0)
	theirs := createChannelAt(t, "acme-own-key", "byok-acme", "claude-opus-5-5", 10)

	onBothRoutingPaths(t, func(t *testing.T) {
		assert.Equal(t, map[int]struct{}{ours.Id: {}}, routedChannelIDs(t, "default", "claude-opus-5-5"))
		assert.Equal(t, map[int]struct{}{theirs.Id: {}}, routedChannelIDs(t, "byok-acme", "claude-opus-5-5"))
	})
}

// A model the customer's key serves and we do not sell must not become
// reachable, or advertised, for anybody else.
func TestAModelOnlyOnAByokKeyIsNotOfferedToOthers(t *testing.T) {
	setupChannelAccessTest(t, `{"default":1,"byok-acme":0.05}`)
	_ = createChannelAt(t, "anthropic-main", "default", "claude-opus-5-5", 0)
	_ = createChannelAt(t, "acme-own-key", "byok-acme", "acme-finetune-1", 0)

	onBothRoutingPaths(t, func(t *testing.T) {
		assert.Empty(t, routedChannelIDs(t, "default", "acme-finetune-1"))
		assert.Empty(t, routedChannelIDs(t, "byok-acme", "claude-opus-5-5"),
			"and the customer cannot reach a model their own key does not serve")
	})
	assert.NotContains(t, GetGroupEnabledModels("default"), "acme-finetune-1")
	assert.Equal(t, []string{"acme-finetune-1"}, GetGroupEnabledModels("byok-acme"))
}

// Group Pricing is saved after the channels exist. Saving it grants every
// existing channel to a new ordinary group; it must grant neither side of a
// BYOK arrangement.
func TestSavingGroupPricingGrantsNothingAcrossAByokBoundary(t *testing.T) {
	setupChannelAccessTest(t, `{"default":1}`)
	ours := createChannelAt(t, "anthropic-main", "default", "claude-opus-5-5", 0)
	theirs := createChannelAt(t, "acme-own-key", "byok-acme", "claude-opus-5-5", 0)

	require.NoError(t, updateOptionMap("GroupRatio", `{"default":1,"byok-acme":0.05,"Late Arrival Ltd":1}`))

	assert.Equal(t, []string{"Late Arrival Ltd", "default"}, abilityGroupsFor(t, ours, "claude-opus-5-5"),
		"the ordinary new group is granted our channel; the BYOK group is not")
	assert.Equal(t, []string{"byok-acme"}, abilityGroupsFor(t, theirs, "claude-opus-5-5"),
		"and the ordinary new group is not granted the customer's key")

	granted, err := GrantAllChannelsToPricingGroups()
	require.NoError(t, err)
	assert.Zero(t, granted, "a BYOK channel is complete as it stands, so the grant has nothing to add")
}

// An ordinary group typed beside a BYOK group would hand it the customer's
// key. The channel keeps serving its BYOK customer and drops the other.
func TestAnOrdinaryGroupListedBesideAByokGroupIsDropped(t *testing.T) {
	setupChannelAccessTest(t, `{"default":1,"byok-acme":0.05,"byok-acme-eu":0.05}`)
	mixed := createChannelAt(t, "acme-own-key", "default, byok-acme ,byok-acme-eu,byok-acme", "claude-opus-5-5", 0)

	assert.Equal(t, []string{"byok-acme", "byok-acme-eu"}, abilityGroupsFor(t, mixed, "claude-opus-5-5"))
	onBothRoutingPaths(t, func(t *testing.T) {
		assert.Empty(t, routedChannelIDs(t, "default", "claude-opus-5-5"))
		assert.Equal(t, map[int]struct{}{mixed.Id: {}}, routedChannelIDs(t, "byok-acme-eu", "claude-opus-5-5"))
	})
}

func TestTheByokPrefixIsMatchedWithoutRegardToCase(t *testing.T) {
	for group, want := range map[string]bool{
		"byok-acme":      true,
		"BYOK-Acme":      true,
		"  Byok-acme  ":  true,
		"byok-":          false, // the prefix alone names nobody
		"byok":           false,
		"byokacme":       false,
		"notbyok-acme":   false,
		"Kingdee":        false,
		"default":        false,
		"":               false,
		"Acme_byok-team": false, // a provisioned "Program_Team" name is not BYOK
	} {
		assert.Equal(t, want, IsBYOKGroup(group), "%q", group)
	}

	// Typed in capitals in the admin UI, it is isolated all the same.
	setupChannelAccessTest(t, `{"default":1,"BYOK-Acme":0.05}`)
	ours := createChannelAt(t, "anthropic-main", "default", "claude-opus-5-5", 0)
	theirs := createChannelAt(t, "acme-own-key", "BYOK-Acme", "claude-opus-5-5", 0)
	onBothRoutingPaths(t, func(t *testing.T) {
		assert.Equal(t, map[int]struct{}{ours.Id: {}}, routedChannelIDs(t, "default", "claude-opus-5-5"))
		assert.Equal(t, map[int]struct{}{theirs.Id: {}}, routedChannelIDs(t, "BYOK-Acme", "claude-opus-5-5"))
	})
}

// A BYOK group names a customer, so it is withheld from everybody else
// exactly like a provisioned customer's group -- and that answer must not
// depend on the partnership registry, which a BYOK customer is not in.
func TestAByokGroupIsACustomersOwnWithoutTheRegistry(t *testing.T) {
	setupChannelAccessTest(t, `{"default":1,"byok-acme":0.05}`)
	InvalidateCustomerOwnedGroupsCache()
	t.Cleanup(InvalidateCustomerOwnedGroupsCache)

	require.False(t, DB.Migrator().HasTable(&PartnershipCustomer{}), "this database has no registry at all")
	assert.True(t, IsCustomerOwnedGroup("byok-acme"))
	assert.False(t, IsCustomerOwnedGroup("default"), "an ordinary group is still ordinary")
}
