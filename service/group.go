package service

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

func GetUserUsableGroups(userGroup string) map[string]string {
	groupsCopy := setting.GetUserUsableGroupsCopy()
	// A provisioned customer's group is that customer's identity: the name is
	// the customer's name and the ratio is their commercial terms. It must
	// never be offered to anybody else, however it got into UserUsableGroups
	// -- provisioning used to add it, and an operator can still add it by hand.
	//
	// Filtered BEFORE the special-usable rules below, so an operator who
	// deliberately grants one user access to a partner group with "+:" still
	// can; only the blanket exposure goes. The caller's own group is restored
	// by the fallback at the end of this function.
	for group := range groupsCopy {
		if group != userGroup && model.IsCustomerOwnedGroup(group) {
			delete(groupsCopy, group)
		}
	}
	if userGroup != "" {
		applyGroupSpecialUsable(groupsCopy, userGroup)
		// 如果userGroup不在UserUsableGroups中，返回UserUsableGroups + userGroup
		if _, ok := groupsCopy[userGroup]; !ok {
			groupsCopy[userGroup] = "用户分组"
		}
	}
	return groupsCopy
}

// applyGroupSpecialUsable applies the operator's per-group overrides in place:
// "-:X" removes X, "+:X" adds X, a bare name adds it.
func applyGroupSpecialUsable(groups map[string]string, userGroup string) {
	specialSettings, ok := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.Get(userGroup)
	if !ok {
		return
	}
	for specialGroup, desc := range specialSettings {
		switch {
		case strings.HasPrefix(specialGroup, "-:"):
			delete(groups, strings.TrimPrefix(specialGroup, "-:"))
		case strings.HasPrefix(specialGroup, "+:"):
			groups[strings.TrimPrefix(specialGroup, "+:")] = desc
		default:
			groups[specialGroup] = desc
		}
	}
}

// GetUserBillableGroups is what a login may BE BILLED UNDER. It is deliberately
// narrower than GetUserUsableGroups, which answers two other questions: what the
// pricing page may SHOW, and which groups the auto walk may search for a
// CHANNEL. Those two are safe to keep broad -- an auto walk changes the channel,
// never the price, because auth pins ContextKeyUsingGroup to the login's own
// group (see middleware/unifyapi_default_user_auto_routing_test.go).
//
// Billing is not safe to keep broad. UserUsableGroups has to keep `default` --
// an empty allowlist makes filterPricingByUsableGroups return no models, so the
// public catalogue goes blank -- but `default` carries the new-customer
// discount (0.9 on production while every customer group is 1.0). Leaving it
// billable let any customer point a token at it and pay 10% less than they had
// agreed.
//
// Operator rule, 2026-09-22: a login bills under its OWN group, plus anything an
// operator granted it explicitly with "+:". Nothing else. On this deployment
// every named group is a customer contract, so self-service switching between
// them has no legitimate use.
func GetUserBillableGroups(userGroup string) map[string]string {
	billable := make(map[string]string, 2)
	if userGroup == "" {
		// No session: nothing to bill under. The catalogue is still public,
		// which is GetUserUsableGroups' job, not this one.
		return billable
	}
	billable[userGroup] = setting.GetUsableGroupDescription(userGroup)
	// An explicit grant still works, and an explicit "-:" can still take the
	// login's own group away.
	applyGroupSpecialUsable(billable, userGroup)
	return billable
}

// IsUserBillableGroup reports whether a login may have a request priced under
// groupName. Use this wherever a caller-supplied group reaches PRICING -- a
// token's group, the playground's group override -- and IsUserSelectableGroup
// where it only reaches routing.
func IsUserBillableGroup(userGroup, groupName string) bool {
	if groupName == "" || groupName == "auto" {
		return false
	}
	_, billable := GetUserBillableGroups(userGroup)[groupName]
	return billable && ratio_setting.ContainsGroupRatio(groupName)
}

func GroupInUserUsableGroups(userGroup, groupName string) bool {
	_, ok := GetUserUsableGroups(userGroup)[groupName]
	return ok
}

func IsUserSelectableGroup(userGroup, groupName string) bool {
	if groupName == "" || groupName == "auto" {
		return false
	}
	return GroupInUserUsableGroups(userGroup, groupName) && ratio_setting.ContainsGroupRatio(groupName)
}

// GetUserAutoGroup 根据用户分组获取自动分组设置
func GetUserAutoGroup(userGroup string) []string {
	autoGroups := make([]string, 0)
	seen := make(map[string]struct{})
	for _, group := range setting.GetAutoGroups() {
		if !IsUserSelectableGroup(userGroup, group) {
			continue
		}
		if _, ok := seen[group]; ok {
			continue
		}
		seen[group] = struct{}{}
		autoGroups = append(autoGroups, group)
	}
	return autoGroups
}

// FilterUserTokenAutoGroups applies current permissions before the current
// per-token limit. It intentionally does not fall back to the global Auto list.
func FilterUserTokenAutoGroups(userGroup string, groups []string) []string {
	maxCount := setting.GetMaxTokenAutoGroups()
	filtered := make([]string, 0, min(len(groups), maxCount))
	seen := make(map[string]struct{})
	for _, group := range groups {
		if !IsUserSelectableGroup(userGroup, group) {
			continue
		}
		if _, ok := seen[group]; ok {
			continue
		}
		seen[group] = struct{}{}
		filtered = append(filtered, group)
		if len(filtered) == maxCount {
			break
		}
	}
	return filtered
}

// GetRequestAutoGroups resolves the ordered Auto groups for the current token.
// The absence of the context value means that the token inherits the complete
// global Auto list; a present (even empty) value is an explicit token snapshot.
func GetRequestAutoGroups(c *gin.Context, userGroup string) []string {
	value, ok := common.GetContextKey(c, constant.ContextKeyTokenAutoGroups)
	if !ok {
		return GetUserAutoGroup(userGroup)
	}
	groups, ok := value.([]string)
	if !ok {
		return []string{}
	}
	return FilterUserTokenAutoGroups(userGroup, groups)
}

// GetGroupsEnabledModels 按 groups 顺序获取各分组启用的模型并去重
func GetGroupsEnabledModels(groups []string) []string {
	seen := make(map[string]struct{})
	models := make([]string, 0)
	for _, group := range groups {
		for _, modelName := range model.GetGroupEnabledModels(group) {
			if _, ok := seen[modelName]; !ok {
				seen[modelName] = struct{}{}
				models = append(models, modelName)
			}
		}
	}
	return models
}

// GetUserGroupRatio 获取用户使用某个分组的倍率
// userGroup 用户分组
// group 需要获取倍率的分组
func GetUserGroupRatio(userGroup, group string) float64 {
	ratio, ok := ratio_setting.GetGroupGroupRatio(userGroup, group)
	if ok {
		return ratio
	}
	return ratio_setting.GetGroupRatio(group)
}
