package controller

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const emailAliasRejectionMessage = "The administrator has enabled email alias restrictions; your address was rejected because it contains special symbols."

type emailVerificationResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// setupEmailVerificationPolicyTest isolates SendEmailVerification from the
// process: an empty user table, no SMTP server (so a send fails fast instead
// of dialling out), and a recorder that captures every delivery attempt.
func setupEmailVerificationPolicyTest(t *testing.T) *[]string {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}))
	previousDB, previousType := model.DB, common.MainDatabaseType()
	previousRedis := common.RedisEnabled
	previousDomainRestriction := common.EmailDomainRestrictionEnabled
	previousAliasRestriction := common.EmailAliasRestrictionEnabled
	previousSMTPServer, previousSMTPAccount := common.SMTPServer, common.SMTPAccount
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.EmailDomainRestrictionEnabled = false
	common.SMTPServer, common.SMTPAccount = "", ""
	attempted := []string{}
	common.SetEmailDeliveryRecorder(func(receiver string, purpose string, err error) {
		attempted = append(attempted, receiver)
	})
	t.Cleanup(func() {
		common.SetEmailDeliveryRecorder(nil)
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
		common.RedisEnabled = previousRedis
		common.EmailDomainRestrictionEnabled = previousDomainRestriction
		common.EmailAliasRestrictionEnabled = previousAliasRestriction
		common.SMTPServer, common.SMTPAccount = previousSMTPServer, previousSMTPAccount
	})
	return &attempted
}

func requestEmailVerification(t *testing.T, email string) emailVerificationResponse {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/verification?email="+url.QueryEscape(email), nil)
	SendEmailVerification(c)
	var response emailVerificationResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	return response
}

func TestEmailAliasRestrictionRejectsPlusTagsAndGmailDotsButNotCorporateDots(t *testing.T) {
	attempted := setupEmailVerificationPolicyTest(t)
	common.EmailAliasRestrictionEnabled = true

	for _, email := range []string{"aenomkali+x7@gmail.com", "user+tag@company.com", "a.b.c@gmail.com", "a.b.c@googlemail.com"} {
		response := requestEmailVerification(t, email)
		assert.False(t, response.Success, email)
		assert.Equal(t, emailAliasRejectionMessage, response.Message, email)
	}
	assert.Empty(t, *attempted, "no verification email may be sent to a rejected alias")

	for _, email := range []string{"john.smith@company.com", "abc@gmail.com"} {
		response := requestEmailVerification(t, email)
		assert.NotEqual(t, emailAliasRejectionMessage, response.Message, email)
	}
	assert.Equal(t, []string{"john.smith@company.com", "abc@gmail.com"}, *attempted,
		"an address that is not an alias must reach the verification send")
}

func TestEmailAliasRestrictionDisabledAllowsGmailDots(t *testing.T) {
	attempted := setupEmailVerificationPolicyTest(t)
	common.EmailAliasRestrictionEnabled = false

	response := requestEmailVerification(t, "a.b.c@gmail.com")
	assert.NotEqual(t, emailAliasRejectionMessage, response.Message)
	assert.Equal(t, []string{"a.b.c@gmail.com"}, *attempted)
}
