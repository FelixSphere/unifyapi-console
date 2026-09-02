package model

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRecordConsumeLogKeepsCustomerSeparateFromRoutingGroup(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Log{}))

	previousDB, previousLogDB := DB, LOG_DB
	previousLogConsume := common.LogConsumeEnabled
	previousDataExport := common.DataExportEnabled
	DB, LOG_DB = db, db
	common.LogConsumeEnabled = true
	common.DataExportEnabled = false
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		common.LogConsumeEnabled = previousLogConsume
		common.DataExportEnabled = previousDataExport
	})

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("username", "Aaron")
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "GenAI")

	RecordConsumeLog(ctx, 7, RecordConsumeLogParams{
		ModelName: "claude-opus-5",
		Quota:     1_350_000,
		Group:     "model--claude-opus-5",
		Other:     map[string]interface{}{},
	})

	var log Log
	require.NoError(t, db.First(&log).Error)
	require.Equal(t, "GenAI", log.Group, "logs.group is the customer/company")
	var other map[string]interface{}
	require.NoError(t, common.Unmarshal([]byte(log.Other), &other))
	require.Equal(t, "model--claude-opus-5", other["routing_group"])
}
