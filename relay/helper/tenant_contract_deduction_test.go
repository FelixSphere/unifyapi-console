package helper_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	helper "github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	relaytypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestTenantModelContractDeductsTheExactSettledQuota crosses the last boundary
// that ratio-only tests cannot: it builds PriceData from the active contract,
// settles a real usage record, and checks the wallet and token balances that a
// customer invoice is ultimately backed by.
func TestTenantModelContractDeductsTheExactSettledQuota(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.User{}, &model.Token{}, &model.Channel{}, &model.Log{}, &model.UserSubscription{},
	))
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedis, previousBatch, previousLogConsume := common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = false, false, true
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = previousRedis, previousBatch, previousLogConsume
	})

	const initialQuota = 20_000_000
	user := model.User{Username: "contract-customer", Quota: initialQuota, Status: common.UserStatusEnabled, TenantId: 7}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{
		UserId: user.Id, Key: "local-contract-test", Name: "contract-test",
		Status: common.TokenStatusEnabled, RemainQuota: initialQuota,
	}
	require.NoError(t, db.Create(&token).Error)
	channel := model.Channel{Name: "acme opus", Key: "local-only", Status: common.ChannelStatusEnabled, Models: "claude-opus-5"}
	require.NoError(t, db.Create(&channel).Error)

	ratio_setting.InitRatioSettings()
	require.NoError(t, ratio_setting.UpdateModelDiscountByJSONString(`{"claude-opus-5":0.9}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"vip":0.8}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelDiscountByJSONString(`{}`))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{}`))
	})

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	ctx.Set(string(constant.ContextKeyTenantModelContractId), 42)
	ctx.Set(string(constant.ContextKeyTenantModelContractDiscount), 0.7)
	ctx.Set(string(constant.ContextKeyTenantId), 7)
	ctx.Set(string(constant.ContextKeyUsingGroup), "vip")

	info := &relaycommon.RelayInfo{
		UserId: user.Id, TenantId: 7, TokenId: token.Id, TokenKey: token.Key,
		OriginModelName: "claude-opus-5", UserGroup: "vip", UsingGroup: "vip",
		RelayFormat: relaytypes.RelayFormatClaude, FinalRequestRelayFormat: relaytypes.RelayFormatClaude,
		StartTime: time.Now(), RequestId: "contract-deduction-test",
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channel.Id},
	}
	priceData, err := helper.ModelPriceHelper(ctx, info, 1_000_000, &relaytypes.TokenCountMeta{})
	require.NoError(t, err)
	info.PriceData = priceData

	service.PostTextConsumeQuota(ctx, info, &dto.Usage{
		PromptTokens: 1_000_000, CompletionTokens: 1_000_000, TotalTokens: 2_000_000,
		UsageSemantic: dto.BillingUsageSemanticAnthropic,
	}, nil)

	// Official Opus 5 is $5 input + $25 output per 1M. At 0.7x the
	// customer owes exactly $21. With 500,000 quota per USD that is
	// 10,500,000 quota. The global 0.9 and vip 0.8 must not appear again.
	const deducted = 10_500_000
	remaining, err := model.GetUserQuota(user.Id, false)
	require.NoError(t, err)
	require.Equal(t, initialQuota-deducted, remaining)
	var settledToken model.Token
	require.NoError(t, db.First(&settledToken, token.Id).Error)
	require.Equal(t, initialQuota-deducted, settledToken.RemainQuota)
	require.Equal(t, deducted, settledToken.UsedQuota)

	var log model.Log
	require.NoError(t, db.Where("user_id = ? AND model_name = ?", user.Id, "claude-opus-5").First(&log).Error)
	require.Equal(t, deducted, log.Quota)
	require.Equal(t, 7, log.TenantId, "the invoice must retain the company that owned the request")
}
