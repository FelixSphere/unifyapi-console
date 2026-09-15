package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The marketing site (www.unifyapi.ai) is a different origin from the
// console. It advertises the new-user price by reading the public catalogue
// in the visitor's browser, which only works if /api/pricing answers a
// cross-origin GET with Access-Control-Allow-Origin. The rows are the ones an
// anonymous visitor already gets; nothing else on /api gains CORS from this.
func TestPublicPricingAnswersACrossOriginRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Channel{}, &model.Ability{}, &model.Model{}, &model.Vendor{}))
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	engine := gin.New()
	engine.Use(gin.Recovery())
	SetApiRouter(engine)

	const marketingOrigin = "https://www.unifyapi.ai"

	pricing := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/pricing", nil)
	req.Header.Set("Origin", marketingOrigin)
	engine.ServeHTTP(pricing, req)
	assert.Equal(t, http.StatusOK, pricing.Code, pricing.Body.String())
	// The shared CORS() allows every origin, so the browser sees "*". The
	// homepage fetches without credentials, for which "*" is sufficient.
	assert.Equal(t, "*", pricing.Header().Get("Access-Control-Allow-Origin"),
		"the homepage must be able to read the public catalogue from its own origin")

	// A neighbouring public route is unchanged: CORS was added to /api/pricing
	// alone, not to the whole public group.
	about := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/about", nil)
	req.Header.Set("Origin", marketingOrigin)
	engine.ServeHTTP(about, req)
	assert.Empty(t, about.Header().Get("Access-Control-Allow-Origin"))
}
