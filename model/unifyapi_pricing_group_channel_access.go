/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// UNIFYAPI-FORK: every pricing group can route through every channel.
//
// Upstream ties channel access to the channel's own group list: a user in
// group G may only be routed to channels whose `group` column names G. Here a
// pricing group is a customer -- Builder provisions one per team, the operator
// adds one per contract -- and none of them is meant to be walled off from
// capacity. Under the upstream rule a freshly provisioned team had NO channel
// at all until an operator edited every channel by hand, and the 64-character
// `group` column cannot even hold the full list once there are a dozen teams.
//
// So the rule is applied where routing is decided rather than stored per row:
// the groups a channel serves are its explicit list UNION every key in Group
// Pricing (GroupRatio). Both routing paths consume that union -- the abilities
// table (database path, and the rows Model Square derives enable_groups from)
// and the in-memory channel cache. The channel's stored `group` column is left
// exactly as the operator wrote it.
//
// When a pricing group is added, existing channels gain abilities for it here
// (idempotent, insert-only), and the channel cache is rebuilt. Removing a
// group leaves stale rows behind; they are inert because no user carries that
// group any more.
//
// The one way to keep a customer off a channel is the channel's own
// excluded_groups setting: a group listed there is served by that channel
// under no circumstances, whether it would have come from the union or from
// the channel's own list. Saving the channel rewrites its ability rows, which
// is what removes the rows the excluded group already had.

// maxRoutingGroupLength mirrors abilities.group varchar(64). A longer pricing
// group is skipped, with a log line, rather than turning every channel save
// into a database error.
const maxRoutingGroupLength = 255

var pricingGroupChannelAccessLock sync.Mutex

// routingPricingGroups is every Group Pricing key that can be used as a
// routing group, sorted for deterministic ability rows.
func routingPricingGroups() []string {
	ratios := ratio_setting.GetGroupRatioCopy()
	groups := make([]string, 0, len(ratios))
	for group := range ratios {
		group = strings.TrimSpace(group)
		if group == "" {
			continue
		}
		if utf8.RuneCountInString(group) > maxRoutingGroupLength {
			common.SysError(fmt.Sprintf("pricing group %q is longer than %d characters and cannot be a routing group; channels will not serve it", group, maxRoutingGroupLength))
			continue
		}
		groups = append(groups, group)
	}
	sort.Strings(groups)
	return groups
}

// excludedGroups is the channel's excluded_groups setting. It is read here
// rather than through GetOtherSettings because that rewrites the channel row
// when the JSON does not parse, and routing must never write.
func (channel *Channel) excludedGroups() []string {
	if channel.OtherSettings == "" {
		return nil
	}
	settings := dto.ChannelOtherSettings{}
	if err := common.UnmarshalJsonStr(channel.OtherSettings, &settings); err != nil {
		return nil
	}
	excluded := make([]string, 0, len(settings.ExcludedGroups))
	for _, group := range settings.ExcludedGroups {
		if group = strings.TrimSpace(group); group != "" {
			excluded = append(excluded, group)
		}
	}
	return excluded
}

// routingGroups is the set of groups this channel serves: its own list first,
// in the operator's order, then every pricing group it does not already name.
//
// BYOK is the one exception, in both directions (unifyapi_byok_group.go). A
// channel that names a BYOK group holds that customer's own provider key, so
// it serves the BYOK groups it names and nothing else: no pricing group is
// added, and an ordinary group listed beside it by mistake is dropped rather
// than given the customer's key. And a BYOK group is never added to a channel
// that does not name it, so that customer cannot be served from our supply at
// a platform-fee price.
func (channel *Channel) routingGroups(pricingGroups []string) []string {
	own := channel.GetGroups()
	if byok := byokGroupsOf(own); len(byok) > 0 {
		return byok
	}
	groups := make([]string, 0, len(own)+len(pricingGroups))
	seen := make(map[string]struct{}, len(own)+len(pricingGroups))
	// Seeding seen with the excluded groups keeps them out of both loops.
	for _, group := range channel.excludedGroups() {
		seen[group] = struct{}{}
	}
	for _, group := range own {
		if group == "" {
			continue
		}
		if _, dup := seen[group]; dup {
			continue
		}
		seen[group] = struct{}{}
		groups = append(groups, group)
	}
	for _, group := range pricingGroups {
		if IsBYOKGroup(group) {
			continue
		}
		if _, dup := seen[group]; dup {
			continue
		}
		seen[group] = struct{}{}
		groups = append(groups, group)
	}
	return groups
}

// GrantAllChannelsToPricingGroups makes sure every channel has ability rows
// for every pricing group, then refreshes the routing cache if anything was
// missing. Returns how many channels needed rows added.
//
// Only the master node writes; the abilities table is shared and a slave's
// cache already applies the union on its own when it is rebuilt.
func GrantAllChannelsToPricingGroups() (int, error) {
	if !common.IsMasterNode {
		return 0, nil
	}
	pricingGroupChannelAccessLock.Lock()
	defer pricingGroupChannelAccessLock.Unlock()

	pricingGroups := routingPricingGroups()
	if len(pricingGroups) == 0 {
		return 0, nil
	}
	var channels []*Channel
	if err := DB.Find(&channels).Error; err != nil {
		return 0, err
	}
	granted := 0
	for _, channel := range channels {
		if strings.TrimSpace(channel.Models) == "" {
			continue
		}
		// Column names given to Distinct/Pluck are quoted by GORM for the
		// dialect in use, so the reserved word is passed bare here.
		var present []string
		err := DB.Model(&Ability{}).Where("channel_id = ?", channel.Id).
			Distinct("group").Pluck("group", &present).Error
		if err != nil {
			return granted, err
		}
		have := make(map[string]struct{}, len(present))
		for _, group := range present {
			have[group] = struct{}{}
		}
		missing := false
		for _, group := range channel.routingGroups(pricingGroups) {
			if _, ok := have[group]; !ok {
				missing = true
				break
			}
		}
		if !missing {
			continue
		}
		// Insert-only: rows that already exist are left alone, so a channel's
		// status, priority and weight are not rewritten here.
		if err := channel.AddAbilities(nil); err != nil {
			return granted, fmt.Errorf("grant pricing groups on channel %d (%s): %w", channel.Id, channel.Name, err)
		}
		granted++
	}
	if granted > 0 {
		common.SysLog(fmt.Sprintf("pricing groups granted on %d channel(s): %s", granted, strings.Join(pricingGroups, ", ")))
		InitChannelCache()
	}
	return granted, nil
}
