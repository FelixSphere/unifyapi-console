package controller

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func filterPricingByUsableGroups(pricing []model.Pricing, usableGroup map[string]string) []model.Pricing {
	if len(pricing) == 0 {
		return pricing
	}
	if len(usableGroup) == 0 {
		return []model.Pricing{}
	}

	filtered := make([]model.Pricing, 0, len(pricing))
	for _, item := range pricing {
		if common.StringsContains(item.EnableGroup, "all") {
			filtered = append(filtered, item)
			continue
		}
		for _, group := range item.EnableGroup {
			if _, ok := usableGroup[group]; ok {
				filtered = append(filtered, item)
				break
			}
		}
	}
	return filtered
}

func GetPricing(c *gin.Context) {
	pricing := model.GetPricing()
	userId, exists := c.Get("id")
	usableGroup := map[string]string{}
	groupRatio := map[string]float64{}
	for s, f := range ratio_setting.GetGroupRatioCopy() {
		groupRatio[s] = f
	}
	var group string
	if exists {
		user, err := model.GetUserCache(userId.(int))
		if err == nil {
			group = user.Group
			for g := range groupRatio {
				ratio, ok := ratio_setting.GetGroupGroupRatio(group, g)
				if ok {
					groupRatio[g] = ratio
				}
			}
		}
	}
	pricing = applyCustomerGroupModelPricing(pricing, group)

	usableGroup = service.GetUserUsableGroups(group)
	pricing = filterPricingByUsableGroups(pricing, usableGroup)
	// check groupRatio contains usableGroup
	for group := range ratio_setting.GetGroupRatioCopy() {
		if _, ok := usableGroup[group]; !ok {
			delete(groupRatio, group)
		}
	}

	c.JSON(200, gin.H{
		"success":            true,
		"data":               pricing,
		"vendors":            model.GetVendors(),
		"group_ratio":        groupRatio,
		"usable_group":       usableGroup,
		"supported_endpoint": model.GetSupportedEndpointMap(),
		"auto_groups":        service.GetUserAutoGroup(group),
		"pricing_version":    "a42d372ccf0b5dd13ecf71203521f9d2",
	})
}

// applyCustomerGroupModelPricing returns request-owned rows. GetPricing caches
// its slice globally, so mutating it would leak one customer's contract into
// the next customer's Model Square response.
func applyCustomerGroupModelPricing(pricing []model.Pricing, userGroup string) []model.Pricing {
	if userGroup == "" {
		return pricing
	}
	out := make([]model.Pricing, len(pricing))
	copy(out, pricing)
	for i := range out {
		ratio, ok := ratio_setting.GetGroupModelDiscount(userGroup, out[i].ModelName)
		if !ok {
			continue
		}
		entry, catalogued := ratio_setting.CatalogEntryFor(out[i].ModelName)
		if !catalogued {
			continue
		}
		out[i].CustomerGroupModelRatio = &ratio
		// Model Square multiplies the model base by its displayed group ratio.
		// Reset the base to official here so a global ModelDiscount cannot be
		// applied a second time beneath the final customer multiplier.
		out[i].ModelRatio = entry.ModelRatio()
		if entry.PerCallUSD > 0 {
			out[i].ModelPrice = entry.PerCallUSD
		}
	}
	return out
}

// ResetModelRatio restores the pricing baseline from the catalog.
//
// UNIFYAPI-FORK: this used to write ModelRatio alone. Because every ratio is
// now derived from one list price, restoring the input ratio without also
// restoring the output/cache multipliers leaves a half-reset price -- so all
// four token-pricing maps go back together, in one transaction, via
// UpdateOptionsBulk. Anything not in the catalog is dropped rather than merged,
// which is the point: reset is how you get the DB back to exactly the models
// UnifyAPI sells.
func ResetModelRatio(c *gin.Context) {
	baseline := ratio_setting.BaselineRatios()
	values := make(map[string]string, len(baseline))
	for option, ratios := range baseline {
		encoded, err := common.Marshal(ratios)
		if err != nil {
			c.JSON(200, gin.H{
				"success": false,
				"message": "序列化 " + option + " 失败：" + err.Error(),
			})
			return
		}
		values[option] = string(encoded)
	}

	// UpdateOptionsBulk persists every key in one transaction and only then
	// dispatches them into the in-memory maps, so a failure part-way cannot
	// leave the process billing on a half-applied baseline.
	if err := model.UpdateOptionsBulkAs(values, optionChangeActor(c)); err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	c.JSON(200, gin.H{
		"success": true,
		"message": fmt.Sprintf("已按官方报价基线重置 %d 个模型的定价（倍率、补全、缓存读、缓存写）",
			len(baseline["ModelRatio"])),
	})
}
