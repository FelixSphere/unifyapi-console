package controller

// UNIFYAPI-FORK: safe admin API for customer-group x model contracts.
//
// The UI saves one customer at a time. The server merges that one row into the
// current table while holding a mutex, so editing GenAI cannot erase UnifyAI's
// prices. The stored value remains one option row so it participates in the
// pricing-history safety net.

import (
	"fmt"
	"net/http"
	"sort"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

var groupModelPricingUpdateMu sync.Mutex

type groupModelPricingModel struct {
	Model                 string  `json:"model"`
	Vendor                string  `json:"vendor"`
	OfficialInputUSD      float64 `json:"official_input_usd"`
	OfficialOutputUSD     float64 `json:"official_output_usd"`
	OfficialCacheReadUSD  float64 `json:"official_cache_read_usd,omitempty"`
	OfficialCacheWriteUSD float64 `json:"official_cache_write_usd,omitempty"`
}

type updateGroupModelPricingRequest struct {
	Group     string             `json:"group"`
	Discounts map[string]float64 `json:"discounts"`
}

func GetGroupModelPricing(c *gin.Context) {
	groupRatios := ratio_setting.GetGroupRatioCopy()
	groups := make([]string, 0, len(groupRatios))
	for group := range groupRatios {
		groups = append(groups, group)
	}
	sort.Strings(groups)

	catalog := ratio_setting.Catalog()
	models := make([]groupModelPricingModel, 0, len(catalog))
	for _, entry := range catalog {
		models = append(models, groupModelPricingModel{
			Model:                 entry.Model,
			Vendor:                entry.Vendor,
			OfficialInputUSD:      entry.InputUSD,
			OfficialOutputUSD:     entry.OutputUSD,
			OfficialCacheReadUSD:  entry.CacheReadUSD,
			OfficialCacheWriteUSD: entry.CacheWriteUSD,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"groups":             groups,
			"models":             models,
			"discounts":          ratio_setting.GetGroupModelDiscountCopy(),
			"fallback_discounts": ratio_setting.GetModelDiscountCopy(),
			"group_ratios":       groupRatios,
		},
	})
}

func UpdateGroupModelPricing(c *gin.Context) {
	var req updateGroupModelPricingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求参数格式错误：" + err.Error()})
		return
	}
	if !ratio_setting.ContainsGroupRatio(req.Group) {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("客户组 %q 不存在，请先在 Group Pricing 中保存该组", req.Group)})
		return
	}
	if req.Discounts == nil {
		req.Discounts = map[string]float64{}
	}
	candidate := map[string]map[string]float64{req.Group: req.Discounts}
	if problems := ratio_setting.ValidateGroupModelDiscounts(candidate); len(problems) > 0 {
		messages := make([]string, 0, len(problems))
		for _, problem := range problems {
			messages = append(messages, problem.Error())
		}
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "客户模型价格未保存", "errors": messages})
		return
	}

	groupModelPricingUpdateMu.Lock()
	defer groupModelPricingUpdateMu.Unlock()

	all := ratio_setting.GetGroupModelDiscountCopy()
	if len(req.Discounts) == 0 {
		delete(all, req.Group)
	} else {
		copied := make(map[string]float64, len(req.Discounts))
		for modelName, ratio := range req.Discounts {
			copied[modelName] = ratio
		}
		all[req.Group] = copied
	}
	encoded, err := common.Marshal(all)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := model.UpdateOptionAs("GroupModelDiscount", string(encoded), optionChangeActor(c)); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("已保存 %s 的 %d 个模型价格；未列出的模型继续使用全局定价", req.Group, len(req.Discounts)),
	})
}
