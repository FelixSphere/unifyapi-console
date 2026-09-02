package controller

// UNIFYAPI-FORK: the admin surface for the pricing baseline and the per-model
// customer discount.
//
// Upstream's pricing page edits raw billing ratios, which is the practice this
// fork is moving away from: a ratio typed by hand is unverifiable, and saving
// one writes an options row that silently replaces the whole code baseline. So
// these endpoints expose the three prices separately instead --
//
//	official  the vendor's list price, from the catalog, read-only here
//	discount  our per-model customer discount, editable
//	effective what each group actually pays, derived
//
// -- and an admin sets the middle one only.

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// baselineRow is one model as the admin pricing page sees it: the official
// price, the discount we apply, and the resulting price for every group.
type baselineRow struct {
	Model                 string                `json:"model"`
	Vendor                string                `json:"vendor"`
	UpstreamModel         string                `json:"upstream_model,omitempty"`
	Unverified            bool                  `json:"unverified"`
	OfficialInputUSD      float64               `json:"official_input_usd"`
	OfficialOutputUSD     float64               `json:"official_output_usd"`
	OfficialCacheReadUSD  float64               `json:"official_cache_read_usd,omitempty"`
	OfficialCacheWriteUSD float64               `json:"official_cache_write_usd,omitempty"`
	Discount              float64               `json:"discount"`
	ModelRatio            float64               `json:"model_ratio"`
	CompletionRatio       float64               `json:"completion_ratio"`
	GroupPrices           map[string]groupPrice `json:"group_prices"`
}

// groupPrice is the customer-facing price for one group, in USD per 1M tokens.
type groupPrice struct {
	GroupRatio         float64  `json:"group_ratio"`
	CustomerMultiplier *float64 `json:"customer_multiplier,omitempty"`
	InputUSD           float64  `json:"input_usd"`
	OutputUSD          float64  `json:"output_usd"`
}

// GetPricingBaseline serves the catalog, the discounts, the resulting
// per-group prices, and any drift between the code baseline and what is
// actually billing.
//
// The shadow list is the important part operationally: because the options
// loader replaces the ratio map wholesale, a single save on upstream's pricing
// page discards the entire code baseline, and nothing else in the product would
// tell you.
func GetPricingBaseline(c *gin.Context) {
	groupRatios := ratio_setting.GetGroupRatioCopy()
	groups := make([]string, 0, len(groupRatios))
	for group := range groupRatios {
		groups = append(groups, group)
	}
	sort.Strings(groups)

	catalog := ratio_setting.Catalog()
	rows := make([]baselineRow, 0, len(catalog))
	for _, entry := range catalog {
		discount := ratio_setting.GetModelDiscount(entry.Model)
		row := baselineRow{
			Model:                 entry.Model,
			Vendor:                entry.Vendor,
			UpstreamModel:         entry.UpstreamModel,
			Unverified:            entry.Unverified,
			OfficialInputUSD:      entry.InputUSD,
			OfficialOutputUSD:     entry.OutputUSD,
			OfficialCacheReadUSD:  entry.CacheReadUSD,
			OfficialCacheWriteUSD: entry.CacheWriteUSD,
			Discount:              discount,
			ModelRatio:            entry.ModelRatio() * discount,
			CompletionRatio:       entry.CompletionRatio(),
			GroupPrices:           make(map[string]groupPrice, len(groups)),
		}
		for _, group := range groups {
			factor := discount * groupRatios[group]
			var customerMultiplier *float64
			if negotiated, ok := ratio_setting.GetGroupModelDiscount(group, entry.Model); ok {
				factor = negotiated
				negotiatedCopy := negotiated
				customerMultiplier = &negotiatedCopy
			}
			row.GroupPrices[group] = groupPrice{
				GroupRatio:         groupRatios[group],
				CustomerMultiplier: customerMultiplier,
				InputUSD:           entry.InputUSD * factor,
				OutputUSD:          entry.OutputUSD * factor,
			}
		}
		rows = append(rows, row)
	}

	shadows := ratio_setting.DetectBaselineShadow()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"models":         rows,
			"group_ratios":   groupRatios,
			"snapshot_date":  ratio_setting.PricingSnapshotDate,
			"discounts":      ratio_setting.GetModelDiscountCopy(),
			"shadows":        shadows,
			"shadow_warning": baselineShadowWarning(len(shadows)),
		},
	})
}

func baselineShadowWarning(count int) string {
	if count == 0 {
		return ""
	}
	return fmt.Sprintf(
		"%d 项定价与代码基线不一致：数据库 options 里存在 ModelRatio 等行，正在覆盖 unifyapi_catalog.go。"+
			"这通常是有人在旧的「模型定价」页按了保存。修复：执行 seed-pricing.sql 删除这些行并重启。", count)
}

// UpdatePricingDiscountRequest is the admin's edit: model -> multiplier on the
// vendor's official price. Omitting a model means it is sold at list price;
// there is deliberately no way to express "discount" by editing the official
// price itself.
type UpdatePricingDiscountRequest struct {
	Discounts map[string]float64 `json:"discounts"`
}

// UpdatePricingDiscount replaces the per-model customer discount table.
//
// Replace, not merge: a discount table you can only add to is impossible to
// reason about, and "remove the discount" has to be expressible. Dropping a
// model from the payload restores its official price.
func UpdatePricingDiscount(c *gin.Context) {
	var req UpdatePricingDiscountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求参数格式错误：" + err.Error()})
		return
	}
	if req.Discounts == nil {
		req.Discounts = map[string]float64{}
	}

	problems, markups := ratio_setting.ValidateModelDiscounts(req.Discounts)
	if len(problems) > 0 {
		messages := make([]string, 0, len(problems))
		for _, problem := range problems {
			messages = append(messages, problem.Error())
		}
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "折扣未保存", "errors": messages})
		return
	}

	encoded, err := common.Marshal(req.Discounts)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "序列化折扣失败：" + err.Error()})
		return
	}

	// UpdateOption persists and then dispatches into
	// UpdateModelDiscountByJSONString, which rebuilds the billing ratios. So
	// the ratio map and the stored discount cannot disagree.
	if err := model.UpdateOptionAs("ModelDiscount", string(encoded), optionChangeActor(c)); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("已保存 %d 个模型的折扣，其余 %d 个按官方报价销售",
			len(req.Discounts), len(ratio_setting.CatalogModels())-len(req.Discounts)),
		"markups": markups,
	})
}
