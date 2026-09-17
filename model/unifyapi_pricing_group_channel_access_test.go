/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The operator's rule: every customer group (a Group Pricing key) can use
// every channel, including groups created after the channel was. These tests
// drive the two places a request is routed -- the abilities table and the
// in-memory channel cache -- plus the two ways a group comes into being: an
// admin saving Group Pricing, and Builder provisioning a team.

func setupChannelAccessTest(t *testing.T, groupRatioJSON string) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}, &Option{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	previousDB, previousType := DB, common.MainDatabaseType()
	previousMaster, previousCache := common.IsMasterNode, common.MemoryCacheEnabled
	previousRatios := ratio_setting.GroupRatio2JSONString()
	DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.IsMasterNode = true
	common.MemoryCacheEnabled = false
	initCol()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
		t.Cleanup(func() { common.OptionMap = nil })
	}
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(groupRatioJSON))
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousType)
		common.IsMasterNode = previousMaster
		common.MemoryCacheEnabled = previousCache
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousRatios))
		_ = sqlDB.Close()
	})
}

func createChannel(t *testing.T, name, groups, models string) *Channel {
	t.Helper()
	channel := &Channel{Name: name, Type: 1, Key: "sk-" + name, Group: groups, Models: models, Status: common.ChannelStatusEnabled}
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, channel.AddAbilities(nil))
	return channel
}

func abilityGroupsFor(t *testing.T, channel *Channel, model string) []string {
	t.Helper()
	var groups []string
	require.NoError(t, DB.Model(&Ability{}).Where("channel_id = ? AND model = ?", channel.Id, model).
		Order(commonGroupCol).Pluck("group", &groups).Error)
	return groups
}

func TestEveryPricingGroupCanRouteThroughAChannelThatNamesNoneOfThem(t *testing.T) {
	setupChannelAccessTest(t, `{"default":1,"GenAI":1,"Acme Robotics fa0535":1,"Kroolo/Nusa-Pay fa0535":1}`)
	channel := createChannel(t, "openai-main", "default", "gpt-4o,claude-opus-5")

	assert.Equal(t, []string{"Acme Robotics fa0535", "GenAI", "Kroolo/Nusa-Pay fa0535", "default"},
		abilityGroupsFor(t, channel, "gpt-4o"), "one ability row per pricing group, on top of the channel's own group")
	assert.Equal(t, "default", channel.Group, "the channel's stored group list is the operator's and is not rewritten")

	for _, group := range []string{"default", "GenAI", "Acme Robotics fa0535", "Kroolo/Nusa-Pay fa0535"} {
		routed, err := GetRandomSatisfiedChannel(group, "claude-opus-5", 0, "")
		require.NoError(t, err, group)
		require.NotNil(t, routed, "%s must be routed to the channel", group)
		assert.Equal(t, channel.Id, routed.Id, group)
	}
	notAGroup, err := GetRandomSatisfiedChannel("Nobody", "claude-opus-5", 0, "")
	require.NoError(t, err)
	assert.Nil(t, notAGroup, "a group that is not priced is not routed anywhere")
}

// A team provisioned after the channels existed is the case that was broken:
// the operator had edited channels by hand once, and every later team had no
// channel at all.
func TestAGroupAddedLaterGainsEveryExistingChannel(t *testing.T) {
	setupChannelAccessTest(t, `{"default":1,"GenAI":1}`)
	openai := createChannel(t, "openai-main", "default,GenAI", "gpt-4o")
	anthropic := createChannel(t, "anthropic-main", "GenAI", "claude-opus-5")
	before, err := GetRandomSatisfiedChannel("Acme Robotics fa0535", "gpt-4o", 0, "")
	require.NoError(t, err)
	require.Nil(t, before, "not priced yet, so not routed")

	// The admin saves Group Pricing with the new team, exactly as the
	// Customer group pricing screen does.
	require.NoError(t, updateOptionMap("GroupRatio", `{"default":1,"GenAI":1,"Acme Robotics fa0535":1}`))

	for _, tc := range []struct {
		model   string
		channel *Channel
	}{{"gpt-4o", openai}, {"claude-opus-5", anthropic}} {
		routed, err := GetRandomSatisfiedChannel("Acme Robotics fa0535", tc.model, 0, "")
		require.NoError(t, err)
		require.NotNil(t, routed, "%s must now be reachable for the new team", tc.model)
		assert.Equal(t, tc.channel.Id, routed.Id)
	}
	assert.Equal(t, []string{"Acme Robotics fa0535", "GenAI", "default"}, abilityGroupsFor(t, openai, "gpt-4o"))

	// Saving the same map again changes nothing and touches no channel.
	granted, err := GrantAllChannelsToPricingGroups()
	require.NoError(t, err)
	assert.Zero(t, granted, "a second pass finds nothing missing")
	var rows int64
	require.NoError(t, DB.Model(&Ability{}).Count(&rows).Error)
	assert.EqualValues(t, 3+3, rows, "openai: 3 groups x 1 model; anthropic: 3 groups x 1 model; no duplicates")
}

// Production routes through the in-memory cache, which upstream builds from
// each channel's stored group list. The union must apply there too, or a new
// team would route on a cache-disabled dev box and fail in production.
func TestTheChannelCacheAppliesTheSameUnion(t *testing.T) {
	setupChannelAccessTest(t, `{"default":1,"Orii 商城 fa0535":1}`)
	channel := createChannel(t, "openai-main", "default", "gpt-4o")
	_ = createChannel(t, "disabled", "default", "gpt-4o")
	require.NoError(t, DB.Model(&Channel{}).Where("name = ?", "disabled").Update("status", common.ChannelStatusManuallyDisabled).Error)

	common.MemoryCacheEnabled = true
	InitChannelCache()

	routed, err := GetRandomSatisfiedChannel("Orii 商城 fa0535", "gpt-4o", 0, "")
	require.NoError(t, err)
	require.NotNil(t, routed, "the cache must route a pricing group the channel does not name")
	assert.Equal(t, channel.Id, routed.Id, "and never the disabled channel")
}

// Builder creates a team's pricing group through its own path. That path must
// end with the team able to reach every channel, or the team is priced but
// cannot make a single request.
func TestABuilderProvisionedTeamCanReachEveryChannel(t *testing.T) {
	setupGroupRatioProvisionTest(t)
	require.NoError(t, DB.AutoMigrate(&Channel{}, &Ability{}))
	previousMaster, previousCache := common.IsMasterNode, common.MemoryCacheEnabled
	previousRatios := ratio_setting.GroupRatio2JSONString()
	common.IsMasterNode, common.MemoryCacheEnabled = true, false
	initCol()
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"partner":0.9,"vip":0.8}`))
	t.Cleanup(func() {
		common.IsMasterNode, common.MemoryCacheEnabled = previousMaster, previousCache
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousRatios))
	})
	channel := createChannel(t, "openai-main", "default", "gpt-4o")

	require.NoError(t, EnsurePartnershipGroupRatio("Nusa Labs"))

	routed, err := GetRandomSatisfiedChannel("Nusa Labs", "gpt-4o", 0, "")
	require.NoError(t, err)
	require.NotNil(t, routed, "a provisioned team must be able to route")
	assert.Equal(t, channel.Id, routed.Id)
}

// abilities.group is varchar(64). A pricing group longer than that must be
// skipped, not turn every channel save into a database error.
func TestAnOverlongPricingGroupNeverBreaksChannelSaves(t *testing.T) {
	long := strings.Repeat("a", 65)
	setupChannelAccessTest(t, `{"default":1,"`+long+`":1}`)
	channel := createChannel(t, "openai-main", "default", "gpt-4o")

	assert.Equal(t, []string{"default"}, abilityGroupsFor(t, channel, "gpt-4o"))
	require.NoError(t, channel.UpdateAbilities(nil), "re-saving the channel must keep working")
	assert.Equal(t, []string{"default"}, abilityGroupsFor(t, channel, "gpt-4o"))
}
