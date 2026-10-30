package controller

// UNIFYAPI-FORK: the shared FelixSphere Stripe account; see stripe_product_scope.go.

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/webhook"
	"gorm.io/gorm"
)

const stripeScopeTestWebhookSecret = "whsec_stripe_scope_test"

type stripeScopeFixture struct {
	db       *gorm.DB
	user     *model.User
	errorLog *bytes.Buffer
}

func setupStripeScopeFixture(t *testing.T) *stripeScopeFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Tenant{}, &model.User{}, &model.TopUp{}, &model.SubscriptionOrder{}, &model.Log{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousRedis, previousBatch := common.RedisEnabled, common.BatchUpdateEnabled
	model.DB, model.LOG_DB = db, db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)
	common.RedisEnabled, common.BatchUpdateEnabled = false, false
	t.Setenv("LOG_SQL_DSN", "")
	require.NoError(t, model.InitLogDB())

	confirmPaymentComplianceForTest(t)
	previousAPISecret, previousWebhookSecret, previousPriceID := setting.StripeApiSecret, setting.StripeWebhookSecret, setting.StripePriceId
	setting.StripeApiSecret, setting.StripeWebhookSecret, setting.StripePriceId = "sk_test_scope", stripeScopeTestWebhookSecret, "price_scope"

	// WARN and ERR lines both go to gin.DefaultErrorWriter; INFO goes to DefaultWriter.
	errorLog := &bytes.Buffer{}
	common.LogWriterMu.Lock()
	previousWriter, previousErrorWriter := gin.DefaultWriter, gin.DefaultErrorWriter
	gin.DefaultWriter, gin.DefaultErrorWriter = io.Discard, errorLog
	common.LogWriterMu.Unlock()

	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter, gin.DefaultErrorWriter = previousWriter, previousErrorWriter
		common.LogWriterMu.Unlock()
		setting.StripeApiSecret, setting.StripeWebhookSecret, setting.StripePriceId = previousAPISecret, previousWebhookSecret, previousPriceID
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetMainDatabaseType(previousMainType)
		common.SetLogDatabaseType(previousLogType)
		common.RedisEnabled, common.BatchUpdateEnabled = previousRedis, previousBatch
	})

	user := &model.User{Username: "stripe_scope", DisplayName: "stripe_scope", AffCode: "aff-stripe-scope",
		Status: common.UserStatusEnabled, Group: "default", Role: common.RoleCommonUser, Quota: 0}
	require.NoError(t, db.Create(user).Error)
	return &stripeScopeFixture{db: db, user: user, errorLog: errorLog}
}

func (f *stripeScopeFixture) createPendingTopUp(t *testing.T, tradeNo string) {
	t.Helper()
	require.NoError(t, f.db.Create(&model.TopUp{UserId: f.user.Id, Amount: 10, Money: 10, TradeNo: tradeNo,
		PaymentMethod: model.PaymentMethodStripe, PaymentProvider: model.PaymentProviderStripe,
		CreateTime: time.Now().Unix(), Status: common.TopUpStatusPending}).Error)
}

func (f *stripeScopeFixture) userQuota(t *testing.T) int {
	t.Helper()
	var user model.User
	require.NoError(t, f.db.First(&user, f.user.Id).Error)
	return user.Quota
}

func deliverStripeEvent(t *testing.T, eventType string, session map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := common.Marshal(map[string]any{
		"id":          "evt_scope_test",
		"object":      "event",
		"api_version": stripe.APIVersion,
		"type":        eventType,
		"data":        map[string]any{"object": session},
	})
	require.NoError(t, err)
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: payload, Secret: stripeScopeTestWebhookSecret})

	engine := gin.New()
	engine.POST("/api/stripe/webhook", StripeWebhook)
	request := httptest.NewRequest(http.MethodPost, "/api/stripe/webhook", bytes.NewReader(payload))
	request.Header.Set("Stripe-Signature", signed.Header)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func paidCheckoutSession(clientReferenceId any, metadata any) map[string]any {
	return map[string]any{
		"id":                  "cs_test_scope",
		"object":              "checkout.session",
		"client_reference_id": clientReferenceId,
		"customer":            "cus_scope",
		"status":              "complete",
		"payment_status":      "paid",
		"amount_total":        1000,
		"currency":            "usd",
		"metadata":            metadata,
	}
}

// Unify Compute tags its sessions product=unify-compute. Its events must not
// reach Recharge even when the reference happens to equal a pending console
// top-up: the product tag, not a coincidental reference, decides ownership.
func TestStripeWebhookAcknowledgesAnotherProductsEventWithoutCrediting(t *testing.T) {
	fixture := setupStripeScopeFixture(t)
	fixture.createPendingTopUp(t, "ref_shared_collision")

	recorder := deliverStripeEvent(t, string(stripe.EventTypeCheckoutSessionCompleted),
		paidCheckoutSession("ref_shared_collision", map[string]any{"product": "unify-compute"}))

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Empty(t, fixture.errorLog.String(), "a foreign event must not log a warning or an error")
	assert.Equal(t, 0, fixture.userQuota(t))
	topUp := model.GetTopUpByTradeNo("ref_shared_collision")
	require.NotNil(t, topUp)
	assert.Equal(t, common.TopUpStatusPending, topUp.Status)
}

// Unify Data sets no product tag, only tenant_id and its own
// client_reference_id. Before this change every such sale was logged here as
// "充值订单不存在" at error level.
func TestStripeWebhookAcknowledgesAnUnknownReferenceWithoutAnErrorLog(t *testing.T) {
	cases := []struct {
		name      string
		eventType stripe.EventType
		status    string
	}{
		{name: "completed", eventType: stripe.EventTypeCheckoutSessionCompleted, status: "complete"},
		{name: "async payment succeeded", eventType: stripe.EventTypeCheckoutSessionAsyncPaymentSucceeded, status: "complete"},
		{name: "async payment failed", eventType: stripe.EventTypeCheckoutSessionAsyncPaymentFailed, status: "complete"},
		{name: "expired", eventType: stripe.EventTypeCheckoutSessionExpired, status: "expired"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := setupStripeScopeFixture(t)
			session := paidCheckoutSession("unify-data-tenant-42-order-7", map[string]any{"tenant_id": "42"})
			session["status"] = tc.status

			recorder := deliverStripeEvent(t, string(tc.eventType), session)

			assert.Equal(t, http.StatusOK, recorder.Code)
			assert.Empty(t, fixture.errorLog.String(), "a foreign event must not log a warning or an error")
			assert.Equal(t, 0, fixture.userQuota(t))
			var topUps int64
			require.NoError(t, fixture.db.Model(&model.TopUp{}).Count(&topUps).Error)
			assert.Zero(t, topUps)
		})
	}
}

func TestStripeWebhookAcknowledgesASessionWithoutAReference(t *testing.T) {
	fixture := setupStripeScopeFixture(t)

	recorder := deliverStripeEvent(t, string(stripe.EventTypeCheckoutSessionCompleted), paidCheckoutSession(nil, nil))

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Empty(t, fixture.errorLog.String())
}

// The console's own sessions still credit — both tagged ones and those created
// before the tag existed, which carry no metadata at all.
func TestStripeWebhookStillCreditsTheConsolesOwnTopUp(t *testing.T) {
	cases := []struct {
		name     string
		metadata any
	}{
		{name: "tagged unify-api", metadata: map[string]any{"product": "unify-api"}},
		{name: "untagged, created before the tag", metadata: map[string]any{}},
		{name: "null metadata", metadata: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := setupStripeScopeFixture(t)
			fixture.createPendingTopUp(t, "ref_console_topup")

			recorder := deliverStripeEvent(t, string(stripe.EventTypeCheckoutSessionCompleted),
				paidCheckoutSession("ref_console_topup", tc.metadata))

			assert.Equal(t, http.StatusOK, recorder.Code)
			assert.Empty(t, fixture.errorLog.String())
			assert.Equal(t, int(10*common.QuotaPerUnit), fixture.userQuota(t))
			topUp := model.GetTopUpByTradeNo("ref_console_topup")
			require.NotNil(t, topUp)
			assert.Equal(t, common.TopUpStatusSuccess, topUp.Status)
		})
	}
}

// captureStripeCheckoutForm points the Stripe client at a local server and
// returns the form body of the Checkout Session the console creates.
func captureStripeCheckoutForm(t *testing.T) *url.Values {
	t.Helper()
	captured := &url.Values{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		*captured = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"cs_test_capture","object":"checkout.session","url":"https://checkout.stripe.test/cs"}`))
	}))
	previousBackend := stripe.GetBackend(stripe.APIBackend)
	stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{
		URL:               stripe.String(server.URL),
		MaxNetworkRetries: stripe.Int64(0),
	}))
	t.Cleanup(func() {
		stripe.SetBackend(stripe.APIBackend, previousBackend)
		server.Close()
	})
	return captured
}

func TestStripeTopUpCheckoutSessionIsTaggedUnifyAPI(t *testing.T) {
	withStripePricingConfig(t, "price_scope", 1)
	previousAPISecret := setting.StripeApiSecret
	setting.StripeApiSecret = "sk_test_scope"
	t.Cleanup(func() { setting.StripeApiSecret = previousAPISecret })
	form := captureStripeCheckoutForm(t)

	_, err := genStripeLink("ref_tagged", "", "buyer@example.com", 10, "", "")
	require.NoError(t, err)

	assert.Equal(t, "ref_tagged", form.Get("client_reference_id"))
	assert.Equal(t, "unify-api", form.Get("metadata[product]"))
	assert.Equal(t, "unify-api", form.Get("payment_intent_data[metadata][product]"))
	assert.Equal(t, "price_scope", form.Get("line_items[0][price]"))
}

func TestStripeSubscriptionCheckoutSessionIsTaggedUnifyAPI(t *testing.T) {
	previousAPISecret := setting.StripeApiSecret
	setting.StripeApiSecret = "sk_test_scope"
	t.Cleanup(func() { setting.StripeApiSecret = previousAPISecret })
	form := captureStripeCheckoutForm(t)

	_, err := genStripeSubscriptionLink("sub_ref_tagged", "cus_existing", "", "price_plan")
	require.NoError(t, err)

	assert.Equal(t, "sub_ref_tagged", form.Get("client_reference_id"))
	assert.Equal(t, "unify-api", form.Get("metadata[product]"))
	assert.Equal(t, "unify-api", form.Get("subscription_data[metadata][product]"))
	assert.Equal(t, "subscription", form.Get("mode"))
}
