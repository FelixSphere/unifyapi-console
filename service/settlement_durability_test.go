package service

// UNIFYAPI-FORK: a served request must be charged and logged even when the
// database refuses a connection at settlement time.
//
// Staging load test, 2026-10-02 (perf repo a823484): at 200 RPS Postgres hit
// max_connections and answered SQLSTATE 53300. Of 35,525 requests the upstream
// served and the client received as 200, 372 were never charged to the tenant
// wallet or the token, and 42 have no consume log. PostTextConsumeQuota runs
// after the response is written and returns nothing, so the request "succeeds"
// whatever happens here; the settlement and log errors were logged and dropped.
//
// These tests drive PostTextConsumeQuota against a database that refuses
// connections the way Postgres does under exhaustion, and assert the money and
// the log row, not the log line.

import (
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// connRefuser makes chosen statements fail exactly as Postgres fails them when
// max_connections is exhausted: before the statement reaches the server, with
// SQLSTATE 53300 wrapped in a connect error.
type connRefuser struct {
	mu        sync.Mutex
	remaining map[string]int // table -> statements still to refuse
	all       bool           // refuse every statement on the watched tables
	watched   map[string]bool
	refused   int
}

func (r *connRefuser) refuseNext(table string, n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.remaining[table] += n
}

func (r *connRefuser) outage(on bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.all = on
}

func (r *connRefuser) take(table string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.all && r.watched[table] {
		r.refused++
		return true
	}
	if r.remaining[table] > 0 {
		r.remaining[table]--
		r.refused++
		return true
	}
	return false
}

func installConnRefuser(t *testing.T, db *gorm.DB) *connRefuser {
	t.Helper()
	r := &connRefuser{
		remaining: map[string]int{},
		watched:   map[string]bool{"users": true, "tenants": true, "tokens": true, "logs": true, "settlement_ledgers": true},
	}
	refuse := func(tx *gorm.DB) {
		if tx.Statement != nil && r.take(tx.Statement.Table) {
			_ = tx.AddError(fmt.Errorf("failed to connect to `user=unifyapi database=unifyapi`: %w",
				&pgconn.PgError{Severity: "FATAL", Code: "53300", Message: "sorry, too many clients already"}))
		}
	}
	cb := db.Callback()
	require.NoError(t, cb.Create().Before("gorm:begin_transaction").Register("test:refuse_create", refuse))
	require.NoError(t, cb.Update().Before("gorm:begin_transaction").Register("test:refuse_update", refuse))
	require.NoError(t, cb.Delete().Before("gorm:begin_transaction").Register("test:refuse_delete", refuse))
	require.NoError(t, cb.Query().Before("gorm:query").Register("test:refuse_query", refuse))
	require.NoError(t, cb.Row().Before("gorm:row").Register("test:refuse_row", refuse))
	return r
}

type settlementFixture struct {
	tenantId int
	userId   int
	tokenId  int
	tokenKey string
	refuser  *connRefuser
}

const (
	settlementWallet = 1_000_000
	settlementToken  = 500_000
	// ModelRatio 1, CompletionRatio 1, GroupRatio 1: one quota unit per token.
	settlementPrompt     = 1_000
	settlementCompletion = 250
	settlementCharge     = settlementPrompt + settlementCompletion
)

func setupSettlementDB(t *testing.T) *settlementFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Tenant{}, &model.User{}, &model.Token{}, &model.Log{}, &model.Channel{}, &model.UserSubscription{}, &model.SettlementLedger{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousBatch, previousLogConsume := common.BatchUpdateEnabled, common.LogConsumeEnabled
	model.DB, model.LOG_DB = db, db
	common.BatchUpdateEnabled = false
	common.LogConsumeEnabled = true
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.BatchUpdateEnabled, common.LogConsumeEnabled = previousBatch, previousLogConsume
		_ = sqlDB.Close()
	})

	user := &model.User{Username: "settle-" + t.Name(), DisplayName: "settle", Quota: 0, AffCode: "aff-" + t.Name(), Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(user).Error)
	tenant, err := model.EnsureTenantForUser(user.Id)
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.Tenant{}).Where("id = ?", tenant.Id).Update("quota", settlementWallet).Error)
	token := &model.Token{UserId: user.Id, Key: "settle-key-" + t.Name(), Name: "settle", RemainQuota: settlementToken, Status: common.TokenStatusEnabled}
	require.NoError(t, db.Create(token).Error)

	return &settlementFixture{
		tenantId: tenant.Id,
		userId:   user.Id,
		tokenId:  token.Id,
		tokenKey: token.Key,
		refuser:  installConnRefuser(t, db),
	}
}

// serveOneRequest is what a finished non-streaming relay does after the
// response has been written: pre-consume happened at request start, and
// PostTextConsumeQuota settles and logs.
//
// afterResponse runs between the two: the moment the response has been
// served, which is when the database starts refusing in these tests.
func (f *settlementFixture) serveOneRequest(t *testing.T, requestId string, afterResponse func()) {
	t.Helper()
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	ctx.Set(common.RequestIdKey, requestId)
	ctx.Set("username", "settle")

	relayInfo := &relaycommon.RelayInfo{
		UserId:                  f.userId,
		TokenId:                 f.tokenId,
		TokenKey:                f.tokenKey,
		RequestId:               requestId,
		RelayFormat:             types.RelayFormatOpenAI,
		FinalRequestRelayFormat: types.RelayFormatOpenAI,
		OriginModelName:         "loadtest-mock",
		StartTime:               time.Now(),
		ChannelMeta:             &relaycommon.ChannelMeta{ChannelId: 199},
		PriceData: hosttypes.PriceData{
			ModelRatio:      1,
			CompletionRatio: 1,
			GroupRatioInfo:  hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
	}
	require.Nil(t, PreConsumeBilling(ctx, 0, relayInfo), "pre-consume runs while the database is healthy")
	if afterResponse != nil {
		afterResponse()
	}

	PostTextConsumeQuota(ctx, relayInfo, &dto.Usage{
		PromptTokens:     settlementPrompt,
		CompletionTokens: settlementCompletion,
		TotalTokens:      settlementPrompt + settlementCompletion,
	}, nil)
}

func (f *settlementFixture) walletCharged(t *testing.T) int {
	t.Helper()
	var tenant model.Tenant
	require.NoError(t, model.DB.Unscoped().First(&tenant, f.tenantId).Error)
	return settlementWallet - tenant.Quota
}

func (f *settlementFixture) tokenCharged(t *testing.T) int {
	t.Helper()
	var token model.Token
	require.NoError(t, model.DB.Unscoped().First(&token, f.tokenId).Error)
	return settlementToken - token.RemainQuota
}

func (f *settlementFixture) consumeLogs(t *testing.T, requestId string) []model.Log {
	t.Helper()
	var logs []model.Log
	require.NoError(t, model.LOG_DB.Unscoped().Where("request_id = ? AND type = ?", requestId, model.LogTypeConsume).Find(&logs).Error)
	return logs
}

// The fixture itself: with a healthy database one request costs exactly
// settlementCharge on the wallet and the token, and leaves one log row
// attributed to the tenant. Every other test here is measured against this.
func TestHealthySettlementChargesWalletTokenAndLogOnce(t *testing.T) {
	f := setupSettlementDB(t)
	f.serveOneRequest(t, "req-healthy", nil)

	assert.Equal(t, settlementCharge, f.walletCharged(t))
	assert.Equal(t, settlementCharge, f.tokenCharged(t))
	logs := f.consumeLogs(t, "req-healthy")
	require.Len(t, logs, 1)
	assert.Equal(t, settlementCharge, logs[0].Quota)
	assert.Equal(t, f.tenantId, logs[0].TenantId)
}

// A brief refusal -- one connection attempt turned away, the next accepted --
// is the common shape under exhaustion: a slot frees up milliseconds later.
// Before the fix the wallet lookup failed once and the whole charge was
// dropped, while the request had already been served.
func TestABriefConnectionRefusalStillChargesTheWallet(t *testing.T) {
	f := setupSettlementDB(t)
	f.serveOneRequest(t, "req-brief-wallet", func() {
		// The wallet lookup and the wallet UPDATE each turned away once,
		// the two statements the staging log shows failing.
		f.refuser.refuseNext("users", 1)
		f.refuser.refuseNext("tenants", 1)
	})

	assert.Equal(t, settlementCharge, f.walletCharged(t), "the served request must be charged to the tenant wallet")
	assert.Equal(t, settlementCharge, f.tokenCharged(t), "and to the token")
	require.Len(t, f.consumeLogs(t, "req-brief-wallet"), 1)
}

// Same, at the log insert: the row invoices and reconciliation read.
func TestABriefConnectionRefusalStillWritesTheConsumeLog(t *testing.T) {
	f := setupSettlementDB(t)
	f.serveOneRequest(t, "req-brief-log", func() { f.refuser.refuseNext("logs", 1) })

	assert.Equal(t, settlementCharge, f.walletCharged(t))
	logs := f.consumeLogs(t, "req-brief-log")
	require.Len(t, logs, 1, "a served request must appear on the invoice exactly once")
	assert.Equal(t, settlementCharge, logs[0].Quota)
}

// An outage that outlasts the in-request retry: every connection is refused
// from the moment the response is served until well after settlement gave
// up. Nothing may be applied while the database is down, and once it is back
// the outbox must apply the charge, the token draw-down and the log row
// exactly once -- however many times it is drained.
func TestAnOutageParksTheChargeAndLogAndReplaysThemOnce(t *testing.T) {
	f := setupSettlementDB(t)
	before := model.PendingSettlementCount()

	f.serveOneRequest(t, "req-outage", func() { f.refuser.outage(true) })

	f.refuser.outage(false)
	assert.Zero(t, f.walletCharged(t), "nothing can be applied while the database refuses")
	assert.Empty(t, f.consumeLogs(t, "req-outage"))
	require.Equal(t, before+2, model.PendingSettlementCount(), "the charge and the log row must both be parked")

	applied, remaining := model.DrainSettlementOutbox()
	assert.Equal(t, 2, applied)
	assert.Zero(t, remaining)

	for range 2 { // replays after recovery must not move money again
		model.DrainSettlementOutbox()
	}
	assert.Equal(t, settlementCharge, f.walletCharged(t), "charged exactly once")
	assert.Equal(t, settlementCharge, f.tokenCharged(t), "token drawn down exactly once")
	logs := f.consumeLogs(t, "req-outage")
	require.Len(t, logs, 1, "logged exactly once")
	assert.Equal(t, settlementCharge, logs[0].Quota)
	assert.Equal(t, f.tenantId, logs[0].TenantId, "a row parked before its wallet resolved must still land on the tenant's invoice")
}

// While the database is still down the outbox keeps the items; a drain that
// fails must neither drop them nor half-apply them.
func TestDrainingDuringTheOutageKeepsEverythingParked(t *testing.T) {
	f := setupSettlementDB(t)
	before := model.PendingSettlementCount()
	f.serveOneRequest(t, "req-still-down", func() { f.refuser.outage(true) })

	applied, remaining := model.DrainSettlementOutbox()
	assert.Zero(t, applied)
	assert.Equal(t, before+2, remaining)

	f.refuser.outage(false)
	assert.Zero(t, f.walletCharged(t), "a failed drain must not half-apply anything")
	_, remaining = model.DrainSettlementOutbox()
	assert.Zero(t, remaining)
	assert.Equal(t, settlementCharge, f.walletCharged(t))
	require.Len(t, f.consumeLogs(t, "req-still-down"), 1)
}
