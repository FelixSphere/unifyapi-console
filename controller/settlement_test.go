/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package controller

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupSettlementControllerDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Log{}, &model.Channel{}, &model.Settlement{}, &model.User{}, &model.TopUp{}))
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	model.DB, model.LOG_DB = db, db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetMainDatabaseType(previousMainType)
		common.SetLogDatabaseType(previousLogType)
	})
	gin.SetMode(gin.TestMode)
	return db
}

func TestIssueSettlementRejectsAnOpenMonthBeforeReadingTheLedger(t *testing.T) {
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	end := start.AddDate(0, 1, -1)
	body, err := json.Marshal(IssueSettlementRequest{
		Kind: "customer", Counterparty: "GenAI",
		Start: start.Format("2006-01-02"), End: end.Format("2006-01-02"),
	})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/pricing/settlement", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")

	IssueSettlement(ctx)
	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Contains(t, recorder.Body.String(), "still open")
}

func TestSettlementCSVRouteRejectsUnauthenticatedNavigation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/pricing/settlement.csv", middleware.RootAuth(), ExportSettlementCSV)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet,
		"/api/pricing/settlement.csv?kind=customer&start=2026-07-01&end=2026-07-31", nil)
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.NotContains(t, recorder.Header().Get("Content-Disposition"), "attachment")
	require.Contains(t, recorder.Body.String(), "AUTH_")
}

func TestSettlementCSVUsesFrozenRowsAndIncludesChannelCostFacts(t *testing.T) {
	db := setupSettlementControllerDB(t)
	baseURL := "https://openrouter.ai/api/v1"
	require.NoError(t, db.Create(&model.Channel{Id: 8, Name: "openrouter-primary", BaseURL: &baseURL}).Error)
	startTime := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.Local).Unix()
	require.NoError(t, db.Create(&model.Log{
		UserId: 1, Username: "acme", CreatedAt: startTime + 10, Type: model.LogTypeConsume,
		ModelName: "gpt-4o", ChannelId: 8, ChannelBaseURL: baseURL,
		PromptTokens: 1_000_000, Quota: 500_000,
	}).Error)

	frozen := service.Statement{
		Kind: service.StatementKindVendor, Counterparty: "openrouter", Label: "OpenRouter",
		PeriodStart: "2026-07-01", PeriodEnd: "2026-07-31", AmountUSD: 2.5, Requests: 1,
		Lines: []service.StatementLine{{
			Model: "gpt-4o", ChannelID: 8, ChannelName: "openrouter-primary",
			ChannelBaseURL: baseURL, CostRatio: 0.8, Requests: 1,
			PromptTokens: 1_000_000, AmountUSD: 2.5,
		}},
	}
	encoded, err := common.Marshal(frozen)
	require.NoError(t, err)
	issued, err := model.CreateSettlement(&model.Settlement{
		Kind: "vendor", Counterparty: "openrouter", Label: "OpenRouter",
		PeriodStart: "2026-07-01", PeriodEnd: "2026-07-31", AmountUSD: 2.5,
		StatementJSON: string(encoded),
	})
	require.NoError(t, err)
	changedBaseURL := "https://console.flatkey.ai/api"
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 8).
		Update("base_url", changedBaseURL).Error)

	// Live usage changes after issue; the downloadable document must not.
	require.NoError(t, db.Create(&model.Log{
		UserId: 1, Username: "acme", CreatedAt: startTime + 20, Type: model.LogTypeConsume,
		ModelName: "gpt-4o", ChannelId: 8, ChannelBaseURL: baseURL,
		PromptTokens: 2_000_000, Quota: 1_000_000,
	}).Error)

	query := url.Values{
		"kind": {"vendor"}, "start": {"2026-07-01"}, "end": {"2026-07-31"},
		"counterparty": {"openrouter"},
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/pricing/settlement.csv?"+query.Encode(), nil)
	ExportSettlementCSV(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()
	require.Contains(t, body, "document_status,settlement_id,vendor")
	require.Contains(t, body, "issued,"+strconv.Itoa(issued.Id)+",openrouter,OpenRouter")
	require.Contains(t, body, "openrouter-primary,"+baseURL+",0.8000")
	require.NotContains(t, body, "DRAFT_NOT_ISSUED")
	require.NotContains(t, body, "flatkey",
		"editing a channel later must not rewrite the issued supplier identity")
	require.NotContains(t, body, "3.0000", "post-issue live tokens must not rewrite the frozen document")

	reader := csv.NewReader(strings.NewReader(body))
	reader.Comment = '#'
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	require.NoError(t, err)
	var detailAmount, totalAmount float64
	for _, record := range records[1:] {
		amount, parseErr := strconv.ParseFloat(record[16], 64)
		require.NoError(t, parseErr)
		if record[7] == "TOTAL" {
			totalAmount = amount
		} else {
			detailAmount += amount
		}
	}
	require.InDelta(t, frozen.AmountUSD, detailAmount, 1e-9)
	require.InDelta(t, detailAmount, totalAmount, 1e-9,
		"CSV supplier total must equal its model/channel details")
}

func TestCustomerSettlementResponseNeverTreatsTopUpsAsInvoicePayments(t *testing.T) {
	db := setupSettlementControllerDB(t)
	startTime := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.Local).Unix()
	require.NoError(t, db.Create(&model.Log{
		UserId: 1, Username: "acme", Group: "GenAI", CreatedAt: startTime + 10,
		Type: model.LogTypeConsume, ModelName: "gpt-4o", Quota: 500_000,
	}).Error)
	require.NoError(t, db.Create(&model.TopUp{
		UserId: 1, Status: common.TopUpStatusSuccess, Amount: 100,
		TradeNo: "wallet-credit", CompleteTime: startTime + 20,
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet,
		"/api/pricing/settlement?kind=customer&start=2026-07-01&end=2026-07-31", nil)
	GetSettlements(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	response := recorder.Body.String()
	require.NotContains(t, response, `"payments"`)
	require.NotContains(t, strings.ToLower(response), "topup")
}

func TestUnissuedSettlementCSVIsMarkedAsDraft(t *testing.T) {
	db := setupSettlementControllerDB(t)
	startTime := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.Local).Unix()
	require.NoError(t, db.Create(&model.Log{
		UserId: 1, Username: "acme", Group: "GenAI", CreatedAt: startTime + 10,
		Type: model.LogTypeConsume, ModelName: "gpt-4o", Quota: 500_000,
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet,
		"/api/pricing/settlement.csv?kind=customer&start=2026-07-01&end=2026-07-31&counterparty=GenAI", nil)
	ExportSettlementCSV(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "# DRAFT_NOT_ISSUED")
	require.Contains(t, recorder.Body.String(), "DRAFT_NOT_ISSUED,,GenAI")
}

func TestSettlementAPITotalsEqualSupplierModelChannelDetails(t *testing.T) {
	db := setupSettlementControllerDB(t)
	startTime := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.Local).Unix()
	for _, log := range []*model.Log{
		{UserId: 1, CreatedAt: startTime + 10, Type: model.LogTypeConsume, ModelName: "gpt-4o",
			ChannelId: 1, ChannelBaseURL: "https://console.flatkey.ai", PromptTokens: 1_000_000},
		{UserId: 1, CreatedAt: startTime + 20, Type: model.LogTypeConsume, ModelName: "claude-opus-5",
			ChannelId: 2, ChannelBaseURL: "https://console.flatkey.ai/api", PromptTokens: 1_000_000},
	} {
		require.NoError(t, db.Create(log).Error)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet,
		"/api/pricing/settlement?kind=vendor&start=2026-07-01&end=2026-07-31", nil)
	GetSettlements(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)

	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Rows   []settlementRow         `json:"rows"`
			Totals service.StatementTotals `json:"totals"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Len(t, response.Data.Rows, 1)
	statement := response.Data.Rows[0].Statement
	require.Equal(t, "flatkey", statement.Counterparty)
	var lineAmount float64
	var lineRequests int64
	for _, line := range statement.Lines {
		lineAmount += line.AmountUSD
		lineRequests += line.Requests
	}
	require.InDelta(t, statement.AmountUSD, lineAmount, 1e-9)
	require.Equal(t, statement.Requests, lineRequests)
	require.InDelta(t, statement.AmountUSD, response.Data.Totals.AmountUSD, 1e-9)
}
