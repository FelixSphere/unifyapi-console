package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupContractRoutingTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.Channel{}, &model.Ability{}, &model.Tenant{}, &model.TenantModelContract{}, &model.TenantModelChannel{},
	))
	previous := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previous })
}

func TestStrictCustomerNeverFallsBackWhenModelHasNoContract(t *testing.T) {
	setupContractRoutingTestDB(t)
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	tenant := model.Tenant{
		Name: "Acme", Slug: "acme", Status: model.TenantStatusEnabled,
		StrictModelContracts: true,
	}
	require.NoError(t, model.DB.Create(&tenant).Error)
	generic := model.Channel{
		Name: "generic opus", Key: "generic-key", Models: "claude-opus-5",
		Status: common.ChannelStatusEnabled,
	}
	require.NoError(t, model.DB.Create(&generic).Error)
	require.NoError(t, model.DB.Create(&model.Ability{
		Group: "default", Model: "claude-opus-5", ChannelId: generic.Id, Enabled: true,
	}).Error)

	selected, _, err := CacheGetRandomSatisfiedChannel(&RetryParam{
		Ctx: ctx, TenantId: tenant.Id, TokenGroup: "default", ModelName: "claude-opus-5",
		RequestPath: "/v1/chat/completions", Retry: common.GetPointer(0),
	})
	require.Error(t, err)
	require.Nil(t, selected)
	require.Contains(t, err.Error(), "no enabled contract")
}

func TestCustomerModelContractNeverFallsBackToGenericChannel(t *testing.T) {
	setupContractRoutingTestDB(t)
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	dedicated := model.Channel{Name: "acme opus", Key: "acme-key", Models: "claude-opus-5", Status: common.ChannelStatusEnabled}
	generic := model.Channel{Name: "generic opus", Key: "generic-key", Models: "claude-opus-5", Status: common.ChannelStatusEnabled}
	require.NoError(t, model.DB.Create(&dedicated).Error)
	require.NoError(t, model.DB.Create(&generic).Error)
	require.NoError(t, model.DB.Create(&model.Ability{
		Group: "default", Model: "claude-opus-5", ChannelId: generic.Id, Enabled: true,
	}).Error)

	contract := &model.TenantModelContract{TenantId: 7, Model: "claude-opus-5", Discount: 0.72, Enabled: true}
	require.NoError(t, model.UpsertTenantModelContract(contract, []int{dedicated.Id}))

	selected, group, err := CacheGetRandomSatisfiedChannel(&RetryParam{
		Ctx: ctx, TenantId: 7, TokenGroup: "default", ModelName: "claude-opus-5",
		RequestPath: "/v1/chat/completions", Retry: common.GetPointer(0),
	})
	require.NoError(t, err)
	require.NotNil(t, selected)
	require.Equal(t, dedicated.Id, selected.Id)
	require.Equal(t, "tenant:7/claude-opus-5", group)

	// Once the dedicated route becomes unavailable, the request must fail. The
	// enabled generic ability is deliberately present to prove there is no
	// cross-customer fallback.
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", dedicated.Id).
		Update("status", common.ChannelStatusManuallyDisabled).Error)
	selected, _, err = CacheGetRandomSatisfiedChannel(&RetryParam{
		Ctx: ctx, TenantId: 7, TokenGroup: "default", ModelName: "claude-opus-5",
		RequestPath: "/v1/chat/completions", Retry: common.GetPointer(0),
	})
	require.NoError(t, err)
	require.Nil(t, selected)
}
