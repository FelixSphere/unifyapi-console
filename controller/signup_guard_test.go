package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/oauth"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupSignupGuardTest gives each test an empty user database, the in-memory
// limiter, and signup settings pinned to explicit values, so the outcome does
// not depend on process defaults or on another test's state.
func setupSignupGuardTest(t *testing.T) *gorm.DB {
	t.Helper()
	require.NoError(t, i18n.Init())
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Tenant{}, &model.Log{}, &model.Option{}))

	previousDB, previousLogDB, previousType := model.DB, model.LOG_DB, common.MainDatabaseType()
	previousRedis := common.RedisEnabled
	previousRegister, previousPasswordRegister := common.RegisterEnabled, common.PasswordRegisterEnabled
	previousVerification := common.EmailVerificationEnabled
	previousDomainRestriction, previousAliasRestriction := common.EmailDomainRestrictionEnabled, common.EmailAliasRestrictionEnabled
	previousBlocklistEnabled, previousBlocklist := common.EmailDomainBlocklistEnabled, common.EmailDomainBlocklist
	previousLimit := common.RegisterIPDailyLimit
	model.DB, model.LOG_DB = db, db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.RegisterEnabled, common.PasswordRegisterEnabled = true, true
	common.EmailVerificationEnabled = false
	common.EmailDomainRestrictionEnabled, common.EmailAliasRestrictionEnabled = false, false
	common.EmailDomainBlocklistEnabled = true
	common.EmailDomainBlocklist = common.ParseEmailDomainList("maildrop.cc\nauroracovia.com")
	common.RegisterIPDailyLimit = 3
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetMainDatabaseType(previousType)
		common.RedisEnabled = previousRedis
		common.RegisterEnabled, common.PasswordRegisterEnabled = previousRegister, previousPasswordRegister
		common.EmailVerificationEnabled = previousVerification
		common.EmailDomainRestrictionEnabled, common.EmailAliasRestrictionEnabled = previousDomainRestriction, previousAliasRestriction
		common.EmailDomainBlocklistEnabled, common.EmailDomainBlocklist = previousBlocklistEnabled, previousBlocklist
		common.RegisterIPDailyLimit = previousLimit
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func signupContext(method string, target string, body string, remoteAddr string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Accept-Language", "en")
	c.Request.RemoteAddr = remoteAddr
	return c, recorder
}

func postRegister(t *testing.T, body string, remoteAddr string) emailVerificationResponse {
	t.Helper()
	c, recorder := signupContext(http.MethodPost, "/api/user/register", body, remoteAddr)
	Register(c)
	var response emailVerificationResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	return response
}

func countUsers(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(&model.User{}).Count(&count).Error)
	return count
}

func TestRegisterRefusesTheFourthAccountFromOneIPWithinADay(t *testing.T) {
	db := setupSignupGuardTest(t)
	address := "203.0.113.10:5555"

	for i := 1; i <= 3; i++ {
		response := postRegister(t, fmt.Sprintf(`{"username":"iplimit%d","password":"password123"}`, i), address)
		require.True(t, response.Success, response.Message)
	}
	response := postRegister(t, `{"username":"iplimit4","password":"password123"}`, address)
	assert.False(t, response.Success)
	assert.Equal(t, i18n.Translate(i18n.LangEn, i18n.MsgUserRegisterIPLimit), response.Message)
	assert.Equal(t, int64(3), countUsers(t, db))

	response = postRegister(t, `{"username":"otherip1","password":"password123"}`, "203.0.113.11:5555")
	assert.True(t, response.Success, "another IP must be unaffected: %s", response.Message)
}

func TestRegisterFailuresDoNotSpendTheIPBudget(t *testing.T) {
	db := setupSignupGuardTest(t)
	common.RegisterIPDailyLimit = 1
	address := "203.0.113.20:5555"
	require.NoError(t, db.Create(&model.User{Username: "taken", Password: "password123"}).Error)

	for i := 0; i < 3; i++ {
		response := postRegister(t, `{"username":"taken","password":"password123"}`, address)
		require.False(t, response.Success)
		require.NotEqual(t, i18n.Translate(i18n.LangEn, i18n.MsgUserRegisterIPLimit), response.Message)
	}
	response := postRegister(t, `{"username":"fresh","password":"password123"}`, address)
	assert.True(t, response.Success, response.Message)
}

func TestRegisterRefusesABlocklistedEmailDomain(t *testing.T) {
	db := setupSignupGuardTest(t)
	common.EmailVerificationEnabled = true

	for _, email := range []string{"bot@maildrop.cc", "bot@x1.auroracovia.com"} {
		response := postRegister(t,
			fmt.Sprintf(`{"username":"bot","password":"password123","email":%q,"verification_code":"123456"}`, email),
			"203.0.113.30:5555")
		assert.False(t, response.Success, email)
		assert.Equal(t, i18n.Translate(i18n.LangEn, i18n.MsgUserEmailDomainBlocked), response.Message, email)
	}
	assert.Equal(t, int64(0), countUsers(t, db))
}

func TestSendEmailVerificationRefusesABlocklistedDomainOnlyWhileEnabled(t *testing.T) {
	attempted := setupEmailVerificationPolicyTest(t)
	require.NoError(t, i18n.Init())
	previousEnabled, previousList := common.EmailDomainBlocklistEnabled, common.EmailDomainBlocklist
	t.Cleanup(func() {
		common.EmailDomainBlocklistEnabled, common.EmailDomainBlocklist = previousEnabled, previousList
	})
	common.EmailDomainBlocklist = common.ParseEmailDomainList("maildrop.cc")

	common.EmailDomainBlocklistEnabled = true
	c, recorder := signupContext(http.MethodGet, "/api/verification?email=bot@maildrop.cc", "", "203.0.113.40:5555")
	SendEmailVerification(c)
	var response emailVerificationResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Equal(t, i18n.Translate(i18n.LangEn, i18n.MsgUserEmailDomainBlocked), response.Message)
	assert.Empty(t, *attempted, "no code may be mailed to a blocklisted domain")

	common.EmailDomainBlocklistEnabled = false
	requestEmailVerification(t, "bot@maildrop.cc")
	assert.Equal(t, []string{"bot@maildrop.cc"}, *attempted)
}

func TestEmailBindRefusesABlocklistedDomain(t *testing.T) {
	db := setupSignupGuardTest(t)
	user := model.User{Username: "binder", Password: "password123", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	common.RegisterVerificationCodeWithKey("binder@maildrop.cc", "654321", common.EmailVerificationPurpose)

	c, recorder := signupContext(http.MethodPost, "/api/oauth/email/bind",
		`{"email":"binder@maildrop.cc","code":"654321"}`, "203.0.113.50:5555")
	c.Set("id", user.Id)
	EmailBind(c)
	var response emailVerificationResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Equal(t, i18n.Translate(i18n.LangEn, i18n.MsgUserEmailDomainBlocked), response.Message)
	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.Empty(t, stored.Email)
}

// signupGuardOAuthProvider is a built-in style provider whose accounts are
// looked up by provider ID, so a test can tell a returning login from a signup.
type signupGuardOAuthProvider struct {
	authFlowTestOAuthProvider
	existing map[string]int
}

func (p *signupGuardOAuthProvider) IsUserIDTaken(id string) bool {
	_, ok := p.existing[id]
	return ok
}

func (p *signupGuardOAuthProvider) FillUserByProviderID(user *model.User, id string) error {
	user.Id = p.existing[id]
	return model.DB.First(user, user.Id).Error
}

func TestOAuthSignupRefusesABlocklistedEmailDomain(t *testing.T) {
	db := setupSignupGuardTest(t)
	c, _ := signupContext(http.MethodGet, "/api/oauth/test", "", "203.0.113.60:5555")

	_, err := findOrCreateOAuthUser(c, &signupGuardOAuthProvider{}, &oauth.OAuthUser{
		ProviderUserID: "gh-1", Username: "oauthbot", Email: "oauthbot@x9.auroracovia.com",
	}, "", "")
	assert.IsType(t, &OAuthEmailDomainBlockedError{}, err)
	assert.Equal(t, int64(0), countUsers(t, db))
}

func TestOAuthSignupSharesTheIPLimitButReturningLoginsDoNot(t *testing.T) {
	db := setupSignupGuardTest(t)
	common.RegisterIPDailyLimit = 2
	address := "203.0.113.70:5555"
	provider := &signupGuardOAuthProvider{existing: map[string]int{}}

	// One password signup and one OAuth signup spend the budget of 2.
	require.True(t, postRegister(t, `{"username":"mixed1","password":"password123"}`, address).Success)
	c, _ := signupContext(http.MethodGet, "/api/oauth/test", "", address)
	first, err := findOrCreateOAuthUser(c, provider, &oauth.OAuthUser{ProviderUserID: "gh-a", Username: "oauth-a"}, "", "")
	require.NoError(t, err)
	provider.existing["gh-a"] = first.Id

	c, _ = signupContext(http.MethodGet, "/api/oauth/test", "", address)
	_, err = findOrCreateOAuthUser(c, provider, &oauth.OAuthUser{ProviderUserID: "gh-b", Username: "oauth-b"}, "", "")
	assert.IsType(t, &OAuthRegisterIPLimitError{}, err)
	assert.Equal(t, int64(2), countUsers(t, db))

	// The existing OAuth user still logs in from the exhausted IP.
	c, _ = signupContext(http.MethodGet, "/api/oauth/test", "", address)
	returning, err := findOrCreateOAuthUser(c, provider, &oauth.OAuthUser{ProviderUserID: "gh-a"}, "", "")
	require.NoError(t, err)
	assert.Equal(t, first.Id, returning.Id)
}
