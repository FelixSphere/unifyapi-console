package middleware

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func CORS() gin.HandlerFunc {
	config := cors.DefaultConfig()
	config.AllowAllOrigins = true
	config.AllowCredentials = true
	config.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	config.AllowHeaders = []string{"*"}
	// Let a frontend served from FRONTEND_BASE_URL read the build id too.
	config.ExposeHeaders = []string{common.BuildIDHeader}
	return cors.New(config)
}

func Version() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-New-Api-Version", common.Version)
		// Version is the VERSION file and does not change per release; the
		// build id does. The SPA compares it against its own to detect a
		// stale bundle (web/src/lib/stale-bundle.ts).
		if common.BuildID != "" {
			c.Header(common.BuildIDHeader, common.BuildID)
		}
		c.Next()
	}
}
