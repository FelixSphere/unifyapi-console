package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func useRegisterIPDailyLimit(t *testing.T, limit int) {
	t.Helper()
	previous := common.RegisterIPDailyLimit
	common.RegisterIPDailyLimit = limit
	t.Cleanup(func() { common.RegisterIPDailyLimit = previous })
}

func useRegisterIPMemoryBackend(t *testing.T) {
	t.Helper()
	previous := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previous })
}

func registrationContext(remoteAddr string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/register", nil)
	c.Request.RemoteAddr = remoteAddr
	return c
}

// createAccounts reserves and commits n signups from one address and reports
// how many were allowed.
func createAccounts(remoteAddr string, n int) int {
	created := 0
	for i := 0; i < n; i++ {
		slot, ok := ReserveRegistrationSlot(registrationContext(remoteAddr))
		if !ok {
			continue
		}
		slot.Commit()
		slot.Release()
		created++
	}
	return created
}

func TestRegisterIPLimitAllowsLimitAccountsPerIPThenRefuses(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			if backend == "redis" {
				useRateLimitMiniRedis(t)
			} else {
				useRegisterIPMemoryBackend(t)
			}
			useRegisterIPDailyLimit(t, 3)
			address := "198.51.100.11:40000"
			if backend == "redis" {
				address = "198.51.100.12:40000"
			}

			assert.Equal(t, 3, createAccounts(address, 3))
			_, ok := ReserveRegistrationSlot(registrationContext(address))
			assert.False(t, ok, "the 4th account from one IP within 24h must be refused")

			assert.Equal(t, 1, createAccounts("198.51.100.99:40000", 1), "another IP keeps its own budget")
		})
	}
}

func TestRegisterIPLimitReturnsTheSlotOfAFailedSignup(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			if backend == "redis" {
				useRateLimitMiniRedis(t)
			} else {
				useRegisterIPMemoryBackend(t)
			}
			useRegisterIPDailyLimit(t, 2)
			address := "198.51.100.21:40000"
			if backend == "redis" {
				address = "198.51.100.22:40000"
			}

			// Three attempts fail (taken username, database error, ...) and
			// give their slot back; they must not spend the budget.
			for i := 0; i < 3; i++ {
				slot, ok := ReserveRegistrationSlot(registrationContext(address))
				require.True(t, ok)
				slot.Release()
			}
			assert.Equal(t, 2, createAccounts(address, 3))
		})
	}
}

func TestRegisterIPLimitRedisWindowIsOneDay(t *testing.T) {
	redisServer, _ := useRateLimitMiniRedis(t)
	useRegisterIPDailyLimit(t, 3)

	require.Equal(t, 1, createAccounts("198.51.100.31:40000", 1))
	key := redisIPRateLimitKey(RegisterIPLimitMark, "198.51.100.31")
	count, err := redisServer.Get(key)
	require.NoError(t, err)
	assert.Equal(t, "1", count)
	ttl := redisServer.TTL(key)
	assert.Greater(t, ttl.Seconds(), float64(23*60*60))
	assert.LessOrEqual(t, ttl.Seconds(), float64(24*60*60))
}

func TestRegisterIPLimitReleaseAfterWindowExpiryDoesNotGoNegative(t *testing.T) {
	redisServer, _ := useRateLimitMiniRedis(t)
	useRegisterIPDailyLimit(t, 1)
	address := "198.51.100.41:40000"
	key := redisIPRateLimitKey(RegisterIPLimitMark, "198.51.100.41")

	slot, ok := ReserveRegistrationSlot(registrationContext(address))
	require.True(t, ok)
	redisServer.FastForward(25 * time.Hour)
	require.False(t, redisServer.Exists(key))
	slot.Release()

	// A release against an expired window must not leave a negative,
	// TTL-less counter that would hand this IP extra accounts forever.
	assert.False(t, redisServer.Exists(key))
	assert.Equal(t, 1, createAccounts(address, 2))
}

func TestRegisterIPLimitZeroDisablesTheCap(t *testing.T) {
	useRegisterIPMemoryBackend(t)
	useRegisterIPDailyLimit(t, 0)

	assert.Equal(t, 10, createAccounts("198.51.100.51:40000", 10))
}

func TestRegisterIPLimitFallsBackToMemoryWhenRedisFails(t *testing.T) {
	redisServer, _ := useRateLimitMiniRedis(t)
	useRegisterIPDailyLimit(t, 2)
	redisServer.Close()

	address := "198.51.100.61:40000"
	assert.Equal(t, 2, createAccounts(address, 3), "a Redis outage must not switch the cap off")
}
