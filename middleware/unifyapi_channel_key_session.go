package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// ChannelKeySessionRequired guards POST /api/channel/:id/key in place of
// upstream's SecureVerificationRequired.
//
// Upstream asks for a fresh 2FA or passkey proof before a channel key is
// shown. The operator does not want a second factor on this action (decision
// 2026-11-20: the root account opens the key directly), so the proof is gone.
//
// What the proof check also did, as a side effect, was refuse a request
// authenticated by an API access token: a proof is bound to a dashboard
// session, and a token has none. That part is kept. Without it, removing the
// second factor would also have made every supplier key readable by a script
// holding the root account's access token, which nobody asked for.
//
// RootAuth runs before this and the handler still writes the
// channel.key_view audit row.
func ChannelKeySessionRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := GetSessionAuthIdentity(c); !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success": false,
				"code":    "CHANNEL_KEY_SESSION_REQUIRED",
				"message": "Sign in to the console to view a channel key. An API access token cannot read it.",
			})
			return
		}
		c.Next()
	}
}
