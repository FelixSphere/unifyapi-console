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

// Every customer group routes through every channel, so the operator needs one
// way to say "not this customer, not this channel". These tests drive it
// through both routing paths, and through the save that has to take away the
// access the customer already had.

func TestAnExcludedCustomerGroupIsNotRoutedToTheChannel(t *testing.T) {
	setupChannelAccessTest(t, `{"default":1,"friend solo":1,"GenAI":1}`)
	shared := createChannel(t, "shared", "default", "gpt-6-astra")
	shared.OtherSettings = `{"excluded_groups":["friend solo"]}`
	require.NoError(t, shared.Update())

	assert.Equal(t, []string{"GenAI", "default"}, abilityGroupsFor(t, shared, "gpt-6-astra"),
		"saving the channel removes the ability rows the excluded group already had")

	excluded, err := GetRandomSatisfiedChannel("friend solo", "gpt-6-astra", 0, "")
	require.NoError(t, err)
	assert.Nil(t, excluded, "the excluded customer has no channel for this model")

	for _, group := range []string{"default", "GenAI"} {
		routed, err := GetRandomSatisfiedChannel(group, "gpt-6-astra", 0, "")
		require.NoError(t, err, group)
		require.NotNil(t, routed, "%s is not excluded and still routes", group)
		assert.Equal(t, shared.Id, routed.Id, group)
	}
}

func TestAnExcludedCustomerGroupStillReachesItsOtherChannels(t *testing.T) {
	setupChannelAccessTest(t, `{"default":1,"friend solo":1}`)
	azure := createChannel(t, "azure-japaneast", "default", "gpt-6-astra")
	azure.OtherSettings = `{"excluded_groups":["friend solo"]}`
	require.NoError(t, azure.Update())
	flatkey := createChannel(t, "flatkey", "default", "gpt-6-astra")

	for range 20 {
		routed, err := GetRandomSatisfiedChannel("friend solo", "gpt-6-astra", 0, "")
		require.NoError(t, err)
		require.NotNil(t, routed)
		assert.Equal(t, flatkey.Id, routed.Id, "only the channel that does not exclude the customer")
	}
}

func TestTheChannelCacheHonoursExcludedGroups(t *testing.T) {
	setupChannelAccessTest(t, `{"default":1,"friend solo":1}`)
	channel := createChannel(t, "shared", "default", "gpt-6-astra")
	channel.OtherSettings = `{"excluded_groups":[" friend solo "]}`
	require.NoError(t, channel.Update())

	common.MemoryCacheEnabled = true
	InitChannelCache()

	excluded, err := GetRandomSatisfiedChannel("friend solo", "gpt-6-astra", 0, "")
	require.NoError(t, err)
	assert.Nil(t, excluded, "the cache applies the same exclusion as the abilities table")
	routed, err := GetRandomSatisfiedChannel("default", "gpt-6-astra", 0, "")
	require.NoError(t, err)
	require.NotNil(t, routed)
	assert.Equal(t, channel.Id, routed.Id)
}

// Group Pricing is saved far more often than a channel is. That save re-grants
// every pricing group to every channel, and must not hand an excluded customer
// their access back.
func TestSavingGroupPricingDoesNotRestoreAnExcludedGroup(t *testing.T) {
	setupChannelAccessTest(t, `{"default":1,"friend solo":1}`)
	channel := createChannel(t, "shared", "default", "gpt-6-astra")
	channel.OtherSettings = `{"excluded_groups":["friend solo"]}`
	require.NoError(t, channel.Update())

	require.NoError(t, updateOptionMap("GroupRatio", `{"default":1,"friend solo":1,"Kingdee":1}`))

	assert.Equal(t, []string{"Kingdee", "default"}, abilityGroupsFor(t, channel, "gpt-6-astra"),
		"the new group is granted, the excluded one stays out")
}

func TestAnExclusionOutranksTheChannelsOwnGroupList(t *testing.T) {
	channel := &Channel{Group: "default,friend solo", OtherSettings: `{"excluded_groups":["friend solo"]}`}

	assert.Equal(t, []string{"default", "GenAI"}, channel.routingGroups([]string{"GenAI", "friend solo"}),
		"a blocked customer stays blocked even when the group list names them")
}

func TestClearingTheExclusionGivesTheGroupItsAccessBack(t *testing.T) {
	setupChannelAccessTest(t, `{"default":1,"friend solo":1}`)
	channel := createChannel(t, "shared", "default", "gpt-6-astra")
	channel.OtherSettings = `{"excluded_groups":["friend solo"]}`
	require.NoError(t, channel.Update())

	require.NoError(t, DB.Model(channel).Update("settings", "{}").Error)
	channel.OtherSettings = "{}"
	require.NoError(t, channel.UpdateAbilities(nil))

	routed, err := GetRandomSatisfiedChannel("friend solo", "gpt-6-astra", 0, "")
	require.NoError(t, err)
	require.NotNil(t, routed)
	assert.Equal(t, channel.Id, routed.Id)
}
