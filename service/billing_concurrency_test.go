package service

import (
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relaytypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const twoDollars = 1_000_000

// walletRequest opens a billing session the way controller/relay.go does for a
// text request on a wallet-funded account. IsPlayground skips the per-key
// ledger so these tests isolate the wallet.
func walletRequest(t *testing.T, userID int, tokenUnlimited bool, tokenQuota int, estimate int) (*BillingSession, *relaytypes.NewAPIError) {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("token_quota", tokenQuota)
	info := &relaycommon.RelayInfo{
		UserId:         userID,
		TokenUnlimited: tokenUnlimited,
		IsPlayground:   true,
	}
	return NewBillingSession(c, info, estimate)
}

// The reservation is the only thing standing between a prepaid balance and a
// negative one. Twenty requests race for a $5 wallet at $2 each: exactly two
// may be admitted, whatever order they arrive in, because the reserve is a
// single `UPDATE ... WHERE quota >= ?`.
func TestParallelRequestsBelowTheTrustLineCannotOverdrawTheWallet(t *testing.T) {
	truncate(t)
	const userID = 8101
	const balance = 2_500_000 // $5, below the $10 trust line
	seedUser(t, userID, balance)

	const requests = 20
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		admitted []*BillingSession
		refused  []*relaytypes.NewAPIError
	)
	start := make(chan struct{})
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			session, apiErr := walletRequest(t, userID, true, 0, twoDollars)
			mu.Lock()
			defer mu.Unlock()
			if apiErr != nil {
				refused = append(refused, apiErr)
				return
			}
			admitted = append(admitted, session)
		}()
	}
	close(start)
	wg.Wait()

	require.Len(t, admitted, 2)
	require.Len(t, refused, requests-2)
	for _, session := range admitted {
		assert.Equal(t, twoDollars, session.GetPreConsumedQuota())
		require.NoError(t, session.Settle(twoDollars))
	}

	remaining, err := model.GetUserQuota(userID, false)
	require.NoError(t, err)
	assert.Equal(t, balance-2*twoDollars, remaining)
}

// Which requests skip the reservation is the boundary of the overdraft
// exposure pinned below, so it is pinned too. A wallet exactly at the line,
// or a key capped at or below it, is still reserved.
func TestOnlyWalletsAndKeysAboveTheTrustLineSkipTheReservation(t *testing.T) {
	trustLine := common.GetTrustQuota()
	require.Equal(t, 5_000_000, trustLine, "the trust line is $10 at the default QuotaPerUnit")

	tests := []struct {
		name           string
		balance        int
		tokenUnlimited bool
		tokenQuota     int
		wantReserved   int
	}{
		{name: "wallet exactly at the line", balance: trustLine, tokenUnlimited: true, wantReserved: twoDollars},
		{name: "wallet above the line, key capped at the line", balance: 10 * trustLine, tokenQuota: trustLine, wantReserved: twoDollars},
		{name: "wallet above the line, key capped above it", balance: 10 * trustLine, tokenQuota: trustLine + 1, wantReserved: 0},
		{name: "wallet above the line, unlimited key", balance: trustLine + 1, tokenUnlimited: true, wantReserved: 0},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			truncate(t)
			userID := 8200 + i
			seedUser(t, userID, tt.balance)

			session, apiErr := walletRequest(t, userID, tt.tokenUnlimited, tt.tokenQuota, twoDollars)
			require.Nil(t, apiErr)
			assert.Equal(t, tt.wantReserved, session.GetPreConsumedQuota())

			remaining, err := model.GetUserQuota(userID, false)
			require.NoError(t, err)
			assert.Equal(t, tt.balance-tt.wantReserved, remaining)
		})
	}
}

// KNOWN EXPOSURE, pinned so it cannot grow unnoticed. A wallet over $10 with
// an unlimited key (or a key holding over $10) reserves nothing, so every
// in-flight request is admitted against the same balance and settlement
// subtracts the full cost with no floor. This needs no race: twenty requests
// merely open at the same time, such as long streams, take a $10 wallet to
// -$30. That debt is uncollectable if the customer never tops up.
//
// When the trust bypass is changed to reserve (or capped by the number of
// in-flight requests), this test is expected to fail. Replacing it is a
// commercial decision and needs the operator's approval (AGENTS.md).
func TestTrustedWalletsAreNotReservedSoParallelRequestsRunIntoDebt(t *testing.T) {
	truncate(t)
	const userID = 8301
	balance := common.GetTrustQuota() + 1 // $10.000002
	seedUser(t, userID, balance)

	const inFlight = 20
	sessions := make([]*BillingSession, 0, inFlight)
	for i := 0; i < inFlight; i++ {
		session, apiErr := walletRequest(t, userID, true, 0, twoDollars)
		require.Nil(t, apiErr, "request %d was refused", i)
		assert.Zero(t, session.GetPreConsumedQuota(), "request %d reserved against the wallet", i)
		sessions = append(sessions, session)
	}
	for _, session := range sessions {
		require.NoError(t, session.Settle(twoDollars))
	}

	remaining, err := model.GetUserQuota(userID, false)
	require.NoError(t, err)
	assert.Equal(t, balance-inFlight*twoDollars, remaining)
	assert.Equal(t, -14_999_999, remaining, "a $10 wallet ends $30 in debt")
}
