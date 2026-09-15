package middleware

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// A new registration lands in group `default`, which has channels for almost
// nothing (1 of 52 models on 2026-09-15). Their way to every other model is an
// `auto` token: auth keeps ContextKeyUsingGroup = the user's own group so that
// customer pricing stays attached to the owning company, and channel selection
// is meant to walk the Auto group list on their behalf.
//
// This test checks the hand-off between those two steps with the exact values
// each one produces: auth's effectiveUsingGroup, then CacheGetRandomSatisfiedChannel
// with what the distributor passes as TokenGroup. If the second step is not told
// the token was `auto`, a default user with a perfectly good `auto` token is
// searched for channels in `default` only, and is refused.

func setupDefaultUserRoutingDB(t *testing.T) *gorm.DB {
	t.Helper()
	originalDB, originalCache, originalRetry := model.DB, common.MemoryCacheEnabled, common.RetryTimes
	originalAuto, originalUsable, originalRatios := setting.AutoGroups2JsonString(), setting.UserUsableGroups2JSONString(), ratio_setting.GroupRatio2JSONString()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	model.DB, common.MemoryCacheEnabled, common.RetryTimes = db, true, 0

	// Production shape: the customer groups carry the channels and are the
	// Auto list; `default` is usable and priced but has no channel for this model.
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["GenAI","UnifyAI"]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"GenAI":"","UnifyAI":""}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"GenAI":1,"UnifyAI":1,"default":1}`))

	t.Cleanup(func() {
		model.DB, common.MemoryCacheEnabled, common.RetryTimes = originalDB, originalCache, originalRetry
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(originalAuto))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsable))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalRatios))
		if originalCache && originalDB != nil && originalDB.Migrator().HasTable(&model.Channel{}) && originalDB.Migrator().HasTable(&model.Ability{}) {
			model.InitChannelCache()
		}
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func createRoutingChannel(t *testing.T, db *gorm.DB, id int, group, modelName string) {
	t.Helper()
	priority, weight := int64(0), uint(100)
	require.NoError(t, db.Create(&model.Channel{Id: id, Type: constant.ChannelTypeOpenAI, Key: fmt.Sprintf("key-%d", id), Status: common.ChannelStatusEnabled, Name: fmt.Sprintf("channel-%d", id), Weight: &weight, Models: modelName, Group: group, Priority: &priority}).Error)
	require.NoError(t, db.Create(&model.Ability{Group: group, Model: modelName, ChannelId: id, Enabled: true, Priority: &priority, Weight: weight}).Error)
}

func defaultUserContext(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	return ctx
}

func selectFor(t *testing.T, ctx *gin.Context, tokenGroup, modelName string) (*model.Channel, string, error) {
	t.Helper()
	retry := 0
	return service.CacheGetRandomSatisfiedChannel(&service.RetryParam{
		Ctx: ctx, TokenGroup: tokenGroup, ModelName: modelName, RequestPath: "/v1/chat/completions", Retry: &retry,
	})
}

// TestChannelSelectionWalksAutoGroupsWhenToldTheTokenIsAuto is the control:
// given the literal string "auto", selection does find the GenAI channel for a
// default user. The machinery works. The question is whether it is ever told.
func TestChannelSelectionWalksAutoGroupsWhenToldTheTokenIsAuto(t *testing.T) {
	db := setupDefaultUserRoutingDB(t)
	createRoutingChannel(t, db, 9101, "GenAI", "gpt-4o")
	model.InitChannelCache()

	channel, group, err := selectFor(t, defaultUserContext(t), "auto", "gpt-4o")
	require.NoError(t, err)
	require.NotNil(t, channel)
	assert.Equal(t, 9101, channel.Id)
	assert.Equal(t, "GenAI", group)
}

// TestADefaultUserWithAnAutoTokenIsRoutedToAnAutoGroup asserts the behaviour a
// customer experiences: they hold an `auto` token, gpt-4o is served in GenAI,
// GenAI is in the Auto list -- the request must reach that channel.
//
// The TokenGroup handed to selection is computed the way the live request path
// computes it: auth.go stores effectiveUsingGroup(userGroup, tokenGroup) as
// ContextKeyUsingGroup and token.Group as ContextKeyTokenGroup; the distributor
// then passes routingGroup(c, usingGroup) to selection. Before routingGroup
// existed the distributor passed usingGroup itself, and this test failed with
// "Expected value not to be nil": selection searched `default` only.
func TestADefaultUserWithAnAutoTokenIsRoutedToAnAutoGroup(t *testing.T) {
	db := setupDefaultUserRoutingDB(t)
	createRoutingChannel(t, db, 9102, "GenAI", "gpt-4o")
	model.InitChannelCache()

	ctx := defaultUserContext(t)
	common.SetContextKey(ctx, constant.ContextKeyTokenGroup, "auto")                  // what auth.go stores for the token
	usingGroup := effectiveUsingGroup("default", "auto")                              // what auth.go stores for pricing
	channel, group, err := selectFor(t, ctx, routingGroup(ctx, usingGroup), "gpt-4o") // what the distributor passes

	require.NoError(t, err)
	require.NotNil(t, channel,
		"a default user's `auto` token was searched only in group %q and refused: "+
			"channel selection was never told the token is auto", usingGroup)
	assert.Equal(t, 9102, channel.Id)
	assert.Equal(t, "GenAI", group)
	assert.Equal(t, "default", usingGroup, "pricing still sees the user's own group, not \"auto\"")
}

// TestRoutingGroupChangesNothingForEveryOtherKindOfRequest pins the blast
// radius of routingGroup to exactly one case. Every other request shape --
// an explicit token group, an empty token group, the playground's literal
// "auto", a credit-pool route that already replaced the using group -- must be
// handed to selection with the value it was handed before.
func TestRoutingGroupChangesNothingForEveryOtherKindOfRequest(t *testing.T) {
	for _, tc := range []struct {
		name       string
		userGroup  string
		tokenGroup string
		usingGroup string
		want       string
	}{
		{"explicit token group", "default", "UnifyAI", "UnifyAI", "UnifyAI"},
		{"empty token group routes in the user's group", "default", "", "default", "default"},
		{"GenAI customer with an auto token", "GenAI", "auto", "GenAI", "auto"},
		{"playground already asked for auto", "default", "", "auto", "auto"},
		{"credit-pool route replaced the using group", "default", "auto", "promo-openai", "promo-openai"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			common.SetContextKey(ctx, constant.ContextKeyUserGroup, tc.userGroup)
			common.SetContextKey(ctx, constant.ContextKeyTokenGroup, tc.tokenGroup)
			assert.Equal(t, tc.want, routingGroup(ctx, tc.usingGroup))
		})
	}
}
