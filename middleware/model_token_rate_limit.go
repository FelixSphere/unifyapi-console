// UNIFYAPI-BRAND: ours. Per-customer TPM limiting.
package middleware

import (
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
)

// ModelRequestTokenRateLimit refuses a request when the caller has already spent
// their token allowance for the current window.
//
// It counts what has already been spent rather than what this request will
// spend, because neither figure is available before the upstream answers. One
// request may therefore cross the line; the next is refused. See
// model/user_token_rate_limit.go.
func ModelRequestTokenRateLimit() func(c *gin.Context) {
	return func(c *gin.Context) {
		limit := resolveTokenLimit(c)
		if limit <= 0 { // 0 means unlimited, as with the request-count limiter
			c.Next()
			return
		}

		userId := c.GetInt("id")
		spent := model.UserTokensInWindow(c, userId)
		if spent < int64(limit) {
			c.Next()
			return
		}

		abortWithOpenAiMessage(c, http.StatusTooManyRequests, fmt.Sprintf(
			"Token rate limit reached: at most %d tokens per %d minute(s); %d already used in this window.",
			limit, setting.ModelRequestRateLimitDurationMinutes, spent))
	}
}

// resolveTokenLimit prefers the token's group, then the user's, then the global
// default -- the same precedence the request-count limiter uses, so the two
// cannot disagree about which group a caller belongs to.
func resolveTokenLimit(c *gin.Context) int {
	group := common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
	if group == "" {
		group = common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	}
	if limit, found := setting.GetGroupTokenLimit(group); found {
		return limit
	}
	return setting.ModelRequestTokenLimitCount
}
