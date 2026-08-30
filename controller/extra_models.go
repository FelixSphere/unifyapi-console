package controller

// UNIFYAPI-FORK: the admin endpoints for extra model pricing.
//
// These exist so that selling a model a vendor launched this morning is a
// console action rather than a pull request and a deploy. The safety is in the
// layer below -- ValidateExtraModels refuses an entry that names a catalogued
// model, so nothing typed here can shadow a price that has provenance and a
// drift check. See setting/ratio_setting/unifyapi_extra_models.go.

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// extraModelRow is one admin-added price plus what it currently bills at, so
// the screen never has to re-derive a ratio the server already knows.
type extraModelRow struct {
	Model         string  `json:"model"`
	Vendor        string  `json:"vendor,omitempty"`
	Note          string  `json:"note,omitempty"`
	InputUSD      float64 `json:"input_usd"`
	OutputUSD     float64 `json:"output_usd"`
	CacheReadUSD  float64 `json:"cache_read_usd,omitempty"`
	CacheWriteUSD float64 `json:"cache_write_usd,omitempty"`

	// Discount and the ratios are the effective billing values, so an operator
	// can see that what they typed became what the relay charges without
	// opening another page.
	Discount        float64 `json:"discount"`
	ModelRatio      float64 `json:"model_ratio"`
	CompletionRatio float64 `json:"completion_ratio"`
}

// GetExtraModels serves the admin-added prices, plus the catalogued names the
// screen must refuse to shadow.
func GetExtraModels(c *gin.Context) {
	extras := ratio_setting.ExtraModels()

	names := make([]string, 0, len(extras))
	for name := range extras {
		names = append(names, name)
	}
	sort.Strings(names)

	rows := make([]extraModelRow, 0, len(names))
	for _, name := range names {
		extra := extras[name]
		entry, _ := ratio_setting.CatalogEntryFor(name)
		discount := ratio_setting.GetModelDiscount(name)
		rows = append(rows, extraModelRow{
			Model:           name,
			Vendor:          extra.Vendor,
			Note:            extra.Note,
			InputUSD:        extra.InputUSD,
			OutputUSD:       extra.OutputUSD,
			CacheReadUSD:    extra.CacheReadUSD,
			CacheWriteUSD:   extra.CacheWriteUSD,
			Discount:        discount,
			ModelRatio:      entry.ModelRatio() * discount,
			CompletionRatio: entry.CompletionRatio(),
		})
	}

	// The catalogued names go with the payload so the form can say "this one is
	// already priced, use a discount" as you type, instead of after a failed
	// save. Same rule as the server enforces, just earlier.
	compiled := ratio_setting.CompiledCatalog()
	catalogued := make([]string, 0, len(compiled))
	for _, entry := range compiled {
		catalogued = append(catalogued, entry.Model)
	}
	sort.Strings(catalogued)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"models":            rows,
			"catalogued_models": catalogued,
			"note": "补充定价叠加在代码目录之上，不会覆盖目录里已有的任何价格。" +
				"目录里已有的模型请用「官方报价与折扣」页调整售价。",
		},
	})
}

// UpdateExtraModelsRequest replaces the whole extras table.
//
// Whole-table rather than per-model, because that is what the underlying option
// row is: sending one model would be indistinguishable from sending a table
// with one model in it, and the difference between those two is 54 models.
type UpdateExtraModelsRequest struct {
	Models map[string]ratio_setting.ExtraModel `json:"models"`
}

// UpdateExtraModels validates and stores the extras table.
func UpdateExtraModels(c *gin.Context) {
	var req UpdateExtraModelsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求参数格式错误：" + err.Error()})
		return
	}
	if req.Models == nil {
		req.Models = map[string]ratio_setting.ExtraModel{}
	}

	// Validated here as well as in the setting layer so the admin gets every
	// bad row at once, rather than the first one wrapped in a save failure.
	if problems := ratio_setting.ValidateExtraModels(req.Models); len(problems) > 0 {
		messages := make([]string, 0, len(problems))
		for _, problem := range problems {
			messages = append(messages, problem.Error())
		}
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "补充定价未保存", "errors": messages})
		return
	}

	encoded, err := common.Marshal(req.Models)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	// Through UpdateOption, so the previous table is snapshotted before it is
	// replaced -- see model/pricing_config_history.go.
	if err := model.UpdateOptionAs("ExtraModelPricing", string(encoded), optionChangeActor(c)); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": pluralisedSaveMessage(len(req.Models)),
	})
}

func pluralisedSaveMessage(count int) string {
	if count == 0 {
		return "已清空补充定价，这些模型将不再可售"
	}
	return fmt.Sprintf("已保存 %d 个补充定价模型，立即生效，无需发版", count)
}

// LookupModelPrice serves every published price for a model name, so adding a
// model is "type the name, press sync" instead of retyping four numbers off a
// vendor page -- which is where a decimal slips.
//
// It returns ALL matches rather than picking one. The same id is listed by many
// providers at different prices, and choosing silently would make the console
// invent a commercial decision. First-party listings sort first; a human picks.
func LookupModelPrice(c *gin.Context) {
	query := c.Query("model")

	candidates, err := service.LookupModelPrice(query)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if len(candidates) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": fmt.Sprintf(
				"models.dev 上没有找到 %s。可能是名字不对，也可能这个模型按次/按秒计费而没有 token 价——那种情况需要手填。", query),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"candidates": candidates},
		"note": "价格来自 models.dev 聚合，可能滞后于厂商实际调价。" +
			"同步进来的价格不会被自动跟踪，涨价时需要你自己复核。",
	})
}
