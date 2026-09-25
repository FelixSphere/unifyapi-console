// UNIFYAPI-BRAND: ours. Per-customer token accounting for the TPM limiter.
//
// Tokens can only be counted after a request finishes: the completion length is
// not knowable before the upstream answers, and the middleware runs before the
// body is even parsed. So enforcement is "check the window so far, then admit",
// which means a customer can exceed their token allowance by at most the one
// request that crosses the line.
//
// That is inherent to token limiting rather than a shortcut. The alternative --
// estimating prompt tokens in the middleware -- would mean parsing every request
// body twice and still guessing the completion.
package model

import (
	"context"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
)

const userTokenWindowKeyTTL = 3 * time.Minute

func userTokenWindowKey(userId int, now time.Time) string {
	window := int64(setting.ModelRequestRateLimitDurationMinutes) * 60
	if window <= 0 {
		window = 60
	}
	return fmt.Sprintf("userTokens:%d:%d", userId, now.UTC().Unix()/window)
}

// RecordUserTokens adds a finished request's tokens to the caller's window.
func RecordUserTokens(ctx context.Context, userId int, tokens int) {
	if !common.RedisEnabled || tokens <= 0 || userId <= 0 {
		return
	}
	key := userTokenWindowKey(userId, time.Now())
	pipe := common.RDB.Pipeline()
	pipe.IncrBy(ctx, key, int64(tokens))
	pipe.Expire(ctx, key, userTokenWindowKeyTTL)
	_, _ = pipe.Exec(ctx)
}

// UserTokensInWindow reports how many tokens the user has spent in the current
// window. A missing key means an untouched window, which is zero rather than an
// error.
func UserTokensInWindow(ctx context.Context, userId int) int64 {
	if !common.RedisEnabled || userId <= 0 {
		return 0
	}
	v, err := common.RDB.Get(ctx, userTokenWindowKey(userId, time.Now())).Int64()
	if err != nil {
		return 0
	}
	return v
}
