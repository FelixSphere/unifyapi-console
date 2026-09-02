package controller

// UNIFYAPI-FORK: the settlement endpoints -- billing a customer, and paying an
// upstream.
//
// One rule runs through all of this: OUR figure is always recomputed from the
// consume log, never accepted from the caller. The client supplies the
// counterparty's number (their invoice), a note and a status; everything on our
// side of the comparison is derived here. A settlement screen that let the
// browser post an amount would be a screen that can be talked into agreeing
// with any invoice.

import (
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// statementsForWindow builds every statement of one kind for a period.
func statementsForWindow(kind service.StatementKind, start, end string) ([]service.Statement, bool, error) {
	from, to, err := model.ParseReconcileWindow(start, end)
	if err != nil {
		return nil, false, err
	}
	rows, truncated, err := model.FetchReconcileUsage(model.ReconcileQuery{
		StartTimestamp: from,
		EndTimestamp:   to,
	})
	if err != nil {
		return nil, false, err
	}
	return service.BuildStatements(rows, kind, start, end), truncated, nil
}

// settlementRow pairs what the period looks like NOW with what was frozen when
// it was issued. Both are sent, because the interesting case is that they
// differ: a vendor cost re-modelled after a rate change no longer matches the
// figure that was actually paid, and only showing one of the two hides it.
type settlementRow struct {
	Statement  service.Statement `json:"statement"`
	Settlement *model.Settlement `json:"settlement,omitempty"`
	// IssuedStatement is the immutable document that was actually issued. The
	// live statement remains beside it solely to expose drift.
	IssuedStatement *service.Statement `json:"issued_statement,omitempty"`

	// DriftUSD is live minus frozen. Non-zero means the pricing configuration
	// moved after this was issued.
	DriftUSD float64 `json:"drift_usd,omitempty"`

	// VarianceUSD is the counterparty's invoice minus ours. Vendor side only.
	VarianceUSD float64 `json:"variance_usd,omitempty"`
	VariancePct float64 `json:"variance_pct,omitempty"`
}

// GetSettlements serves one period's statements for one side of the business,
// each joined to its issued settlement if it has one.
func GetSettlements(c *gin.Context) {
	kind, ok := service.ParseStatementKind(c.Query("kind"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "unknown kind; expected customer or vendor",
		})
		return
	}

	start, end := c.Query("start"), c.Query("end")
	statements, truncated, err := statementsForWindow(kind, start, end)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}

	saved, err := model.ListSettlements(string(kind), start, end, 0)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	byParty := make(map[string]*model.Settlement, len(saved))
	for _, settlement := range saved {
		byParty[settlement.Counterparty] = settlement
	}

	rows := make([]settlementRow, 0, len(statements))
	displayedStatements := make([]service.Statement, 0, len(statements))
	for _, statement := range statements {
		row := settlementRow{Statement: statement}
		if settlement, found := byParty[statement.Counterparty]; found {
			row.Settlement = settlement
			var issued service.Statement
			if err := common.Unmarshal([]byte(settlement.StatementJSON), &issued); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{
					"success": false,
					"message": "frozen statement is unreadable: " + err.Error(),
				})
				return
			}
			row.IssuedStatement = &issued
			row.DriftUSD = statement.AmountUSD - settlement.AmountUSD
			row.VarianceUSD = settlement.VarianceUSD()
			if settlement.InvoiceRecorded && settlement.AmountUSD != 0 {
				row.VariancePct = row.VarianceUSD / settlement.AmountUSD * 100
			}
			delete(byParty, statement.Counterparty)
		}
		rows = append(rows, row)
		if row.IssuedStatement != nil {
			displayedStatements = append(displayedStatements, *row.IssuedStatement)
		} else {
			displayedStatements = append(displayedStatements, statement)
		}
	}

	// A settlement whose counterparty has no traffic left in the period is not
	// dropped: it is usually the interesting one -- a vendor invoiced for
	// traffic our logs no longer attribute to them, or a customer billed for a
	// period that has since been re-scoped.
	orphaned := make([]*model.Settlement, 0, len(byParty))
	for _, settlement := range saved {
		if _, still := byParty[settlement.Counterparty]; still {
			orphaned = append(orphaned, settlement)
		}
	}

	response := gin.H{
		"success": true,
		"data": gin.H{
			"kind":         kind,
			"period_start": start,
			"period_end":   end,
			"rows":         rows,
			"totals":       service.SumStatements(displayedStatements),
			"orphaned":     orphaned,
		},
	}

	if kind == service.StatementKindVendor {
		response["cost_basis"] = gin.H{
			"snapshot_date":       ratio_setting.PricingSnapshotDate,
			"channel_cost_ratios": ratio_setting.GetChannelCostRatioCopy(),
			"description":         "modelled: tokens x vendor official list price x per-channel purchasing ratio",
		}
	}

	if truncated {
		response["warning"] = fmt.Sprintf(
			"结果达到 %d 行上限，本期账单不完整，请勿据此开票。请缩短时间范围后重跑。", 200_000)
	}
	c.JSON(http.StatusOK, response)
}

// IssueSettlementRequest is what the screen may set. Our own amount is
// deliberately absent -- see the file header.
type IssueSettlementRequest struct {
	Kind         string `json:"kind"`
	Counterparty string `json:"counterparty"`
	Start        string `json:"start"`
	End          string `json:"end"`

	InvoicedUSD     float64 `json:"invoiced_usd"`
	InvoiceRecorded bool    `json:"invoice_recorded"`
	Status          string  `json:"status"`
	Note            string  `json:"note"`
}

// IssueSettlement freezes one counterparty's statement for a period.
func IssueSettlement(c *gin.Context) {
	var req IssueSettlementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求参数格式错误：" + err.Error()})
		return
	}

	kind, ok := service.ParseStatementKind(req.Kind)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "kind 必须是 customer 或 vendor"})
		return
	}
	if req.Counterparty == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "缺少结算对象"})
		return
	}
	if !validSettlementStatus(req.Status) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "未知的状态：" + req.Status})
		return
	}
	if err := service.ValidateClosedCalendarMonth(req.Start, req.End, time.Now()); err != nil {
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": err.Error()})
		return
	}

	statements, truncated, err := statementsForWindow(kind, req.Start, req.End)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	if truncated {
		// Freezing a truncated statement writes a number that is wrong by an
		// unknown amount and then makes it authoritative. Refuse.
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "本期数据超过行数上限，账单不完整，拒绝开具。请缩短结算周期后重试。",
		})
		return
	}

	var found *service.Statement
	for i := range statements {
		if statements[i].Counterparty == req.Counterparty {
			found = &statements[i]
			break
		}
	}
	if found == nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": fmt.Sprintf("在 %s 至 %s 期间没有 %s 的用量，无法开具账单。",
				req.Start, req.End, req.Counterparty),
		})
		return
	}

	encoded, err := common.Marshal(found)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	settlement := &model.Settlement{
		Kind:                string(kind),
		Counterparty:        found.Counterparty,
		Label:               found.Label,
		PeriodStart:         req.Start,
		PeriodEnd:           req.End,
		AmountUSD:           found.AmountUSD,
		InvoicedUSD:         req.InvoicedUSD,
		InvoiceRecorded:     req.InvoiceRecorded,
		Status:              req.Status,
		Note:                req.Note,
		StatementJSON:       string(encoded),
		PricingSnapshotDate: ratio_setting.PricingSnapshotDate,
	}
	saved, err := model.CreateSettlement(settlement)
	if err != nil {
		if errors.Is(err, model.ErrSettlementAlreadyIssued) {
			c.JSON(http.StatusConflict, gin.H{"success": false, "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	message := fmt.Sprintf("已记录 %s 在 %s 至 %s 的结算：$%.4f",
		saved.Label, saved.PeriodStart, saved.PeriodEnd, saved.AmountUSD)
	if !found.Complete() {
		// Said on the way out, not buried in the row: a vendor statement with
		// unpriced traffic understates what we owe, and paying from it
		// underpays.
		message += fmt.Sprintf("。注意：其中 %d 次请求的模型没有目录价，金额被低估。",
			found.UnpricedRequests)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": message, "data": saved})
}

// UpdateSettlementRequest records the counterparty's side after the fact.
type UpdateSettlementRequest struct {
	InvoicedUSD     float64 `json:"invoiced_usd"`
	InvoiceRecorded bool    `json:"invoice_recorded"`
	Status          string  `json:"status"`
	Note            string  `json:"note"`
}

// UpdateSettlement edits an issued settlement's counterparty-side fields.
//
// The frozen statement is not touched. Typing in an invoice is not a reason to
// re-model the period -- that would replace the figure the invoice is being
// compared against, and the comparison is the entire point.
func UpdateSettlement(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "无效的结算单编号"})
		return
	}
	var req UpdateSettlementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求参数格式错误：" + err.Error()})
		return
	}
	if !validSettlementStatus(req.Status) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "未知的状态：" + req.Status})
		return
	}

	existing, err := model.GetSettlement(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "结算单不存在"})
		return
	}

	existing.InvoicedUSD = req.InvoicedUSD
	existing.InvoiceRecorded = req.InvoiceRecorded
	existing.Note = req.Note
	if req.Status != "" {
		existing.Status = req.Status
	}

	saved, err := model.UpdateSettlementCounterparty(existing)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "已更新", "data": saved})
}

// DeleteSettlementRecord is retained for API compatibility but issued records
// are immutable. Operators void a mistaken record so the audit trail survives.
func DeleteSettlementRecord(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "无效的结算单编号"})
		return
	}
	if err := model.DeleteSettlement(id); err != nil {
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "已删除该结算记录"})
}

func validSettlementStatus(status string) bool {
	switch status {
	case "", model.SettlementStatusIssued, model.SettlementStatusSettled, model.SettlementStatusVoid:
		return true
	default:
		return false
	}
}

// ExportSettlementCSV streams one counterparty's statement as the line-item
// document you attach to an invoice.
//
// Money is written at four decimal places, matching the reconciliation export:
// per-model amounts on cheap models are fractions of a cent, and rounding to
// cents at export time turns real usage into 0.00.
func ExportSettlementCSV(c *gin.Context) {
	kind, ok := service.ParseStatementKind(c.Query("kind"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "unknown kind"})
		return
	}
	start, end := c.Query("start"), c.Query("end")
	counterparty := c.Query("counterparty")

	statements, truncated, err := statementsForWindow(kind, start, end)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	if counterparty != "" {
		filtered := statements[:0:0]
		for _, statement := range statements {
			if statement.Counterparty == counterparty {
				filtered = append(filtered, statement)
			}
		}
		statements = filtered
	}

	saved, err := model.ListSettlements(string(kind), start, end, 0)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	byParty := make(map[string]*model.Settlement, len(saved))
	for _, settlement := range saved {
		byParty[settlement.Counterparty] = settlement
	}
	type exportDocument struct {
		Statement  service.Statement
		Settlement *model.Settlement
	}
	documents := make([]exportDocument, 0, len(statements)+len(saved))
	seen := make(map[string]bool, len(statements))
	for _, live := range statements {
		document := exportDocument{Statement: live, Settlement: byParty[live.Counterparty]}
		if document.Settlement != nil {
			var frozen service.Statement
			if err := common.Unmarshal([]byte(document.Settlement.StatementJSON), &frozen); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "frozen statement is unreadable: " + err.Error()})
				return
			}
			document.Statement = frozen
		}
		documents = append(documents, document)
		seen[live.Counterparty] = true
	}
	// Preserve an issued document even when the live ledger no longer produces
	// that counterparty. Dropping it from an export would erase the very drift
	// the settlement table is meant to expose.
	for _, settlement := range saved {
		if seen[settlement.Counterparty] || (counterparty != "" && settlement.Counterparty != counterparty) {
			continue
		}
		var frozen service.Statement
		if err := common.Unmarshal([]byte(settlement.StatementJSON), &frozen); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "frozen statement is unreadable: " + err.Error()})
			return
		}
		documents = append(documents, exportDocument{Statement: frozen, Settlement: settlement})
	}

	name := counterparty
	if name == "" {
		name = "all"
	}
	filename := fmt.Sprintf("unifyapi-%s-statement-%s-%s-%s.csv", kind, safeFilenamePart(name), start, end)
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)

	writer := csv.NewWriter(c.Writer)
	defer writer.Flush()

	if truncated {
		_ = writer.Write([]string{"# WARNING: row cap reached, this statement is incomplete — do not invoice from it"})
	}
	for _, document := range documents {
		if document.Settlement == nil {
			_ = writer.Write([]string{"# DRAFT_NOT_ISSUED: preview only; issue the closed month before sending this as an official statement"})
			break
		}
	}
	if kind == service.StatementKindVendor {
		_ = writer.Write([]string{"# amounts are MODELLED from token counts x official list price x the snapshotted channel purchasing ratio"})
	}
	_ = writer.Write([]string{
		"document_status", "settlement_id", string(kind), "label", "group", "period_start", "period_end", "model",
		"channel_id", "channel_name", "channel_base_url", "cost_ratio", "requests", "prompt_tokens",
		"cached_tokens", "completion_tokens", "amount_usd", "priced",
	})

	for _, document := range documents {
		statement := document.Statement
		status, settlementID := "DRAFT_NOT_ISSUED", ""
		if document.Settlement != nil {
			status = document.Settlement.Status
			settlementID = strconv.Itoa(document.Settlement.Id)
		}
		for _, line := range statement.Lines {
			_ = writer.Write([]string{
				status,
				settlementID,
				statement.Counterparty,
				statement.Label,
				statement.Group,
				statement.PeriodStart,
				statement.PeriodEnd,
				line.Model,
				strconv.Itoa(line.ChannelID),
				line.ChannelName,
				line.ChannelBaseURL,
				strconv.FormatFloat(line.CostRatio, 'f', 4, 64),
				strconv.FormatInt(line.Requests, 10),
				strconv.FormatInt(line.PromptTokens, 10),
				strconv.FormatInt(line.CachedTokens, 10),
				strconv.FormatInt(line.CompletionTokens, 10),
				strconv.FormatFloat(line.AmountUSD, 'f', 4, 64),
				boolWord(!line.Unpriced),
			})
		}
		_ = writer.Write([]string{
			status,
			settlementID,
			statement.Counterparty,
			statement.Label,
			statement.Group,
			statement.PeriodStart,
			statement.PeriodEnd,
			"TOTAL",
			"",
			"",
			"",
			"",
			strconv.FormatInt(statement.Requests, 10),
			strconv.FormatInt(statement.PromptTokens, 10),
			strconv.FormatInt(statement.CachedTokens, 10),
			strconv.FormatInt(statement.CompletionTokens, 10),
			strconv.FormatFloat(statement.AmountUSD, 'f', 4, 64),
			boolWord(statement.Complete()),
		})
	}
}

func safeFilenamePart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "all"
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return '-'
		}
	}, value)
}

func boolWord(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
