package controller

// UNIFYAPI-FORK: safe admin surface for company x model contracts. Official
// prices remain catalog-owned; the admin edits only a sell-price multiplier and
// binds dedicated one-model channels.

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

type tenantModelContractRequest struct {
	TenantId   int     `json:"tenant_id"`
	Model      string  `json:"model"`
	Discount   float64 `json:"discount"`
	ChannelIds []int   `json:"channel_ids"`
	Enabled    bool    `json:"enabled"`
}

type tenantModelContractModeRequest struct {
	TenantId int  `json:"tenant_id"`
	Strict   bool `json:"strict"`
}

type tenantModelContractRow struct {
	Id                int     `json:"id"`
	TenantId          int     `json:"tenant_id"`
	Model             string  `json:"model"`
	Discount          float64 `json:"discount"`
	Enabled           bool    `json:"enabled"`
	ChannelIds        []int   `json:"channel_ids"`
	OfficialInputUSD  float64 `json:"official_input_usd"`
	OfficialOutputUSD float64 `json:"official_output_usd"`
	CustomerInputUSD  float64 `json:"customer_input_usd"`
	CustomerOutputUSD float64 `json:"customer_output_usd"`
	UpdatedAt         int64   `json:"updated_at"`
}

func GetTenantModelContracts(c *gin.Context) {
	tenants, err := model.ListTenantModelContractTenants()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	channels, err := model.ListTenantModelContractChannels()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	contracts, err := model.ListTenantModelContracts()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	contractRows := make([]tenantModelContractRow, 0, len(contracts))
	for _, contract := range contracts {
		entry, ok := ratio_setting.CatalogEntryFor(contract.Model)
		if !ok {
			continue
		}
		channelIds := make([]int, 0, len(contract.Channels))
		for _, binding := range contract.Channels {
			channelIds = append(channelIds, binding.ChannelId)
		}
		sort.Ints(channelIds)
		contractRows = append(contractRows, tenantModelContractRow{
			Id:                contract.Id,
			TenantId:          contract.TenantId,
			Model:             contract.Model,
			Discount:          contract.Discount,
			Enabled:           contract.Enabled,
			ChannelIds:        channelIds,
			OfficialInputUSD:  entry.InputUSD,
			OfficialOutputUSD: entry.OutputUSD,
			CustomerInputUSD:  entry.InputUSD * contract.Discount,
			CustomerOutputUSD: entry.OutputUSD * contract.Discount,
			UpdatedAt:         contract.UpdatedAt,
		})
	}

	catalog := ratio_setting.Catalog()
	modelRows := make([]gin.H, 0, len(catalog))
	for _, entry := range catalog {
		modelRows = append(modelRows, gin.H{
			"model":               entry.Model,
			"vendor":              entry.Vendor,
			"official_input_usd":  entry.InputUSD,
			"official_output_usd": entry.OutputUSD,
		})
	}

	channelRows := make([]gin.H, 0, len(channels))
	channelContractIds := make(map[int]int)
	for _, contract := range contracts {
		for _, binding := range contract.Channels {
			channelContractIds[binding.ChannelId] = contract.Id
		}
	}
	for i := range channels {
		channel := &channels[i]
		models := splitNonEmpty(channel.Models)
		priority := int64(0)
		if channel.Priority != nil {
			priority = *channel.Priority
		}
		channelRows = append(channelRows, gin.H{
			"id":                channel.Id,
			"name":              channel.Name,
			"status":            channel.Status,
			"models":            models,
			"group":             channel.Group,
			"priority":          priority,
			"weight":            channel.GetWeight(),
			"single_model":      len(models) == 1,
			"bound_contract_id": channelContractIds[channel.Id],
		})
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"tenants":   tenants,
		"models":    modelRows,
		"channels":  channelRows,
		"contracts": contractRows,
		"formula":   "official_price × customer_model_discount (global model discount and legacy group ratio are not applied)",
	}})
}

func UpsertTenantModelContract(c *gin.Context) {
	var req tenantModelContractRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求参数格式错误：" + err.Error()})
		return
	}
	req.Model = strings.TrimSpace(req.Model)
	problems := validateTenantModelContractRequest(req)
	if len(problems) > 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "合同未保存", "errors": problems})
		return
	}

	contract := &model.TenantModelContract{
		TenantId: req.TenantId,
		Model:    req.Model,
		Discount: req.Discount,
		Enabled:  req.Enabled,
	}
	if err := model.UpsertTenantModelContract(contract, req.ChannelIds); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	recordManageAudit(c, "pricing.customer_model.upsert", map[string]interface{}{
		"id": contract.Id, "tenant_id": req.TenantId, "model": req.Model,
		"discount": req.Discount, "channel_ids": req.ChannelIds, "enabled": req.Enabled,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("已保存客户 %d 的 %s 合同；售价为官方报价的 %.4gx", req.TenantId, req.Model, req.Discount),
		"data":    gin.H{"id": contract.Id},
	})
}

func DeleteTenantModelContract(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "无效的合同 ID"})
		return
	}
	if err := model.DeleteTenantModelContract(id); err != nil {
		status := http.StatusOK
		if errors.Is(err, model.ErrTenantModelContractNotFound) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"success": false, "message": err.Error()})
		return
	}
	recordManageAudit(c, "pricing.customer_model.delete", map[string]interface{}{"id": id})
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "客户模型合同已删除；该模型恢复旧定价和路由逻辑"})
}

func UpdateTenantModelContractMode(c *gin.Context) {
	var req tenantModelContractModeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求参数格式错误：" + err.Error()})
		return
	}
	if req.TenantId <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "必须选择客户公司"})
		return
	}
	if _, err := model.GetTenantById(req.TenantId); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "客户公司不存在"})
		return
	}
	if req.Strict {
		contracts, err := model.ListEnabledTenantModelContractsForTenant(req.TenantId)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
			return
		}
		if len(contracts) == 0 {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": model.ErrStrictModelContractsRequireEnabledContract.Error()})
			return
		}
	}
	if err := model.SetTenantStrictModelContracts(req.TenantId, req.Strict); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	recordManageAudit(c, "pricing.customer_model.mode", map[string]interface{}{
		"tenant_id": req.TenantId, "strict": req.Strict,
	})
	message := "已关闭专属合同模式；未配置模型会继续使用默认定价和渠道"
	if req.Strict {
		message = "已启用专属合同模式；未配置合同的模型将被隐藏并拒绝调用"
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": message})
}

func validateTenantModelContractRequest(req tenantModelContractRequest) []string {
	problems := make([]string, 0)
	currentContractId := 0
	if req.TenantId <= 0 {
		problems = append(problems, "必须选择客户公司")
	} else {
		var tenant model.Tenant
		if err := model.DB.Select("id").First(&tenant, req.TenantId).Error; err != nil {
			problems = append(problems, "客户公司不存在")
		}
	}
	if _, ok := ratio_setting.CatalogEntryFor(req.Model); !ok {
		problems = append(problems, "模型不在官方价格目录中")
	} else if req.TenantId > 0 {
		if existing, err := model.GetTenantModelContract(req.TenantId, req.Model, false); err == nil {
			currentContractId = existing.Id
		} else if !errors.Is(err, model.ErrTenantModelContractNotFound) {
			problems = append(problems, "读取现有合同失败："+err.Error())
		}
	}
	if req.Discount <= 0 || req.Discount > 10 {
		problems = append(problems, "折扣倍率必须大于 0 且不超过 10")
	}
	channelIds := uniquePositiveChannelIds(req.ChannelIds)
	if len(channelIds) != len(req.ChannelIds) {
		problems = append(problems, "渠道列表包含重复或无效 ID")
	}
	if req.Enabled && len(channelIds) == 0 {
		problems = append(problems, "启用合同时至少绑定一个专属渠道")
		return problems
	}
	channels, err := model.GetChannelsForTenantModelContract(channelIds)
	if err != nil {
		return append(problems, "读取渠道失败："+err.Error())
	}
	if len(channels) != len(channelIds) {
		problems = append(problems, "有渠道不存在")
	}
	bindings, err := model.GetTenantModelChannelBindings(channelIds)
	if err != nil {
		problems = append(problems, "读取渠道绑定失败："+err.Error())
	} else {
		for _, binding := range bindings {
			if binding.ContractId != currentContractId {
				problems = append(problems, fmt.Sprintf("渠道 #%d 已属于另一个客户模型合同", binding.ChannelId))
			}
		}
	}
	for i := range channels {
		channel := &channels[i]
		if !model.ChannelCarriesOnlyModel(channel, req.Model) {
			problems = append(problems, fmt.Sprintf("渠道 #%d %s 必须且只能包含模型 %s", channel.Id, channel.Name, req.Model))
		}
		if req.Enabled && channel.Status != common.ChannelStatusEnabled {
			problems = append(problems, fmt.Sprintf("渠道 #%d %s 已禁用，不能启用合同", channel.Id, channel.Name))
		}
	}
	return problems
}

func splitNonEmpty(value string) []string {
	parts := make([]string, 0)
	for _, raw := range strings.Split(value, ",") {
		if part := strings.TrimSpace(raw); part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

func uniquePositiveChannelIds(values []int) []int {
	seen := map[int]struct{}{}
	out := make([]int, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
