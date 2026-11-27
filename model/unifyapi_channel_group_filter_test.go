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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The channel page's group filter used to match only the stored group list.
// Since every pricing group is routed through every channel, a customer whose
// name appears in no channel's list (JobRight on production, 107 routable
// channels) filtered to an empty page. The filter must show what routes.

func channelIDsForGroupFilter(t *testing.T, group string) []int {
	t.Helper()
	var ids []int
	require.NoError(t, ApplyChannelGroupFilter(DB.Model(&Channel{}), group).Order("id").Pluck("id", &ids).Error)
	return ids
}

func TestGroupFilterShowsChannelsTheGroupIsRoutedThrough(t *testing.T) {
	setupChannelAccessTest(t, `{"default":1,"GenAI":1,"JobRight":1}`)
	shared := createChannel(t, "shared", "default", "gpt-4o")
	named := createChannel(t, "named", "GenAI,JobRight", "claude-opus-5")

	assert.Equal(t, []int{shared.Id, named.Id}, channelIDsForGroupFilter(t, "JobRight"),
		"a channel that does not name JobRight still serves it, so it is listed")
}

func TestGroupFilterLeavesOutAChannelTheGroupIsExcludedFrom(t *testing.T) {
	setupChannelAccessTest(t, `{"default":1,"Kingdee":1}`)
	open := createChannel(t, "open", "default", "glm-5.3")
	closed := createChannel(t, "closed", "default", "glm-5.3")
	closed.OtherSettings = `{"excluded_groups":["Kingdee"]}`
	require.NoError(t, closed.Update())

	assert.Equal(t, []int{open.Id}, channelIDsForGroupFilter(t, "Kingdee"),
		"Kingdee cannot be routed through the excluded channel, so it is not listed")
	assert.Equal(t, []int{open.Id, closed.Id}, channelIDsForGroupFilter(t, "default"))
}

func TestGroupFilterTrustsAnExclusionOverTheStoredList(t *testing.T) {
	setupChannelAccessTest(t, `{"default":1,"Kingdee":1}`)
	stale := createChannel(t, "stale", "default,Kingdee", "glm-5.3")
	stale.OtherSettings = `{"excluded_groups":["Kingdee"]}`
	require.NoError(t, stale.Update())

	assert.Empty(t, channelIDsForGroupFilter(t, "Kingdee"),
		"the list still names Kingdee, but Kingdee is never routed here, so it is not listed")
}

func TestGroupFilterStillFindsAGroupOnlyTheListNamesAndAnEmptyFilterShowsAll(t *testing.T) {
	setupChannelAccessTest(t, `{"default":1}`)
	legacy := createChannel(t, "legacy", "Retired Team", "gpt-4o")
	other := createChannel(t, "other", "default", "gpt-4o")

	assert.Equal(t, []int{legacy.Id}, channelIDsForGroupFilter(t, "Retired Team"),
		"the channel's own groups are routed, so a group only the list names is still found")
	assert.Equal(t, []int{legacy.Id, other.Id}, channelIDsForGroupFilter(t, ""))
	assert.Equal(t, []int{legacy.Id, other.Id}, channelIDsForGroupFilter(t, "all"))
	assert.Empty(t, channelIDsForGroupFilter(t, "Nobody"))
}

func TestGroupFilterNarrowsAChannelSearch(t *testing.T) {
	setupChannelAccessTest(t, `{"default":1,"JobRight":1}`)
	match := createChannel(t, "flatkey glm", "default", "glm-5.3")
	createChannel(t, "openrouter gpt", "default", "gpt-4o")

	found, err := SearchChannels("flatkey", "JobRight", "", false)
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, match.Id, found[0].Id)
}
