package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const channelKeyRouteTestKey = "sk-supplier-key-for-the-reveal-test"

// The operator opens a channel key from the channel editor without a second
// factor (decision 2026-11-20). Upstream required a 2FA or passkey proof on
// this route, and an account with neither enrolled could not see a key at
// all. These cases pin what replaced it: who may read a key, and who still
// may not.
func TestChannelKeyIsRevealedToRootWithoutASecondFactor(t *testing.T) {
	engine := setupChannelKeyRouteTest(t)

	root := createChannelKeyRouteUser(t, "key-root", common.RoleRootUser, "")
	admin := createChannelKeyRouteUser(t, "key-admin", common.RoleAdminUser, "")
	customer := createChannelKeyRouteUser(t, "key-customer", common.RoleCommonUser, "")
	const rootAccessToken = "root-api-access-token"
	createChannelKeyRouteUser(t, "key-root-token", common.RoleRootUser, rootAccessToken)

	channel := &model.Channel{Name: "reveal-test", Key: channelKeyRouteTestKey, Status: common.ChannelStatusEnabled}
	require.NoError(t, model.DB.Create(channel).Error)
	path := fmt.Sprintf("/api/channel/%d/key", channel.Id)

	t.Run("root, signed in, no proof header: the key is returned", func(t *testing.T) {
		response := postChannelKey(engine, path, signInForChannelKeyRoute(t, root))
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var body struct {
			Success bool `json:"success"`
			Data    struct {
				Key string `json:"key"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
		assert.True(t, body.Success)
		assert.Equal(t, channelKeyRouteTestKey, body.Data.Key)
		assert.NotContains(t, response.Body.String(), "SECURITY_PROOF",
			"the route must not ask for a 2FA or passkey proof any more")
	})

	t.Run("the reveal is still written to the audit log", func(t *testing.T) {
		var audits int64
		require.NoError(t, model.LOG_DB.Model(&model.Log{}).
			Where("user_id = ? AND type = ? AND other LIKE ?", root.Id, model.LogTypeManage, "%channel.key_view%").
			Count(&audits).Error)
		assert.Equal(t, int64(1), audits)
	})

	refused := []struct {
		name       string
		credential string
		wantStatus int
		wantCode   string
	}{
		{"nobody signed in", "", http.StatusUnauthorized, "AUTH_UNAUTHORIZED"},
		{"an admin who is not root", signInForChannelKeyRoute(t, admin), http.StatusForbidden, "AUTH_INSUFFICIENT_PRIVILEGE"},
		{"a customer", signInForChannelKeyRoute(t, customer), http.StatusForbidden, "AUTH_INSUFFICIENT_PRIVILEGE"},
		// Dropping the second factor must not also open the key to a script
		// that holds root's API access token. Upstream's proof check refused
		// a token as a side effect; this is that refusal, kept on purpose.
		{"root's API access token, not a session", rootAccessToken, http.StatusForbidden, "CHANNEL_KEY_SESSION_REQUIRED"},
	}
	for _, test := range refused {
		t.Run("refused: "+test.name, func(t *testing.T) {
			response := postChannelKey(engine, path, test.credential)
			assert.Equal(t, test.wantStatus, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), test.wantCode)
			assert.NotContains(t, response.Body.String(), channelKeyRouteTestKey)
		})
	}
}

func setupChannelKeyRouteTest(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	previousRedis, previousSecret := common.RedisEnabled, common.SessionSecret

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.Channel{}, &model.Log{}))
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.SessionSecret = "channel-key-route-test-secret"
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMain, previousLog)
		common.RedisEnabled, common.SessionSecret = previousRedis, previousSecret
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	engine := gin.New()
	engine.Use(gin.Recovery())
	registerChannelRoutes(engine.Group("/api"))
	return engine
}

func createChannelKeyRouteUser(t *testing.T, username string, role int, accessToken string) *model.User {
	t.Helper()
	user := &model.User{
		Username: username, Password: "password-placeholder", Role: role,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1,
		AffCode: "aff-" + username,
	}
	if accessToken != "" {
		user.AccessToken = &accessToken
	}
	require.NoError(t, model.DB.Create(user).Error)
	return user
}

// signInForChannelKeyRoute gives the user a live dashboard session and returns
// the access token the console's own pages would send.
func signInForChannelKeyRoute(t *testing.T, user *model.User) string {
	t.Helper()
	now := time.Now().Unix()
	session := &model.UserSession{
		SID:             "session-" + user.Username,
		UserID:          user.Id,
		Version:         1,
		UserAuthVersion: user.AuthVersion,
		Status:          model.UserSessionStatusActive,
		RefreshHash:     "refresh-" + user.Username,
		LoginMethod:     "password",
		LastActiveAt:    now,
		ExpiresAt:       now + 3600,
	}
	require.NoError(t, model.CreateUserSession(session))
	token, _, err := service.IssueAccessToken(service.AuthIdentity{
		UserID:          user.Id,
		SessionID:       session.SID,
		UserAuthVersion: session.UserAuthVersion,
		SessionVersion:  session.Version,
	})
	require.NoError(t, err)
	return token
}

func postChannelKey(engine *gin.Engine, path string, credential string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, nil)
	if credential != "" {
		request.Header.Set("Authorization", "Bearer "+credential)
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}
