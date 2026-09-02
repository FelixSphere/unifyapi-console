package controller

// UNIFYAPI-FORK: the reconciliation endpoints.
//
// Revenue comes out of the consume-log ledger, cost is modelled from tokens x
// official list price x the channel's cost multiplier. See service/reconcile.go
// for why the two sides are built differently.

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// reconcileFromRequest does the parsing shared by the JSON and CSV endpoints.
func reconcileFromRequest(c *gin.Context) (service.ReconcileReport, bool, error) {
	groupBy, err := service.ParseGroupBy(c.Query("group_by"))
	if err != nil {
		return service.ReconcileReport{}, false, err
	}

	start, end, err := model.ParseReconcileWindow(c.Query("start"), c.Query("end"))
	if err != nil {
		return service.ReconcileReport{}, false, err
	}

	channelID, err := optionalInt(c.Query("channel_id"))
	if err != nil {
		return service.ReconcileReport{}, false, fmt.Errorf("invalid channel_id: %w", err)
	}

	rows, truncated, err := model.FetchReconcileUsage(model.ReconcileQuery{
		StartTimestamp: start,
		EndTimestamp:   end,
		ModelName:      c.Query("model"),
		Username:       c.Query("username"),
		ChannelID:      channelID,
		UserGroup:      c.Query("group"),
	})
	if err != nil {
		return service.ReconcileReport{}, false, err
	}

	return service.Reconcile(rows, groupBy), truncated, nil
}

func optionalInt(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	return strconv.Atoi(raw)
}

// GetReconciliation serves the report as JSON.
//
// The `invoiced` query parameter takes vendor totals as vendor:amount pairs
// (e.g. invoiced=anthropic:1240.55,openai:310.20) and is only meaningful with
// group_by=vendor, which is where the modelled cost can be compared line for
// line against what a vendor actually billed.
func GetReconciliation(c *gin.Context) {
	report, truncated, err := reconcileFromRequest(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}

	invoiced, err := parseInvoicedTotals(c.Query("invoiced"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}

	response := gin.H{
		"success":   true,
		"data":      report,
		"variances": service.CompareVendorInvoices(report, invoiced),
		"cost_basis": gin.H{
			"description":          "modelled: tokens x vendor official list price x per-channel cost multiplier",
			"snapshot_date":        ratio_setting.PricingSnapshotDate,
			"channel_cost_ratios":  ratio_setting.GetChannelCostRatioCopy(),
			"unconfigured_channel": "channels with no multiplier are costed at list price, which understates margin",
		},
	}
	if truncated {
		response["warning"] = fmt.Sprintf(
			"结果达到 %d 行上限，报表不完整。请缩小时间范围或加上 model/channel_id 过滤后重跑。",
			200_000)
	}
	c.JSON(http.StatusOK, response)
}

// parseInvoicedTotals reads `vendor:amount` pairs.
func parseInvoicedTotals(raw string) (map[string]float64, error) {
	if raw == "" {
		return nil, nil
	}
	out := map[string]float64{}
	for _, pair := range splitAndTrim(raw, ',') {
		vendor, amount, found := cutLast(pair, ':')
		if !found || vendor == "" {
			return nil, fmt.Errorf("invalid invoiced entry %q: expected vendor:amount", pair)
		}
		parsed, err := strconv.ParseFloat(amount, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid invoiced amount for %s: %q", vendor, amount)
		}
		out[vendor] = parsed
	}
	return out, nil
}

// ExportReconciliationCSV streams the report as CSV for a finance handoff.
//
// Money is written with four decimal places rather than two: per-model margins
// on cheap models are fractions of a cent, and rounding them to cents at export
// time turns a real loss into 0.00.
func ExportReconciliationCSV(c *gin.Context) {
	report, truncated, err := reconcileFromRequest(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}

	filename := fmt.Sprintf("unifyapi-reconciliation-%s-%s-by-%s.csv",
		c.Query("start"), c.Query("end"), report.GroupBy)
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)

	writer := csv.NewWriter(c.Writer)
	defer writer.Flush()

	if truncated {
		_ = writer.Write([]string{"# WARNING: row cap reached, this report is incomplete"})
	}
	_ = writer.Write([]string{
		string(report.GroupBy), "requests", "prompt_tokens", "cached_tokens", "completion_tokens",
		"revenue_usd", "cost_usd", "margin_usd", "margin_pct", "unpriced_requests", "unpriced_models",
	})

	writeLine := func(line service.ReconcileLine) {
		_ = writer.Write([]string{
			line.Label,
			strconv.FormatInt(line.Requests, 10),
			strconv.FormatInt(line.PromptTokens, 10),
			strconv.FormatInt(line.CachedTokens, 10),
			strconv.FormatInt(line.CompletionTokens, 10),
			strconv.FormatFloat(line.RevenueUSD, 'f', 4, 64),
			strconv.FormatFloat(line.CostUSD, 'f', 4, 64),
			strconv.FormatFloat(line.MarginUSD, 'f', 4, 64),
			strconv.FormatFloat(line.MarginPct, 'f', 2, 64),
			strconv.FormatInt(line.UnpricedRequests, 10),
			joinStrings(line.UnpricedModels, " "),
		})
	}

	for _, line := range report.Lines {
		writeLine(line)
	}
	writeLine(report.Total)
}

// GetChannelCost serves the per-channel upstream cost multipliers, plus enough
// context to actually set them.
//
// A channel id and a name are not enough: to type a purchasing discount you
// have to know which vendor contract the channel is, so the models it serves are
// resolved to catalog vendors. Channels serving models that are NOT catalogued
// are called out too -- their traffic cannot be costed, so any margin computed
// for that channel is overstated, and this is the screen where you would act on
// it.
func GetChannelCost(c *gin.Context) {
	configured := ratio_setting.GetChannelCostRatioCopy()

	channels, err := model.GetAllChannels(0, 0, true, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	type channelCost struct {
		ID              int      `json:"id"`
		Name            string   `json:"name"`
		Status          int      `json:"status"`
		Group           string   `json:"group"`
		CostRatio       float64  `json:"cost_ratio"`
		Configured      bool     `json:"configured"`
		Vendors         []string `json:"vendors"`
		ModelCount      int      `json:"model_count"`
		UncataloguedNum int      `json:"uncatalogued_count"`
		Uncatalogued    []string `json:"uncatalogued_models,omitempty"`
	}

	rows := make([]channelCost, 0, len(channels))
	for _, channel := range channels {
		_, configuredForChannel := configured[strconv.Itoa(channel.Id)]

		vendors := map[string]bool{}
		var uncatalogued []string
		var modelCount int
		for _, name := range splitAndTrim(channel.Models, ',') {
			modelCount++
			entry, ok := ratio_setting.CatalogEntryFor(name)
			switch {
			case !ok:
				uncatalogued = append(uncatalogued, name)
			case entry.Vendor != "":
				vendors[entry.Vendor] = true
			default:
				vendors["unlisted"] = true
			}
		}

		vendorList := make([]string, 0, len(vendors))
		for vendor := range vendors {
			vendorList = append(vendorList, vendor)
		}
		sort.Strings(vendorList)
		sort.Strings(uncatalogued)

		// Cap the sample: a misconfigured channel can carry hundreds of unknown
		// model names, and the count is the number that matters.
		sample := uncatalogued
		if len(sample) > 8 {
			sample = sample[:8]
		}

		rows = append(rows, channelCost{
			ID:              channel.Id,
			Name:            channel.Name,
			Status:          channel.Status,
			Group:           channel.Group,
			CostRatio:       ratio_setting.GetChannelCostRatio(channel.Id),
			Configured:      configuredForChannel,
			Vendors:         vendorList,
			ModelCount:      modelCount,
			UncataloguedNum: len(uncatalogued),
			Uncatalogued:    sample,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"channels": rows,
			"note": "cost_ratio multiplies the vendor's official list price. Unconfigured channels " +
				"default to 1 (we pay list), which is conservative: it can only understate margin. " +
				"This never affects customer billing -- routing is load balanced, so a channel's " +
				"cost must not reach a customer's invoice.",
		},
	})
}

// UpdateChannelCostRequest maps channel id (as a string, since it is a JSON
// object key) to a cost multiplier on list price.
type UpdateChannelCostRequest struct {
	CostRatios map[string]float64 `json:"cost_ratios"`
}

// UpdateChannelCost replaces the per-channel cost table.
func UpdateChannelCost(c *gin.Context) {
	var req UpdateChannelCostRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求参数格式错误：" + err.Error()})
		return
	}
	if req.CostRatios == nil {
		req.CostRatios = map[string]float64{}
	}

	if problems := ratio_setting.ValidateChannelCostRatios(req.CostRatios); len(problems) > 0 {
		messages := make([]string, 0, len(problems))
		for _, problem := range problems {
			messages = append(messages, problem.Error())
		}
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "渠道成本未保存", "errors": messages})
		return
	}

	encoded, err := common.Marshal(req.CostRatios)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := model.UpdateOptionAs("ChannelCostRatio", string(encoded), optionChangeActor(c)); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("已保存 %d 个渠道的上游成本倍率（仅用于对账，不影响客户计费）", len(req.CostRatios)),
	})
}

// splitAndTrim splits on a separator and drops empty fields, so a trailing
// comma in a hand-typed query string is not an error.
func splitAndTrim(raw string, sep rune) []string {
	var out []string
	current := ""
	flush := func() {
		trimmed := trimSpace(current)
		if trimmed != "" {
			out = append(out, trimmed)
		}
		current = ""
	}
	for _, r := range raw {
		if r == sep {
			flush()
			continue
		}
		current += string(r)
	}
	flush()
	return out
}

func trimSpace(value string) string {
	start, end := 0, len(value)
	for start < end && (value[start] == ' ' || value[start] == '\t') {
		start++
	}
	for end > start && (value[end-1] == ' ' || value[end-1] == '\t') {
		end--
	}
	return value[start:end]
}

// cutLast splits on the LAST separator, so a vendor id containing the separator
// still parses and only the amount is taken from the tail.
func cutLast(value string, sep byte) (before, after string, found bool) {
	for i := len(value) - 1; i >= 0; i-- {
		if value[i] == sep {
			return trimSpace(value[:i]), trimSpace(value[i+1:]), true
		}
	}
	return value, "", false
}

func joinStrings(values []string, sep string) string {
	out := ""
	for i, value := range values {
		if i > 0 {
			out += sep
		}
		out += value
	}
	return out
}
