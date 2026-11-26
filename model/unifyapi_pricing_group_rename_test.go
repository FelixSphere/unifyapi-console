/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"sort"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A pricing group's name is its only identity, so a rename has to reach every
// place the name is written down. The fixture below is the customer that was
// half-renamed on production: logins, a key fixed to the group, a key with its
// own Auto list, per-model prices, a wallet with a balance, a channel that
// lists the group and a channel that blocks it.

type renamedCustomer struct {
	wallet     *Tenant
	owner      *User
	member     *User
	fixedKey   *Token
	autoKey    *Token
	shared     *Channel
	restricted *Channel
	named      *Channel
	other      *User
	otherKey   *Token
	otherFunds *Tenant
}

func setupRenamedCustomer(t *testing.T) renamedCustomer {
	t.Helper()
	setupPricingGroupTest(t, map[string]string{
		"ExtraModelPricing":          `{}`,
		"GroupRatio":                 `{"default":1,"friend solo":0.9,"GenAI":1}`,
		"TopupGroupRatio":            `{"default":1,"friend solo":1.1}`,
		"UserUsableGroups":           `{"default":"Default","friend solo":"Friends"}`,
		"GroupModelDiscount":         `{"friend solo":{"claude-opus-5":0.8,"gpt-6-astra":0.75},"GenAI":{"claude-opus-5":0.9}}`,
		"GroupGroupRatio":            `{"friend solo":{"default":0.95},"GenAI":{"friend solo":1.2}}`,
		"AutoGroups":                 `["default","friend solo"]`,
		"ModelRequestRateLimitGroup": `{"friend solo":[100,90]}`,
		"group_ratio_setting.group_special_usable_group": `{"GenAI":{"+:friend solo":"shared"},"friend solo":{"-:default":""}}`,
	})

	var customer renamedCustomer
	customer.wallet = createWallet(t, "friend solo", 5000)
	customer.owner = createLogin(t, "gade", "friend solo", customer.wallet.Id)
	customer.member = createLogin(t, "gade-ops", "friend solo", customer.wallet.Id)
	customer.fixedKey = createKey(t, customer.owner, "fixedgroupkeyfixedgroupkey", "friend solo")
	customer.autoKey = createKey(t, customer.member, "autokeyautokeyautokeyauto", "auto")
	require.NoError(t, DB.Model(customer.autoKey).Update("auto_groups", `["friend solo","default"]`).Error)

	customer.otherFunds = createWallet(t, "GenAI", 700)
	customer.other = createLogin(t, "genai", "GenAI", customer.otherFunds.Id)
	customer.otherKey = createKey(t, customer.other, "genaikeygenaikeygenaikey", "GenAI")

	// The settings carry a field this build knows and one it does not; both
	// have to survive the rewrite of excluded_groups.
	customer.shared = createChannel(t, "shared", "default", "claude-opus-5")
	customer.restricted = createChannel(t, "mythos", "default", "gpt-6-astra")
	customer.restricted.OtherSettings = `{"excluded_groups":["friend solo"],"azure_responses_version":"preview","from_a_newer_build":{"kept":true}}`
	require.NoError(t, customer.restricted.Update())
	customer.named = createChannel(t, "dedicated", "friend solo, default", "claude-opus-5")

	program := &PartnershipProgram{Name: "Friends", Code: "friends", Group: "friend solo"}
	require.NoError(t, DB.Create(program).Error)
	team := &PartnershipCustomer{ProgramId: program.Id, Name: "Friend Solo", Code: "friend-solo", Group: "friend solo", TenantId: customer.wallet.Id}
	require.NoError(t, DB.Create(team).Error)
	require.NoError(t, DB.Create(&PartnershipEnrollment{ProgramId: program.Id, CustomerId: team.Id, UserId: customer.owner.Id, CustomerGroup: "friend solo"}).Error)
	require.NoError(t, DB.Create(&TopUp{UserId: customer.owner.Id, TenantId: customer.wallet.Id, CustomerGroup: "friend solo", TradeNo: "paid-1", Status: common.TopUpStatusSuccess}).Error)
	require.NoError(t, DB.Create(&UserSubscription{UserId: customer.owner.Id, PrevUserGroup: "friend solo"}).Error)
	require.NoError(t, DB.Create(&Task{UserId: customer.owner.Id, Group: "friend solo"}).Error)
	return customer
}

func storedOption(t *testing.T, key string) string {
	t.Helper()
	var option Option
	require.NoError(t, DB.Where(map[string]any{"key": key}).First(&option).Error, key)
	return option.Value
}

func groupOf(t *testing.T, model any, column string, id any) string {
	t.Helper()
	var groups []string
	require.NoError(t, DB.Model(model).Where("id = ?", id).Pluck(column, &groups).Error)
	require.Len(t, groups, 1)
	return groups[0]
}

func TestRenamingAGroupMovesEveryReference(t *testing.T) {
	customer := setupRenamedCustomer(t)

	result, err := RenamePricingGroup("friend solo", " Gade Union ", "root")
	require.NoError(t, err)
	assert.Equal(t, "Gade Union", result.To, "surrounding spaces are not part of a name")

	t.Run("every option that named the group", func(t *testing.T) {
		assert.JSONEq(t, `{"default":1,"Gade Union":0.9,"GenAI":1}`, storedOption(t, "GroupRatio"))
		assert.JSONEq(t, `{"default":1,"Gade Union":1.1}`, storedOption(t, "TopupGroupRatio"))
		assert.JSONEq(t, `{"default":"Default","Gade Union":"Friends"}`, storedOption(t, "UserUsableGroups"))
		assert.JSONEq(t, `{"Gade Union":{"claude-opus-5":0.8,"gpt-6-astra":0.75},"GenAI":{"claude-opus-5":0.9}}`,
			storedOption(t, "GroupModelDiscount"), "the per-model prices keep their values under the new name")
		assert.JSONEq(t, `{"Gade Union":{"default":0.95},"GenAI":{"Gade Union":1.2}}`, storedOption(t, "GroupGroupRatio"))
		assert.JSONEq(t, `["default","Gade Union"]`, storedOption(t, "AutoGroups"))
		assert.JSONEq(t, `{"Gade Union":[100,90]}`, storedOption(t, "ModelRequestRateLimitGroup"))
		assert.JSONEq(t, `{"GenAI":{"+:Gade Union":"shared"},"Gade Union":{"-:default":""}}`,
			storedOption(t, "group_ratio_setting.group_special_usable_group"))
		for _, option := range []string{"GroupRatio", "TopupGroupRatio", "UserUsableGroups", "GroupModelDiscount",
			"GroupGroupRatio", "AutoGroups", "ModelRequestRateLimitGroup", "group_ratio_setting.group_special_usable_group"} {
			assert.Contains(t, result.Options, option)
			assert.NotContains(t, storedOption(t, option), "friend solo", option)
		}
	})

	t.Run("what the running process prices and admits", func(t *testing.T) {
		assert.False(t, ratio_setting.ContainsGroupRatio("friend solo"))
		assert.Equal(t, 0.9, ratio_setting.GetGroupRatio("Gade Union"))
		price, ok := ratio_setting.GetGroupModelDiscount("Gade Union", "claude-opus-5")
		assert.True(t, ok, "the customer's negotiated price is found under the new name")
		assert.Equal(t, 0.8, price)
		_, old := ratio_setting.GetGroupModelDiscount("friend solo", "claude-opus-5")
		assert.False(t, old)
		assert.Equal(t, 1.1, common.GetTopupGroupRatio("Gade Union"))
		assert.Equal(t, "Friends", setting.GetUserUsableGroupsCopy()["Gade Union"])
		assert.Equal(t, []string{"default", "Gade Union"}, setting.GetAutoGroups())
		rules := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.ReadAll()
		assert.Equal(t, map[string]map[string]string{
			"GenAI":      {"+:Gade Union": "shared"},
			"Gade Union": {"-:default": ""},
		}, rules, "the grant that bridged the two names during the commit is gone")
	})

	t.Run("logins and keys", func(t *testing.T) {
		assert.Equal(t, "Gade Union", groupOf(t, &User{}, "group", customer.owner.Id))
		assert.Equal(t, "Gade Union", groupOf(t, &User{}, "group", customer.member.Id))
		assert.Equal(t, "GenAI", groupOf(t, &User{}, "group", customer.other.Id), "another customer is not touched")
		assert.Equal(t, "GenAI", groupOf(t, &Token{}, "group", customer.otherKey.Id))

		// What TokenAuth checks for a key fixed to a group: the key is valid,
		// its group has a price, and its login may use that group.
		key, err := ValidateUserToken(customer.fixedKey.Key)
		require.NoError(t, err, "the key still authenticates")
		assert.Equal(t, "Gade Union", key.Group)
		assert.True(t, ratio_setting.ContainsGroupRatio(key.Group), "a key on a group without a price is refused as deprecated")
		login, err := GetUserCache(key.UserId)
		require.NoError(t, err)
		assert.Equal(t, key.Group, login.Group, "a login can always use its own group")

		auto, err := ValidateUserToken(customer.autoKey.Key)
		require.NoError(t, err)
		groups, err := auto.GetAutoGroups()
		require.NoError(t, err)
		assert.Equal(t, []string{"Gade Union", "default"}, groups, "the key's own Auto list follows, in order")
	})

	t.Run("no wallet moved and no balance changed", func(t *testing.T) {
		var wallets []CustomerWallet
		require.NoError(t, DB.Order("id").Find(&wallets).Error)
		require.Len(t, wallets, 2)
		assert.Equal(t, CustomerWallet{Id: wallets[0].Id, Group: "Gade Union", TenantId: customer.wallet.Id}, wallets[0],
			"the wallet row is renamed in place and still points at the same tenant")
		assert.Equal(t, "GenAI", wallets[1].Group)

		var tenants []Tenant
		require.NoError(t, DB.Order("id").Find(&tenants).Error)
		require.Len(t, tenants, 2, "no wallet was created")
		assert.Equal(t, customer.wallet.Id, tenants[0].Id)
		assert.Equal(t, 5000, tenants[0].Quota, "the balance is exactly what it was")
		assert.Equal(t, "Gade Union", tenants[0].Name)
		assert.Equal(t, "Gade Union", tenants[0].Group)
		assert.Equal(t, customer.wallet.Slug, tenants[0].Slug, "the slug is the stable handle")
		assert.Equal(t, 700, tenants[1].Quota)
		assert.Equal(t, "GenAI", tenants[1].Name)

		for _, login := range []*User{customer.owner, customer.member} {
			var after User
			require.NoError(t, DB.First(&after, login.Id).Error)
			assert.Equal(t, customer.wallet.Id, after.TenantId, "%s draws on the same wallet", login.Username)
			assert.Equal(t, login.Quota, after.Quota)
		}
	})

	t.Run("channels", func(t *testing.T) {
		assert.Equal(t, "Gade Union, default", groupOf(t, &Channel{}, "group", customer.named.Id),
			"the channel's own list follows and the rest of it is left as written")
		assert.Equal(t, "default", groupOf(t, &Channel{}, "group", customer.shared.Id))
		assert.JSONEq(t, `{"excluded_groups":["Gade Union"],"azure_responses_version":"preview","from_a_newer_build":{"kept":true}}`,
			groupOf(t, &Channel{}, "settings", customer.restricted.Id), "only the exclusion changes")

		var stale int64
		require.NoError(t, DB.Model(&Ability{}).Where(map[string]any{"group": "friend solo"}).Count(&stale).Error)
		assert.Zero(t, stale, "no ability row is left under the old name")
		assert.Contains(t, abilityGroupsFor(t, customer.shared, "claude-opus-5"), "Gade Union")
		assert.NotContains(t, abilityGroupsFor(t, customer.restricted, "gpt-6-astra"), "Gade Union")

		for _, cached := range []bool{false, true} {
			common.MemoryCacheEnabled = cached
			InitChannelCache()
			blocked, err := GetRandomSatisfiedChannel("Gade Union", "gpt-6-astra", 0, "")
			require.NoError(t, err)
			assert.Nil(t, blocked, "the channel that blocked the customer still does (memory cache: %v)", cached)
			open, err := GetRandomSatisfiedChannel("GenAI", "gpt-6-astra", 0, "")
			require.NoError(t, err)
			require.NotNil(t, open)
			assert.Equal(t, customer.restricted.Id, open.Id)
			routed, err := GetRandomSatisfiedChannel("Gade Union", "claude-opus-5", 0, "")
			require.NoError(t, err)
			assert.NotNil(t, routed, "and the customer still reaches its other channels (memory cache: %v)", cached)
		}
		common.MemoryCacheEnabled = false

		// Saving Group Pricing re-grants every group to every channel. The
		// exclusion has to hold through that under the new name too.
		require.NoError(t, UpdateOption("GroupRatio", `{"default":1,"Gade Union":0.9,"GenAI":1,"Kingdee":1}`))
		assert.NotContains(t, abilityGroupsFor(t, customer.restricted, "gpt-6-astra"), "Gade Union")
	})

	t.Run("the other records that carry the name", func(t *testing.T) {
		for label, holder := range map[string]struct {
			model  any
			column string
		}{
			"partnership_programs.group":             {&PartnershipProgram{}, "group"},
			"partnership_customers.group":            {&PartnershipCustomer{}, "group"},
			"partnership_enrollments.customer_group": {&PartnershipEnrollment{}, "customer_group"},
			"top_ups.customer_group":                 {&TopUp{}, "customer_group"},
			"user_subscriptions.prev_user_group":     {&UserSubscription{}, "prev_user_group"},
			"tasks.group":                            {&Task{}, "group"},
		} {
			var groups []string
			require.NoError(t, DB.Model(holder.model).Pluck(holder.column, &groups).Error, label)
			assert.Equal(t, []string{"Gade Union"}, groups, label)
			assert.EqualValues(t, 1, result.Rows[label], label)
		}
		assert.EqualValues(t, 2, result.Rows["users.group"])
		assert.EqualValues(t, 1, result.Rows["tokens.group"])
		assert.EqualValues(t, 1, result.Rows["tokens.auto_groups"])
		assert.EqualValues(t, 1, result.Rows["customer_wallets.pricing_group"])
		assert.EqualValues(t, 1, result.Rows["channels.group"])
		assert.EqualValues(t, 1, result.Rows["channels.settings.excluded_groups"])
	})

	t.Run("the change is in the pricing history", func(t *testing.T) {
		var entries []PricingConfigHistory
		require.NoError(t, DB.Where("reason = ?", "rename-pricing-group").Find(&entries).Error)
		keys := map[string]string{}
		for _, entry := range entries {
			keys[entry.Key] = entry.ChangedBy
			assert.Contains(t, entry.OldValue, "friend solo", entry.Key)
		}
		assert.Equal(t, map[string]string{"GroupRatio": "root", "GroupGroupRatio": "root", "GroupModelDiscount": "root"}, keys)
	})
}

func TestRenamingAGroupKeepsCustomerPricesStoredAfterExtraPricing(t *testing.T) {
	position := func(t *testing.T) (prices, extra int) {
		t.Helper()
		stored, err := AllOption()
		require.NoError(t, err)
		prices, extra = -1, -1
		for i, option := range stored {
			switch option.Key {
			case "GroupModelDiscount":
				prices = i
			case "ExtraModelPricing":
				extra = i
			}
		}
		return prices, extra
	}

	t.Run("an order that was right stays right", func(t *testing.T) {
		setupRenamedCustomer(t)
		result, err := RenamePricingGroup("friend solo", "Gade Union", "root")
		require.NoError(t, err)
		prices, extra := position(t)
		assert.Greater(t, prices, extra, "customer prices are checked against extra pricing, so they load after it")
		assert.Empty(t, result.Warnings)
	})

	t.Run("an order that was wrong is put right", func(t *testing.T) {
		setupPricingGroupTest(t, map[string]string{
			"GroupRatio":         `{"default":1,"friend solo":1}`,
			"GroupModelDiscount": `{"friend solo":{"claude-opus-5":0.8}}`,
		})
		require.NoError(t, DB.Create(&Option{Key: "ExtraModelPricing", Value: `{}`}).Error)
		prices, extra := position(t)
		require.Less(t, prices, extra, "precondition: the prices are stored first")

		result, err := RenamePricingGroup("friend solo", "Gade Union", "root")
		require.NoError(t, err)

		prices, extra = position(t)
		assert.Greater(t, prices, extra)
		assert.Empty(t, result.Warnings)
		assert.JSONEq(t, `{"Gade Union":{"claude-opus-5":0.8}}`, storedOption(t, "GroupModelDiscount"))
	})
}

func TestARefusedRenameChangesNothing(t *testing.T) {
	snapshot := func(t *testing.T) string {
		t.Helper()
		var out []string
		options, err := AllOption()
		require.NoError(t, err)
		for _, option := range options {
			out = append(out, option.Key+"="+option.Value)
		}
		for _, holder := range []struct {
			model  any
			column string
		}{{&User{}, "group"}, {&Token{}, "group"}, {&Token{}, "auto_groups"}, {&Tenant{}, "group"}, {&Tenant{}, "name"},
			{&CustomerWallet{}, "pricing_group"}, {&Ability{}, "group"}, {&Channel{}, "group"}, {&Channel{}, "settings"},
			{&PartnershipEnrollment{}, "customer_group"}, {&TopUp{}, "customer_group"}} {
			var values []string
			require.NoError(t, DB.Model(holder.model).Pluck(holder.column, &values).Error)
			sort.Strings(values)
			out = append(out, strings.Join(values, "|"))
		}
		out = append(out, ratio_setting.GroupRatio2JSONString(), ratio_setting.GroupModelDiscount2JSONString())
		return strings.Join(out, "\n")
	}

	for name, rename := range map[string]struct {
		from, to string
		reason   string
	}{
		"onto a group that already exists":      {"friend solo", "GenAI", "already exists"},
		"of a group that is not in the table":   {"nobody", "Gade Union", "does not exist in Group Pricing"},
		"onto the default group":                {"friend solo", "default", "reserved"},
		"onto the auto routing instruction":     {"friend solo", "auto", "reserved"},
		"of the default group":                  {"default", "standard", "cannot be renamed"},
		"to nothing":                            {"friend solo", "  ", "required"},
		"to itself":                             {"friend solo", "friend solo", "same"},
		"to a name with a comma":                {"friend solo", "Gade, Union", "comma"},
		"to a special usable rule prefix":       {"friend solo", "+:Gade", "special usable"},
		"into the BYOK namespace":               {"friend solo", "byok-gade", "byok-"},
		"to a name a stray login still carries": {"friend solo", "left behind", "already used by 1 login(s)"},
		// Checked after the options have been rewritten, so this is also the
		// proof that a failure late in the transaction undoes the early part.
		"to a name too long for a narrow column": {"friend solo", strings.Repeat("g", 65), "partnership_enrollments.customer_group"},
	} {
		t.Run(name, func(t *testing.T) {
			setupRenamedCustomer(t)
			createLogin(t, "stray", "left behind", 0)
			before := snapshot(t)

			result, err := RenamePricingGroup(rename.from, rename.to, "root")

			require.Error(t, err)
			assert.Nil(t, result)
			assert.Contains(t, err.Error(), rename.reason)
			assert.Equal(t, before, snapshot(t), "nothing was renamed")
		})
	}
}

func TestRenamingAGroupClearsAbilityRowsADeletedGroupLeftUnderTheNewName(t *testing.T) {
	customer := setupRenamedCustomer(t)
	// A group called "Gade Union" existed once and was deleted; deleting a
	// group leaves its ability rows behind.
	leftover := Ability{Group: "Gade Union", Model: "claude-opus-5", ChannelId: customer.shared.Id, Enabled: false}
	require.NoError(t, DB.Create(&leftover).Error)

	_, err := RenamePricingGroup("friend solo", "Gade Union", "root")
	require.NoError(t, err, "a leftover row must not collide with the renamed one")

	var rows []Ability
	require.NoError(t, DB.Where(map[string]any{"group": "Gade Union"}).
		Where("channel_id = ? AND model = ?", customer.shared.Id, "claude-opus-5").Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.True(t, rows[0].Enabled, "the row that survives is the live group's, not the leftover")
}

// GORM writes an updated column back into the struct it was handed. The ability
// table's key includes the group, so a rename that reused one struct renamed
// correctly once per process and matched no ability row ever after.
func TestASecondRenameInTheSameProcessStillMovesEveryRow(t *testing.T) {
	customer := setupRenamedCustomer(t)

	_, err := RenamePricingGroup("friend solo", "Gade Union", "root")
	require.NoError(t, err)
	result, err := RenamePricingGroup("Gade Union", "Gade Holdings", "root")
	require.NoError(t, err)

	assert.Contains(t, abilityGroupsFor(t, customer.shared, "claude-opus-5"), "Gade Holdings")
	assert.NotContains(t, abilityGroupsFor(t, customer.shared, "claude-opus-5"), "Gade Union")
	assert.NotContains(t, abilityGroupsFor(t, customer.restricted, "gpt-6-astra"), "Gade Holdings")
	assert.Equal(t, "Gade Holdings", groupOf(t, &User{}, "group", customer.owner.Id))
	assert.Equal(t, "Gade Holdings", groupOf(t, &Token{}, "group", customer.fixedKey.Id))
	assert.EqualValues(t, 2, result.Rows["abilities.group"], "both channels that serve the group")
	assert.EqualValues(t, 2, result.Rows["users.group"])
	price, ok := ratio_setting.GetGroupModelDiscount("Gade Holdings", "gpt-6-astra")
	assert.True(t, ok)
	assert.Equal(t, 0.75, price)
}

func TestRenamingAGroupAnswersForBothNamesWhileTheCommitIsPublished(t *testing.T) {
	setupPricingGroupTest(t, map[string]string{"GroupRatio": `{"default":1,"friend solo":0.9}`})

	final, hasFrom, hasTo, err := renameGroupInOptionValue(groupKeyedOption, `{"default":1,"friend solo":0.9}`, "friend solo", "Gade Union", false)
	require.NoError(t, err)
	assert.True(t, hasFrom)
	assert.False(t, hasTo)
	assert.JSONEq(t, `{"default":1,"Gade Union":0.9}`, final)

	// A login cached before the commit carries the old name, one read after it
	// the new one. Both must be priced the same across that gap.
	transitional, _, _, err := renameGroupInOptionValue(groupKeyedOption, `{"default":1,"friend solo":0.9}`, "friend solo", "Gade Union", true)
	require.NoError(t, err)
	assert.JSONEq(t, `{"default":1,"friend solo":0.9,"Gade Union":0.9}`, transitional)

	// And a key fixed to one name must be usable by a login carrying the
	// other, or the two caches disagreeing for a moment is a 403.
	bridged, err := bridgeUsableGroups(nil, "friend solo", "Gade Union")
	require.NoError(t, err)
	require.Len(t, bridged, 1)
	assert.JSONEq(t, `{"friend solo":{"+:Gade Union":""},"Gade Union":{"+:friend solo":""}}`, bridged[0].transitional)
	assert.JSONEq(t, `{}`, bridged[0].final, "the bridge is never what is left in memory")

	common.MemoryCacheEnabled = true
	channel := createChannel(t, "shared", "default", "claude-opus-5")
	InitChannelCache()
	serveChannelCacheUnderBothNames("friend solo", "Gade Union")
	for _, group := range []string{"friend solo", "Gade Union"} {
		routed, err := GetRandomSatisfiedChannel(group, "claude-opus-5", 0, "")
		require.NoError(t, err)
		require.NotNil(t, routed, group)
		assert.Equal(t, channel.Id, routed.Id, group)
	}
	dropChannelCacheGroup("Gade Union")
	gone, err := GetRandomSatisfiedChannel("Gade Union", "claude-opus-5", 0, "")
	require.NoError(t, err)
	assert.Nil(t, gone, "a rename that did not commit takes the alias away again")
}

// The Group Pricing table, the group pickers and the Auto list show groups in
// the order they are stored. A rename changes a name, not where it sits, and
// it does not re-encode the prices attached to it.
func TestRenamingAGroupKeepsTheOrderGroupsAreListedIn(t *testing.T) {
	for _, tc := range []struct {
		name     string
		shape    groupOptionShape
		value    string
		final    string
		bothLive string
	}{
		{
			name:     "a price table",
			shape:    groupKeyedOption,
			value:    `{"default":1,"friend solo":0.90,"GenAI":1,"R&D <lab>":1.5}`,
			final:    `{"default":1,"Gade Union":0.90,"GenAI":1,"R&D <lab>":1.5}`,
			bothLive: `{"default":1,"friend solo":0.90,"Gade Union":0.90,"GenAI":1,"R&D <lab>":1.5}`,
		},
		{
			name:     "per-model prices",
			shape:    groupKeyedOption,
			value:    `{"friend solo":{"gpt-6-astra":0.8,"claude-opus-5":0.75},"GenAI":{"gpt-6-astra":0.9}}`,
			final:    `{"Gade Union":{"gpt-6-astra":0.8,"claude-opus-5":0.75},"GenAI":{"gpt-6-astra":0.9}}`,
			bothLive: `{"friend solo":{"gpt-6-astra":0.8,"claude-opus-5":0.75},"Gade Union":{"gpt-6-astra":0.8,"claude-opus-5":0.75},"GenAI":{"gpt-6-astra":0.9}}`,
		},
		{
			name:     "group-to-group ratios, as the user's group and as the key's group",
			shape:    groupNestedOption,
			value:    `{"vip":{"friend solo":0.5,"default":1},"friend solo":{"default":0.9}}`,
			final:    `{"vip":{"Gade Union":0.5,"default":1},"Gade Union":{"default":0.9}}`,
			bothLive: `{"vip":{"friend solo":0.5,"Gade Union":0.5,"default":1},"friend solo":{"default":0.9},"Gade Union":{"default":0.9}}`,
		},
		{
			name:     "special usable rules, including added and removed groups",
			shape:    groupSpecialUsableOption,
			value:    `{"vip":{"+:friend solo":"Friends","-:default":"remove"},"friend solo":{"-:friend solo":"remove","special":"Special"}}`,
			final:    `{"vip":{"+:Gade Union":"Friends","-:default":"remove"},"Gade Union":{"-:Gade Union":"remove","special":"Special"}}`,
			bothLive: `{"vip":{"+:friend solo":"Friends","+:Gade Union":"Friends","-:default":"remove"},"friend solo":{"-:friend solo":"remove","-:Gade Union":"remove","special":"Special"},"Gade Union":{"-:friend solo":"remove","-:Gade Union":"remove","special":"Special"}}`,
		},
		{
			name:     "the Auto order",
			shape:    groupListOption,
			value:    `["vip","friend solo","default"]`,
			final:    `["vip","Gade Union","default"]`,
			bothLive: `["vip","friend solo","Gade Union","default"]`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			final, hasFrom, hasTo, err := renameGroupInOptionValue(tc.shape, tc.value, "friend solo", "Gade Union", false)
			require.NoError(t, err)
			assert.True(t, hasFrom)
			assert.False(t, hasTo)
			assert.Equal(t, tc.final, final)

			bothLive, _, _, err := renameGroupInOptionValue(tc.shape, tc.value, "friend solo", "Gade Union", true)
			require.NoError(t, err)
			assert.Equal(t, tc.bothLive, bothLive)

			_, _, alreadyThere, err := renameGroupInOptionValue(tc.shape, tc.final, "GenAI", "Gade Union", false)
			require.NoError(t, err)
			assert.True(t, alreadyThere, "a value that already names the new group says so")
		})
	}

	_, _, _, err := renameGroupInOptionValue(groupKeyedOption, `["not","an","object"]`, "friend solo", "Gade Union", false)
	assert.Error(t, err, "a value of the wrong shape is refused, not rewritten")
	_, _, _, err = renameGroupInOptionValue(groupKeyedOption, `{"friend solo":1} trailing`, "friend solo", "Gade Union", false)
	assert.Error(t, err)
}
