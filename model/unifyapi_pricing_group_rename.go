/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"gorm.io/gorm"
)

// UNIFYAPI-FORK: renaming a pricing group.
//
// A pricing group has no id. Its name IS the reference: logins, API keys,
// wallets, per-model prices, channel lists and channel exclusions all store the
// string. The Group Pricing form used to treat an edited name as "delete this
// key, add that one" in three option maps, which left every other holder of the
// old name behind. On production, 2026-11-22, renaming `friend solo` kept its
// logins and keys on a group that no longer had a price, orphaned 32 per-model
// prices and the wallet, and -- because the exclusions still named the old
// group -- opened four channels the customer was blocked from.
//
// RenamePricingGroup rewrites every live reference in one transaction, so the
// group is either renamed everywhere or nowhere.
//
// Two things it deliberately does not do:
//
//   - It never moves a login between wallets. The wallet registry row is
//     renamed in place and no login's tenant_id is written, so the path that
//     re-homes a login when its group changes (reattachWalletTx) is not taken.
//   - It does not rewrite history. Request logs, usage aggregates and issued
//     settlements keep the name that was true when they were written;
//     statements already follow a login's current group, and the settlement
//     screen links an invoice issued under the old name through the logs'
//     request-time group (controller/settlement.go), which is why that column
//     must stay as it was.

// PricingGroupRename reports what a rename changed.
type PricingGroupRename struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Options are the option keys whose value named the group.
	Options []string `json:"options"`
	// Rows counts the rows rewritten, keyed by "table.column".
	Rows     map[string]int64 `json:"rows"`
	Warnings []string         `json:"warnings,omitempty"`
}

// narrowGroupColumnLength is the width of the two group columns that are
// narrower than the rest (partnership_enrollments.customer_group and
// credit_pools.routing_group).
const narrowGroupColumnLength = 64

type groupOptionShape int

const (
	// {"<group>": value}
	groupKeyedOption groupOptionShape = iota
	// {"<group>": {"<group>": value}}
	groupNestedOption
	// ["<group>", ...]
	groupListOption
	// {"<group>": {"+:<group>": "description"}}
	groupSpecialUsableOption
)

// groupNamedOptions is every option whose value names a pricing group.
//
// storedOnly marks the two config aliases of GroupRatio and GroupGroupRatio:
// they are rewritten when an operator has saved them, and never created, since
// a second stored copy of Group Pricing would compete with the first on every
// reload.
var groupNamedOptions = []struct {
	key        string
	shape      groupOptionShape
	storedOnly bool
}{
	{key: "GroupRatio", shape: groupKeyedOption},
	{key: "TopupGroupRatio", shape: groupKeyedOption},
	{key: "UserUsableGroups", shape: groupKeyedOption},
	{key: "GroupModelDiscount", shape: groupKeyedOption},
	{key: "GroupGroupRatio", shape: groupNestedOption},
	{key: "AutoGroups", shape: groupListOption},
	{key: "ModelRequestRateLimitGroup", shape: groupKeyedOption},
	{key: "ModelRequestTokenLimitGroup", shape: groupKeyedOption},
	{key: "group_ratio_setting.group_special_usable_group", shape: groupSpecialUsableOption},
	{key: "group_ratio_setting.group_ratio", shape: groupKeyedOption, storedOnly: true},
	{key: "group_ratio_setting.group_group_ratio", shape: groupNestedOption, storedOnly: true},
}

// groupNamedColumns is every column that stores one pricing group name and
// must follow a rename. History is absent on purpose; see the file header.
//
// model is a constructor, not a value: GORM writes the updated column back
// into the struct it was given, and for abilities that column is part of the
// primary key, so a shared struct would turn the next rename in this process
// into "WHERE group = <the previous new name>" and match nothing.
var groupNamedColumns = []struct {
	label  string
	model  func() any
	column string
	narrow bool
}{
	{label: "users.group", model: func() any { return &User{} }, column: "group"},
	{label: "tokens.group", model: func() any { return &Token{} }, column: "group"},
	{label: "tenants.group", model: func() any { return &Tenant{} }, column: "group"},
	{label: "customer_wallets.pricing_group", model: func() any { return &CustomerWallet{} }, column: "pricing_group"},
	{label: "abilities.group", model: func() any { return &Ability{} }, column: "group"},
	{label: "partnership_programs.group", model: func() any { return &PartnershipProgram{} }, column: "group"},
	{label: "partnership_customers.group", model: func() any { return &PartnershipCustomer{} }, column: "group"},
	{label: "partnership_enrollments.customer_group", model: func() any { return &PartnershipEnrollment{} }, column: "customer_group", narrow: true},
	{label: "subscription_plans.upgrade_group", model: func() any { return &SubscriptionPlan{} }, column: "upgrade_group"},
	{label: "subscription_plans.downgrade_group", model: func() any { return &SubscriptionPlan{} }, column: "downgrade_group"},
	{label: "user_subscriptions.upgrade_group", model: func() any { return &UserSubscription{} }, column: "upgrade_group"},
	{label: "user_subscriptions.prev_user_group", model: func() any { return &UserSubscription{} }, column: "prev_user_group"},
	{label: "user_subscriptions.downgrade_group", model: func() any { return &UserSubscription{} }, column: "downgrade_group"},
	{label: "credit_pools.routing_group", model: func() any { return &CreditPool{} }, column: "routing_group", narrow: true},
	// The company a payment belongs to. A reassignment must not move it, but a
	// rename is the same company, and the statement lists payments by this
	// name beside usage that already follows the login's current group.
	{label: "top_ups.customer_group", model: func() any { return &TopUp{} }, column: "customer_group"},
	// Read back when an asynchronous task without a price snapshot is settled.
	{label: "tasks.group", model: func() any { return &Task{} }, column: "group"},
}

type renamedOption struct {
	key string
	// stored is what the options table held; empty when there was no row.
	stored string
	// memory is what the process was serving, to put back if the commit fails.
	memory string
	// both names priced alike, served across the commit.
	transitional string
	final        string
}

func validatePricingGroupRename(from, to string) error {
	switch {
	case from == "":
		return errors.New("the group to rename is required")
	case to == "":
		return errors.New("the new group name is required")
	case from == to:
		return errors.New("the new name is the same as the current one")
	case from == DefaultUserGroup || from == "auto":
		return fmt.Errorf("group %q cannot be renamed: the system refers to it by this name", from)
	case to == DefaultUserGroup || to == "auto":
		return fmt.Errorf("%q is reserved and cannot be used as a new name", to)
	case strings.Contains(to, ","):
		return errors.New("a group name cannot contain a comma: channels store their groups as a comma-separated list")
	case strings.HasPrefix(to, "+:") || strings.HasPrefix(to, "-:"):
		return errors.New(`a group name cannot start with "+:" or "-:": special usable group rules use those prefixes`)
	case utf8.RuneCountInString(to) > maxRoutingGroupLength:
		return fmt.Errorf("the new name is longer than %d characters", maxRoutingGroupLength)
	case IsBYOKGroup(from) != IsBYOKGroup(to):
		return fmt.Errorf("a rename cannot add or remove the %q prefix: it decides which channels a group may use", BYOKGroupPrefix)
	}
	return nil
}

// RenamePricingGroup renames a pricing group everywhere it is referenced, in
// one transaction. actor is recorded in the pricing history.
func RenamePricingGroup(from, to, actor string) (*PricingGroupRename, error) {
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	if err := validatePricingGroupRename(from, to); err != nil {
		return nil, err
	}

	// A grant running beside the rename would add ability rows under the old
	// name after they had been moved.
	pricingGroupChannelAccessLock.Lock()
	defer pricingGroupChannelAccessLock.Unlock()

	result := &PricingGroupRename{From: from, To: to, Options: []string{}, Rows: map[string]int64{}}
	var options []renamedOption
	var userIds []int
	var tokens []Token
	served := false

	err := DB.Transaction(func(tx *gorm.DB) error {
		return withPartnershipGroupIntegrityLock(tx, func(tx *gorm.DB) error {
			var err error
			if options, err = renameGroupInOptionsTx(tx, from, to, result); err != nil {
				return err
			}
			if err := refuseNameInUseTx(tx, to); err != nil {
				return err
			}
			if err := tx.Unscoped().Model(&User{}).Where(map[string]any{"group": from}).Pluck("id", &userIds).Error; err != nil {
				return err
			}
			if err := tx.Unscoped().Where(map[string]any{"group": from}).Find(&tokens).Error; err != nil {
				return err
			}
			autoGroupTokens, err := renameGroupInTokenAutoGroupsTx(tx, from, to, result)
			if err != nil {
				return err
			}
			tokens = append(tokens, autoGroupTokens...)
			if err := renameGroupInChannelsTx(tx, from, to, result); err != nil {
				return err
			}
			if err := renameWalletTenantTx(tx, from, to, result); err != nil {
				return err
			}
			if err := renameGroupInColumnsTx(tx, from, to, result); err != nil {
				return err
			}

			// From here until the caches below are refreshed, a request may
			// carry either name: a login cached before the commit still says
			// `from`, one read after it says `to`. Both are priced and routed
			// identically across that gap, so no request is billed at the
			// group-ratio fallback or refused a channel it was allowed.
			if options, err = bridgeUsableGroups(options, from, to); err != nil {
				return err
			}
			for _, option := range options {
				publishStoredOption(option.key, option.transitional)
			}
			serveChannelCacheUnderBothNames(from, to)
			served = true
			return nil
		})
	})
	if err != nil {
		if served {
			for _, option := range options {
				publishStoredOption(option.key, option.memory)
			}
			dropChannelCacheGroup(to)
		}
		return nil, err
	}

	if cacheErr := invalidateTokensCache(tokens); cacheErr != nil {
		common.SysError("rename pricing group: failed to drop a cached API key: " + cacheErr.Error())
	}
	for _, userId := range userIds {
		if cacheErr := RefreshUserGroupCache(userId); cacheErr != nil {
			// The entry expires by itself; until then this login is priced
			// under a name that no longer has a price.
			common.SysError(fmt.Sprintf("rename pricing group: user %d still cached under %q: %s", userId, from, cacheErr.Error()))
			result.Warnings = append(result.Warnings, fmt.Sprintf("login %d could not be refreshed in the cache and may be refused until its entry expires", userId))
		}
	}
	for _, option := range options {
		publishStoredOption(option.key, option.final)
		if option.stored != option.final {
			RecordPricingConfigChange(option.key, option.stored, option.final, actor, "rename-pricing-group")
		}
	}
	InvalidateCustomerOwnedGroupsCache()
	InitChannelCache()
	common.SysLog(fmt.Sprintf("pricing group %q renamed to %q by %s: options %s; rows %v",
		from, to, actor, strings.Join(result.Options, ", "), result.Rows))
	return result, nil
}

const specialUsableGroupOptionKey = "group_ratio_setting.group_special_usable_group"

// bridgeUsableGroups lets a key fixed to one name be used by a login carrying
// the other while the commit is being published. Auth checks a fixed-group key
// against its login's usable groups, the two are cached separately, and their
// caches cannot be refreshed in one step; without this a key and its login
// that disagree for that moment are answered with a 403. The grant exists in
// memory only and is gone when the final values are published.
func bridgeUsableGroups(options []renamedOption, from, to string) ([]renamedOption, error) {
	index := -1
	for i, option := range options {
		if option.key == specialUsableGroupOptionKey {
			index = i
		}
	}
	if index < 0 {
		common.OptionMapRWMutex.RLock()
		memory := common.OptionMap[specialUsableGroupOptionKey]
		common.OptionMapRWMutex.RUnlock()
		if strings.TrimSpace(memory) == "" {
			memory = "{}"
		}
		options = append(options, renamedOption{key: specialUsableGroupOptionKey, memory: memory, transitional: memory, final: memory})
		index = len(options) - 1
	}
	rules := map[string]map[string]json.RawMessage{}
	if err := common.Unmarshal([]byte(options[index].transitional), &rules); err != nil {
		return nil, fmt.Errorf("parse %s: %w", specialUsableGroupOptionKey, err)
	}
	for _, pair := range [][2]string{{from, to}, {to, from}} {
		if rules[pair[0]] == nil {
			rules[pair[0]] = map[string]json.RawMessage{}
		}
		rules[pair[0]]["+:"+pair[1]] = json.RawMessage(`""`)
	}
	encoded, err := common.Marshal(rules)
	if err != nil {
		return nil, err
	}
	options[index].transitional = string(encoded)
	return options, nil
}

// serveChannelCacheUnderBothNames makes the in-memory routing table answer for
// the new name with exactly the channels the old one has, exclusions included,
// until InitChannelCache rebuilds it from the committed rows.
func serveChannelCacheUnderBothNames(from, to string) {
	channelSyncLock.Lock()
	defer channelSyncLock.Unlock()
	if channels, ok := group2model2channels[from]; ok {
		group2model2channels[to] = channels
	}
}

func dropChannelCacheGroup(group string) {
	channelSyncLock.Lock()
	defer channelSyncLock.Unlock()
	delete(group2model2channels, group)
}

// publishStoredOption puts an option value into memory the way a reload from
// the database would, without the Group Pricing grant: the ability rows are
// renamed by the transaction and must not be re-derived half way through it.
func publishStoredOption(key, value string) {
	value, ok := storedOptionValueForMemory(key, value)
	if !ok {
		return
	}
	if err := updateOptionMapLocked(key, value); err != nil {
		common.SysError(fmt.Sprintf("rename pricing group: failed to publish %s: %s", key, err.Error()))
	}
}

// renameGroupInOptionsTx rewrites every option that names from and reports the
// values to publish. It refuses a group that is not in Group Pricing and a new
// name that an option already uses.
func renameGroupInOptionsTx(tx *gorm.DB, from, to string, result *PricingGroupRename) ([]renamedOption, error) {
	var options []renamedOption
	for _, named := range groupNamedOptions {
		var rows []Option
		if err := tx.Where(map[string]any{"key": named.key}).Find(&rows).Error; err != nil {
			return nil, err
		}
		common.OptionMapRWMutex.RLock()
		memory := common.OptionMap[named.key]
		common.OptionMapRWMutex.RUnlock()

		option := renamedOption{key: named.key}
		current := memory
		if len(rows) > 0 {
			option.stored = rows[0].Value
			current = rows[0].Value
		} else if named.storedOnly {
			continue
		}
		if named.key == "GroupRatio" && current == "" {
			current = ratio_setting.GroupRatio2JSONString()
		}
		if strings.TrimSpace(current) == "" {
			continue
		}
		// Publishing an empty value would empty the map it feeds, so a process
		// that never loaded this option is put back on the stored value.
		if option.memory = memory; option.memory == "" {
			option.memory = current
		}

		final, hasFrom, hasTo, err := renameGroupInOptionValue(named.shape, current, from, to, false)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", named.key, err)
		}
		if named.key == "GroupRatio" && !hasFrom {
			return nil, fmt.Errorf("group %q does not exist in Group Pricing", from)
		}
		if hasTo {
			return nil, fmt.Errorf("group %q already exists (%s); choose another name", to, named.key)
		}
		if !hasFrom {
			continue
		}
		transitional, _, _, err := renameGroupInOptionValue(named.shape, current, from, to, true)
		if err != nil {
			return nil, err
		}
		option.final, option.transitional = final, transitional
		if err := saveOptionValue(tx, named.key, final); err != nil {
			return nil, err
		}
		options = append(options, option)
		result.Options = append(result.Options, named.key)
	}
	warning, err := keepCustomerPricesAfterExtraPricingTx(tx, options)
	if err != nil {
		return nil, err
	}
	if warning != "" {
		result.Warnings = append(result.Warnings, warning)
	}
	return options, nil
}

// renameGroupInOptionValue renames from to to inside one option value. The
// entries keep their order -- the Group Pricing table and the group pickers
// list groups in stored order -- and every value attached to a name stays byte
// for byte as it was. With keepOld the old name stays, directly followed by
// the new one. hasFrom and hasTo report which names the value mentioned
// before the change.
func renameGroupInOptionValue(shape groupOptionShape, value, from, to string, keepOld bool) (renamed string, hasFrom, hasTo bool, err error) {
	names := map[string]string{from: to}
	var encoded []byte
	switch shape {
	case groupKeyedOption:
		encoded, hasFrom, hasTo, err = renameObjectKeys([]byte(value), names, keepOld)
	case groupNestedOption, groupSpecialUsableOption:
		inner := names
		if shape == groupSpecialUsableOption {
			inner = map[string]string{from: to, "+:" + from: "+:" + to, "-:" + from: "-:" + to}
		}
		var entries []jsonObjectEntry
		if entries, err = decodeJSONObject([]byte(value)); err != nil {
			return "", false, false, err
		}
		for index, entry := range entries {
			if !isJSONObject(entry.value) {
				continue
			}
			rewritten, innerFrom, innerTo, innerErr := renameObjectKeys(entry.value, inner, keepOld)
			if innerErr != nil {
				return "", false, false, innerErr
			}
			entries[index].value = rewritten
			hasFrom = hasFrom || innerFrom
			hasTo = hasTo || innerTo
		}
		var outerFrom, outerTo bool
		entries, outerFrom, outerTo = renameObjectEntries(entries, names, keepOld)
		hasFrom = hasFrom || outerFrom
		hasTo = hasTo || outerTo
		encoded, err = encodeJSONObject(entries)
	case groupListOption:
		var groups []string
		if err = common.Unmarshal([]byte(value), &groups); err != nil {
			return "", false, false, err
		}
		out := make([]string, 0, len(groups)+1)
		for _, group := range groups {
			switch group {
			case to:
				hasTo = true
				out = append(out, group)
			case from:
				hasFrom = true
				if keepOld {
					out = append(out, from)
				}
				out = append(out, to)
			default:
				out = append(out, group)
			}
		}
		encoded, err = common.Marshal(out)
	}
	if err != nil {
		return "", false, false, err
	}
	return string(encoded), hasFrom, hasTo, nil
}

// jsonObjectEntry is one member of a JSON object, in the order it was written.
type jsonObjectEntry struct {
	key   string
	value json.RawMessage
}

func isJSONObject(raw json.RawMessage) bool {
	return strings.HasPrefix(strings.TrimSpace(string(raw)), "{")
}

// decodeJSONObject reads an object without losing the order of its members,
// which decoding into a map would.
func decodeJSONObject(raw []byte) ([]jsonObjectEntry, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := opening.(json.Delim); !ok || delim != '{' {
		return nil, errors.New("expected a JSON object")
	}
	var entries []jsonObjectEntry
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, errors.New("expected a JSON object key")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		entries = append(entries, jsonObjectEntry{key: key, value: value})
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if decoder.More() {
		return nil, errors.New("unexpected data after the JSON object")
	}
	return entries, nil
}

func encodeJSONObject(entries []jsonObjectEntry) ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	for index, entry := range entries {
		if index > 0 {
			out.WriteByte(',')
		}
		// Not json.Marshal: it would write <, > and & in a name as \u escapes,
		// and the stored text is compared and shown as it is.
		var key bytes.Buffer
		encoder := json.NewEncoder(&key)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(entry.key); err != nil {
			return nil, err
		}
		out.Write(bytes.TrimRight(key.Bytes(), "\n"))
		out.WriteByte(':')
		out.Write(entry.value)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// renameObjectEntries renames the members whose key is in names, in place.
func renameObjectEntries(entries []jsonObjectEntry, names map[string]string, keepOld bool) (out []jsonObjectEntry, hasFrom, hasTo bool) {
	targets := make(map[string]struct{}, len(names))
	for _, target := range names {
		targets[target] = struct{}{}
	}
	out = make([]jsonObjectEntry, 0, len(entries)+len(names))
	for _, entry := range entries {
		if _, ok := targets[entry.key]; ok {
			hasTo = true
		}
		target, ok := names[entry.key]
		if !ok {
			out = append(out, entry)
			continue
		}
		hasFrom = true
		if keepOld {
			out = append(out, entry)
		}
		out = append(out, jsonObjectEntry{key: target, value: entry.value})
	}
	return out, hasFrom, hasTo
}

func renameObjectKeys(raw []byte, names map[string]string, keepOld bool) (encoded []byte, hasFrom, hasTo bool, err error) {
	entries, err := decodeJSONObject(raw)
	if err != nil {
		return nil, false, false, err
	}
	entries, hasFrom, hasTo = renameObjectEntries(entries, names, keepOld)
	encoded, err = encodeJSONObject(entries)
	return encoded, hasFrom, hasTo, err
}

// keepCustomerPricesAfterExtraPricingTx keeps GroupModelDiscount stored after
// ExtraModelPricing once the rename has rewritten it.
//
// Options are applied in storage order, and a customer price for a model that
// ExtraModelPricing defines is dropped on load when it is read first. An
// update in place normally leaves the order alone; where the database moved
// the row anyway, it is re-inserted. A row that still reads first is reported
// rather than failing the rename: the cost is a minute of list price for those
// models after the next restart, and the rename itself is sound.
func keepCustomerPricesAfterExtraPricingTx(tx *gorm.DB, options []renamedOption) (string, error) {
	var final string
	for _, option := range options {
		if option.key == "GroupModelDiscount" {
			final = option.final
		}
	}
	if final == "" {
		return "", nil
	}
	for attempt := 0; ; attempt++ {
		// The same unordered read loadOptionsFromDatabase makes.
		var stored []*Option
		if err := tx.Find(&stored).Error; err != nil {
			return "", err
		}
		prices, extra := -1, -1
		for position, option := range stored {
			switch option.Key {
			case "GroupModelDiscount":
				prices = position
			case "ExtraModelPricing":
				extra = position
			}
		}
		if extra < 0 || prices > extra {
			return "", nil
		}
		if attempt == 5 {
			return "GroupModelDiscount is stored before ExtraModelPricing; customer prices for extra models load one sync late after a restart until the option order is repaired", nil
		}
		if err := tx.Where(map[string]any{"key": "GroupModelDiscount"}).Delete(&Option{}).Error; err != nil {
			return "", err
		}
		if err := tx.Create(&Option{Key: "GroupModelDiscount", Value: final}).Error; err != nil {
			return "", err
		}
	}
}

// refuseNameInUseTx refuses a new name that something already refers to. A
// login, key or wallet left on a deleted group of that name would otherwise
// join the renamed customer without anybody having decided that.
func refuseNameInUseTx(tx *gorm.DB, to string) error {
	holders := []struct {
		what   string
		model  any
		column string
	}{
		{"login(s)", &User{}, "group"},
		{"API key(s)", &Token{}, "group"},
		{"wallet(s)", &CustomerWallet{}, "pricing_group"},
		{"partnership program(s)", &PartnershipProgram{}, "group"},
		{"partnership customer(s)", &PartnershipCustomer{}, "group"},
		{"credit pool(s)", &CreditPool{}, "routing_group"},
	}
	for _, holder := range holders {
		if !tx.Migrator().HasTable(holder.model) {
			continue
		}
		var count int64
		if err := tx.Model(holder.model).Where(map[string]any{holder.column: to}).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return fmt.Errorf("the name %q is already used by %d %s; choose another name or move them first", to, count, holder.what)
		}
	}
	if !tx.Migrator().HasTable(&Channel{}) {
		return nil
	}
	var channels []Channel
	if err := tx.Select([]string{"id", "group", "settings"}).Find(&channels).Error; err != nil {
		return err
	}
	for _, channel := range channels {
		for _, group := range append(channel.GetGroups(), channel.excludedGroups()...) {
			if group == to {
				return fmt.Errorf("the name %q is already used by channel %d; choose another name", to, channel.Id)
			}
		}
	}
	// No channel names it and it has no price, so any ability row under the
	// new name is what a deleted group left behind. It would collide with the
	// rows about to be renamed.
	if !tx.Migrator().HasTable(&Ability{}) {
		return nil
	}
	return tx.Where(map[string]any{"group": to}).Delete(&Ability{}).Error
}

// renameGroupInColumnsTx renames the group in every column that stores one.
func renameGroupInColumnsTx(tx *gorm.DB, from, to string, result *PricingGroupRename) error {
	for _, named := range groupNamedColumns {
		if !tx.Migrator().HasTable(named.model()) {
			continue
		}
		holders := func() *gorm.DB {
			return tx.Unscoped().Model(named.model()).Where(map[string]any{named.column: from})
		}
		if named.narrow && utf8.RuneCountInString(to) > narrowGroupColumnLength {
			var held int64
			if err := holders().Count(&held).Error; err != nil {
				return err
			}
			if held > 0 {
				return fmt.Errorf("the new name is longer than %d characters, which %s cannot hold", narrowGroupColumnLength, named.label)
			}
			continue
		}
		// UpdateColumn writes this one column and nothing else: no hooks, no
		// timestamps, and no wallet re-homing.
		updated := holders().UpdateColumn(named.column, to)
		if updated.Error != nil {
			return fmt.Errorf("rename %s: %w", named.label, updated.Error)
		}
		if updated.RowsAffected > 0 {
			result.Rows[named.label] = updated.RowsAffected
		}
	}
	return nil
}

// renameWalletTenantTx renames the customer's wallet when it carries the
// group's name, which is how customerWalletTx names it. The slug is the stable
// handle and stays; so do the tenant id and its balance.
func renameWalletTenantTx(tx *gorm.DB, from, to string, result *PricingGroupRename) error {
	if !tx.Migrator().HasTable(&CustomerWallet{}) || !tx.Migrator().HasTable(&Tenant{}) {
		return nil
	}
	var wallets []CustomerWallet
	if err := tx.Where("pricing_group = ?", from).Find(&wallets).Error; err != nil {
		return err
	}
	for _, wallet := range wallets {
		updated := tx.Unscoped().Model(&Tenant{}).Where("id = ? AND name = ?", wallet.TenantId, from).UpdateColumn("name", to)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected > 0 {
			result.Rows["tenants.name"] += updated.RowsAffected
		}
	}
	return nil
}

// renameGroupInChannelsTx renames the group in each channel's own group list
// and in its excluded_groups setting. The exclusion is the part that must not
// be missed: left on the old name it stops excluding anybody.
func renameGroupInChannelsTx(tx *gorm.DB, from, to string, result *PricingGroupRename) error {
	if !tx.Migrator().HasTable(&Channel{}) {
		return nil
	}
	var channels []Channel
	if err := tx.Select([]string{"id", "group", "settings"}).Find(&channels).Error; err != nil {
		return err
	}
	for _, channel := range channels {
		updates := map[string]any{}
		if groups, changed := renameInCommaList(channel.Group, from, to); changed {
			if utf8.RuneCountInString(groups) > 1024 {
				return fmt.Errorf("channel %d: its group list would be longer than the column allows", channel.Id)
			}
			updates["group"] = groups
			result.Rows["channels.group"]++
		}
		settings, changed, err := renameInExcludedGroups(channel.OtherSettings, from, to)
		if err != nil {
			return fmt.Errorf("channel %d: %w", channel.Id, err)
		}
		if changed {
			updates["settings"] = settings
			result.Rows["channels.settings.excluded_groups"]++
		}
		if len(updates) == 0 {
			continue
		}
		if err := tx.Model(&Channel{}).Where("id = ?", channel.Id).UpdateColumns(updates).Error; err != nil {
			return fmt.Errorf("rename group on channel %d: %w", channel.Id, err)
		}
	}
	return nil
}

func renameInCommaList(list, from, to string) (string, bool) {
	parts := strings.Split(list, ",")
	changed := false
	for i, part := range parts {
		if strings.TrimSpace(part) == from {
			parts[i] = to
			changed = true
		}
	}
	if !changed {
		return list, false
	}
	return strings.Join(parts, ","), true
}

// renameInExcludedGroups rewrites excluded_groups and leaves every other
// setting in the JSON untouched, including ones this build does not know.
func renameInExcludedGroups(settings, from, to string) (string, bool, error) {
	if !strings.Contains(settings, "excluded_groups") {
		return settings, false, nil
	}
	fields := map[string]json.RawMessage{}
	if err := common.UnmarshalJsonStr(settings, &fields); err != nil {
		// Routing reads unparseable settings as "no exclusion"; there is
		// nothing here to rename.
		return settings, false, nil
	}
	var excluded []string
	if err := common.Unmarshal(fields["excluded_groups"], &excluded); err != nil {
		return settings, false, nil
	}
	changed := false
	for i, group := range excluded {
		if strings.TrimSpace(group) == from {
			excluded[i] = to
			changed = true
		}
	}
	if !changed {
		return settings, false, nil
	}
	raw, err := common.Marshal(excluded)
	if err != nil {
		return "", false, err
	}
	fields["excluded_groups"] = raw
	encoded, err := common.Marshal(fields)
	if err != nil {
		return "", false, err
	}
	return string(encoded), true, nil
}

// renameGroupInTokenAutoGroupsTx renames the group inside the per-key Auto
// lists and returns the keys it changed, for cache invalidation.
func renameGroupInTokenAutoGroupsTx(tx *gorm.DB, from, to string, result *PricingGroupRename) ([]Token, error) {
	var candidates []Token
	if err := tx.Unscoped().Where("auto_groups <> ''").Find(&candidates).Error; err != nil {
		return nil, err
	}
	var changed []Token
	for _, token := range candidates {
		groups, err := token.GetAutoGroups()
		if err != nil {
			continue
		}
		renamed := false
		for i, group := range groups {
			if group == from {
				groups[i] = to
				renamed = true
			}
		}
		if !renamed {
			continue
		}
		raw, err := common.Marshal(groups)
		if err != nil {
			return nil, err
		}
		if err := tx.Unscoped().Model(&Token{}).Where("id = ?", token.Id).UpdateColumn("auto_groups", string(raw)).Error; err != nil {
			return nil, err
		}
		result.Rows["tokens.auto_groups"]++
		changed = append(changed, token)
	}
	return changed, nil
}

// pricingGroupUsage is what still depends on a pricing group.
type pricingGroupUsage struct {
	logins        int64
	keys          int64
	walletTenant  int
	walletBalance int
	channels      []int
}

func (usage pricingGroupUsage) describe() []string {
	var parts []string
	if usage.logins > 0 {
		parts = append(parts, fmt.Sprintf("%d login(s)", usage.logins))
	}
	if usage.keys > 0 {
		parts = append(parts, fmt.Sprintf("%d enabled API key(s) fixed to it", usage.keys))
	}
	if usage.walletBalance != 0 {
		parts = append(parts, fmt.Sprintf("a wallet that still holds a balance (tenant %d)", usage.walletTenant))
	}
	if len(usage.channels) > 0 {
		ids := make([]string, len(usage.channels))
		for i, id := range usage.channels {
			ids[i] = fmt.Sprint(id)
		}
		parts = append(parts, "the blocked customer groups of channel(s) "+strings.Join(ids, ", "))
	}
	return parts
}

// protectPricingGroupsInUse refuses a Group Pricing save that drops a group
// something still depends on.
//
// Dropping it does not delete the logins, keys or balance. It leaves them on a
// name with no price: a fixed-group key is refused outright, everything else is
// billed at the fallback ratio of 1 with the customer's per-model prices gone,
// and a channel that blocked the group blocks nobody. Nothing reports any of
// that, so the save is the last place it can be caught. Only groups this save
// removes are checked, so an old dangling reference cannot block an unrelated
// edit.
func protectPricingGroupsInUse(db *gorm.DB, kept map[string]struct{}) error {
	if !db.Migrator().HasTable(&Option{}) {
		return nil
	}
	var stored []Option
	if err := db.Where(map[string]any{"key": "GroupRatio"}).Find(&stored).Error; err != nil {
		return err
	}
	previous := ratio_setting.GroupRatio2JSONString()
	if len(stored) > 0 {
		previous = stored[0].Value
	}
	current := map[string]json.RawMessage{}
	if err := common.Unmarshal([]byte(previous), &current); err != nil {
		// An unreadable stored value has no groups to protect, and refusing
		// here would make it impossible to repair.
		return nil
	}
	var removed []string
	for group := range current {
		if _, ok := kept[group]; !ok {
			removed = append(removed, group)
		}
	}
	if len(removed) == 0 {
		return nil
	}
	sort.Strings(removed)

	excluding, err := channelsExcludingGroups(db)
	if err != nil {
		return err
	}
	for _, group := range removed {
		usage := pricingGroupUsage{channels: excluding[group]}
		if db.Migrator().HasTable(&User{}) {
			if err := db.Model(&User{}).Where(map[string]any{"group": group}).Count(&usage.logins).Error; err != nil {
				return err
			}
		}
		if db.Migrator().HasTable(&Token{}) {
			if err := db.Model(&Token{}).Where(map[string]any{"group": group, "status": common.TokenStatusEnabled}).
				Count(&usage.keys).Error; err != nil {
				return err
			}
		}
		if db.Migrator().HasTable(&CustomerWallet{}) && db.Migrator().HasTable(&Tenant{}) {
			var wallets []CustomerWallet
			if err := db.Where("pricing_group = ?", group).Find(&wallets).Error; err != nil {
				return err
			}
			for _, wallet := range wallets {
				var tenants []Tenant
				if err := db.Where("id = ?", wallet.TenantId).Find(&tenants).Error; err != nil {
					return err
				}
				if len(tenants) > 0 && tenants[0].Quota != 0 {
					usage.walletTenant, usage.walletBalance = wallet.TenantId, tenants[0].Quota
				}
			}
		}
		if parts := usage.describe(); len(parts) > 0 {
			return fmt.Errorf("group %q is still used by %s; to rename it use Rename in Group Pricing, which moves every reference, and to delete it move these off the group first",
				group, strings.Join(parts, ", "))
		}
	}
	return nil
}

// channelsExcludingGroups maps each excluded group to the channels that block
// it, in channel order.
func channelsExcludingGroups(db *gorm.DB) (map[string][]int, error) {
	excluding := map[string][]int{}
	if !db.Migrator().HasTable(&Channel{}) {
		return excluding, nil
	}
	var channels []Channel
	if err := db.Select([]string{"id", "settings"}).Where("settings LIKE ?", "%excluded_groups%").
		Order("id").Find(&channels).Error; err != nil {
		return nil, err
	}
	for _, channel := range channels {
		for _, group := range channel.excludedGroups() {
			excluding[group] = append(excluding[group], channel.Id)
		}
	}
	return excluding, nil
}
