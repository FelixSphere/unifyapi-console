package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTenantModelContractControllerDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.Tenant{}, &model.Channel{}, &model.TenantModelContract{}, &model.TenantModelChannel{},
		&model.User{}, &model.Log{},
	))
	require.NoError(t, db.Exec("CREATE UNIQUE INDEX ux_tenant_model_channel_owner ON tenant_model_channels (channel_id)").Error)
	previous := model.DB
	previousLogDB := model.LOG_DB
	previousRedisEnabled := common.RedisEnabled
	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = false
	t.Cleanup(func() {
		model.DB = previous
		model.LOG_DB = previousLogDB
		common.RedisEnabled = previousRedisEnabled
	})
}

func TestCustomerModelContractAPIStoresContractWithoutExposingChannelKey(t *testing.T) {
	setupTenantModelContractControllerDB(t)
	gin.SetMode(gin.TestMode)
	tenant := model.Tenant{Name: "Acme", Slug: "acme", Status: model.TenantStatusEnabled}
	require.NoError(t, model.DB.Create(&tenant).Error)
	channel := model.Channel{
		Name: "acme opus", Key: "must-never-appear-in-response", Models: "claude-opus-5",
		Status: common.ChannelStatusEnabled,
	}
	require.NoError(t, model.DB.Create(&channel).Error)
	body, err := json.Marshal(tenantModelContractRequest{
		TenantId: tenant.Id, Model: "claude-opus-5", Discount: 0.74,
		ChannelIds: []int{channel.Id}, Enabled: true,
	})
	require.NoError(t, err)

	writeRecorder := httptest.NewRecorder()
	writeContext, _ := gin.CreateTestContext(writeRecorder)
	writeContext.Request = httptest.NewRequest(http.MethodPut, "/api/pricing/customer_models", bytes.NewReader(body))
	writeContext.Request.Header.Set("Content-Type", "application/json")
	UpsertTenantModelContract(writeContext)
	require.Equal(t, http.StatusOK, writeRecorder.Code)
	require.Contains(t, writeRecorder.Body.String(), `"success":true`)

	loaded, err := model.GetTenantModelContract(tenant.Id, "claude-opus-5", true)
	require.NoError(t, err)
	require.InDelta(t, 0.74, loaded.Discount, 1e-9)
	require.Len(t, loaded.Channels, 1)

	readRecorder := httptest.NewRecorder()
	readContext, _ := gin.CreateTestContext(readRecorder)
	GetTenantModelContracts(readContext)
	require.Equal(t, http.StatusOK, readRecorder.Code)
	require.Contains(t, readRecorder.Body.String(), "claude-opus-5")
	require.NotContains(t, readRecorder.Body.String(), "must-never-appear-in-response")
}

func TestValidateTenantModelContractRequiresDedicatedChannel(t *testing.T) {
	setupTenantModelContractControllerDB(t)
	tenant := model.Tenant{Name: "Acme", Slug: "acme", Status: model.TenantStatusEnabled}
	require.NoError(t, model.DB.Create(&tenant).Error)
	shared := model.Channel{
		Name:   "shared",
		Key:    "test-only",
		Models: "claude-opus-5,claude-fable-5.1",
		Status: common.ChannelStatusEnabled,
	}
	require.NoError(t, model.DB.Create(&shared).Error)

	problems := validateTenantModelContractRequest(tenantModelContractRequest{
		TenantId: tenant.Id, Model: "claude-opus-5", Discount: 0.8,
		ChannelIds: []int{shared.Id}, Enabled: true,
	})
	require.NotEmpty(t, problems)
	require.Contains(t, problems[0], "必须且只能包含模型")
}

func TestValidateTenantModelContractAcceptsOfficialModelAndEnabledDedicatedChannel(t *testing.T) {
	setupTenantModelContractControllerDB(t)
	tenant := model.Tenant{Name: "Acme", Slug: "acme", Status: model.TenantStatusEnabled}
	require.NoError(t, model.DB.Create(&tenant).Error)
	dedicated := model.Channel{
		Name:   "acme opus",
		Key:    "test-only",
		Models: "claude-opus-5",
		Status: common.ChannelStatusEnabled,
	}
	require.NoError(t, model.DB.Create(&dedicated).Error)

	problems := validateTenantModelContractRequest(tenantModelContractRequest{
		TenantId: tenant.Id, Model: "claude-opus-5", Discount: 0.73,
		ChannelIds: []int{dedicated.Id}, Enabled: true,
	})
	require.Empty(t, problems)
}

func TestValidateTenantModelContractRejectsChannelOwnedByAnotherCompany(t *testing.T) {
	setupTenantModelContractControllerDB(t)
	acme := model.Tenant{Name: "Acme", Slug: "acme", Status: model.TenantStatusEnabled}
	globex := model.Tenant{Name: "Globex", Slug: "globex", Status: model.TenantStatusEnabled}
	require.NoError(t, model.DB.Create(&acme).Error)
	require.NoError(t, model.DB.Create(&globex).Error)
	dedicated := model.Channel{
		Name: "acme opus", Key: "acme-only", Models: "claude-opus-5",
		Status: common.ChannelStatusEnabled,
	}
	require.NoError(t, model.DB.Create(&dedicated).Error)
	require.NoError(t, model.UpsertTenantModelContract(&model.TenantModelContract{
		TenantId: acme.Id, Model: "claude-opus-5", Discount: 0.7, Enabled: true,
	}, []int{dedicated.Id}))

	problems := validateTenantModelContractRequest(tenantModelContractRequest{
		TenantId: globex.Id, Model: "claude-opus-5", Discount: 0.6,
		ChannelIds: []int{dedicated.Id}, Enabled: true,
	})
	require.Contains(t, problems, "渠道 #1 已属于另一个客户模型合同")
}

func TestModelSquareDecorationUsesOfficialPriceAndContractDiscount(t *testing.T) {
	setupTenantModelContractControllerDB(t)
	contract := model.TenantModelContract{
		TenantId: 8, Model: "claude-opus-5", Discount: 0.7, Enabled: true,
	}
	require.NoError(t, model.DB.Create(&contract).Error)
	pricing := []model.Pricing{{ModelName: "claude-opus-5", ModelRatio: 2.25}}

	decorated, err := applyTenantModelContractPrices(pricing, 8)
	require.NoError(t, err)
	require.Len(t, decorated, 1)
	require.InDelta(t, 2.5, decorated[0].ModelRatio, 1e-9, "model square starts from official $5/1M")
	require.NotNil(t, decorated[0].CustomerContractDiscount)
	require.InDelta(t, 0.7, *decorated[0].CustomerContractDiscount, 1e-9)
}

func TestStrictCustomerModelModeHidesUncontractedModels(t *testing.T) {
	setupTenantModelContractControllerDB(t)
	tenant := model.Tenant{
		Name: "Acme", Slug: "acme", Status: model.TenantStatusEnabled,
		StrictModelContracts: true,
	}
	require.NoError(t, model.DB.Create(&tenant).Error)
	require.NoError(t, model.DB.Create(&model.TenantModelContract{
		TenantId: tenant.Id, Model: "claude-opus-5", Discount: 0.7, Enabled: true,
	}).Error)

	pricing := []model.Pricing{
		{ModelName: "claude-opus-5"},
		{ModelName: "gpt-4o"},
	}
	decorated, err := applyTenantModelContractPrices(pricing, tenant.Id)
	require.NoError(t, err)
	require.Len(t, decorated, 1)
	require.Equal(t, "claude-opus-5", decorated[0].ModelName)
}

func TestUpdateTenantModelContractModePersistsStrictBoundary(t *testing.T) {
	setupTenantModelContractControllerDB(t)
	gin.SetMode(gin.TestMode)
	tenant := model.Tenant{Name: "Acme", Slug: "acme", Status: model.TenantStatusEnabled}
	require.NoError(t, model.DB.Create(&tenant).Error)
	require.NoError(t, model.DB.Create(&model.TenantModelContract{
		TenantId: tenant.Id, Model: "claude-opus-5", Discount: 0.7, Enabled: true,
	}).Error)
	body, err := json.Marshal(tenantModelContractModeRequest{TenantId: tenant.Id, Strict: true})
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/pricing/customer_models/tenant_mode", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	UpdateTenantModelContractMode(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"success":true`)
	strict, err := model.TenantRequiresStrictModelContracts(tenant.Id)
	require.NoError(t, err)
	require.True(t, strict)
}

func TestUpdateTenantModelContractModeRefusesEmptyStrictBoundary(t *testing.T) {
	setupTenantModelContractControllerDB(t)
	gin.SetMode(gin.TestMode)
	tenant := model.Tenant{Name: "Acme", Slug: "acme", Status: model.TenantStatusEnabled}
	require.NoError(t, model.DB.Create(&tenant).Error)
	body, err := json.Marshal(tenantModelContractModeRequest{TenantId: tenant.Id, Strict: true})
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/pricing/customer_models/tenant_mode", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	UpdateTenantModelContractMode(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"success":false`)
	strict, err := model.TenantRequiresStrictModelContracts(tenant.Id)
	require.NoError(t, err)
	require.False(t, strict)
}
