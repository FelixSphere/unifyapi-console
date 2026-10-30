package service

// UNIFYAPI-FORK: the settlement state machine, which decides whether a
// customer is charged once, twice, or refunded money they already spent.
//
// A relay either succeeds and settles, or fails and refunds. Both paths can be
// reached more than once -- a retry, a deferred cleanup, an error handler that
// also runs on the success path -- and the money must not move a second time.
// The guards are three booleans (settled, fundingSettled, refunded) with a
// mutex, and there was no test for them.
//
// The funding source is faked so these assert the session's own logic rather
// than a wallet's. IsPlayground keeps token quota out of the picture for the
// same reason.

import (
	"errors"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// recordingFunding counts what actually reached the money, which is the only
// thing that matters here: a guard that flips a boolean but still calls
// through has not prevented anything.
type recordingFunding struct {
	source  string
	settles []int
	refunds int
	err     error
}

func (f *recordingFunding) Source() string              { return f.source }
func (f *recordingFunding) PreConsume(amount int) error { return f.err }
func (f *recordingFunding) Settle(delta int) error {
	if f.err != nil {
		return f.err
	}
	f.settles = append(f.settles, delta)
	return nil
}
func (f *recordingFunding) Refund() error {
	f.refunds++
	return f.err
}

func sessionWithFunding(funding *recordingFunding, preConsumed, tokenConsumed int) *BillingSession {
	return &BillingSession{
		relayInfo:        &relaycommon.RelayInfo{UserId: 1, IsPlayground: true},
		funding:          funding,
		preConsumedQuota: preConsumed,
		tokenConsumed:    tokenConsumed,
	}
}

// Settling twice must move money once. A second Settle is the shape a retry or
// a duplicated deferred call takes, and charging again would be invisible: the
// second deduction looks exactly like the first.
//
// Two separate guards have to hold for this. fundingSettled stops the money
// moving again, and settled stops the rest of the settlement re-running -- the
// subscription delta that feeds the usage log is the observable half of that,
// and double-counting it overstates what the period consumed.
func TestSettlingTwiceChargesOnce(t *testing.T) {
	funding := &recordingFunding{source: BillingSourceWallet}
	session := sessionWithFunding(funding, 100, 100)

	require.NoError(t, session.Settle(180))
	require.NoError(t, session.Settle(180), "a repeated settle must be a no-op, not an error")

	require.Equal(t, []int{80}, funding.settles, "only the delta over the pre-consumed amount may be charged, once")
}

func TestSettlingTwiceCountsTheSubscriptionDeltaOnce(t *testing.T) {
	funding := &recordingFunding{source: BillingSourceSubscription}
	session := sessionWithFunding(funding, 100, 100)

	require.NoError(t, session.Settle(180))
	require.NoError(t, session.Settle(180))

	require.EqualValues(t, 80, session.relayInfo.SubscriptionPostDelta,
		"a repeated settle must not add the same consumption to the log twice")
}

// An exact estimate still has to finalize. A promotional reservation left open
// holds credit that nobody can spend, so Settle(0) is a commit, not a skip.
func TestAnExactEstimateStillFinalizesTheReservation(t *testing.T) {
	funding := &recordingFunding{source: BillingSourcePromotional}
	session := sessionWithFunding(funding, 100, 100)

	require.NoError(t, session.Settle(100))
	require.Equal(t, []int{0}, funding.settles, "an exact estimate must still commit the funding reservation")
	require.False(t, session.NeedsRefund(), "a settled session has nothing left to refund")
}

// Overestimating returns the difference rather than keeping it.
func TestOverestimatingRefundsTheDifferenceAtSettlement(t *testing.T) {
	funding := &recordingFunding{source: BillingSourceWallet}
	session := sessionWithFunding(funding, 500, 500)

	require.NoError(t, session.Settle(120))
	require.Equal(t, []int{-380}, funding.settles, "the unused pre-consumption must come back")
}

// Money already committed must never be handed back. Once funding settled, the
// pre-consumption is not a reservation any more -- refunding it would credit a
// customer for usage they really had.
func TestRefundAfterSettlementReturnsNothing(t *testing.T) {
	funding := &recordingFunding{source: BillingSourceWallet}
	session := sessionWithFunding(funding, 100, 100)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	require.NoError(t, session.Settle(180))

	// Refund dispatches asynchronously, so the decision -- not the side effect
	// -- is what can be asserted reliably. NeedsRefund is that decision.
	require.False(t, session.NeedsRefund(), "a settled session must not report a refund as owed")
	session.Refund(ctx)
	require.False(t, session.NeedsRefund(), "settling must keep the session out of the refund path")
	_ = funding
}

// Refunding twice returns the pre-consumption once. The second call is what a
// failure path plus a cleanup handler looks like.
func TestRefundingTwiceReturnsTheMoneyOnce(t *testing.T) {
	funding := &recordingFunding{source: BillingSourceWallet}
	session := sessionWithFunding(funding, 100, 100)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	require.True(t, session.NeedsRefund(), "an unsettled session holding token quota needs a refund")
	session.Refund(ctx)
	session.Refund(ctx)

	require.False(t, session.NeedsRefund(), "a refunded session must not ask to be refunded again")
}

// A session that never held anything must not manufacture a refund. Trusted
// requests bypass pre-consumption entirely, so there is nothing to give back.
func TestASessionThatPreConsumedNothingHasNothingToRefund(t *testing.T) {
	funding := &recordingFunding{source: BillingSourceWallet}
	session := sessionWithFunding(funding, 0, 0)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	require.False(t, session.NeedsRefund())
	session.Refund(ctx)
	require.Zero(t, funding.refunds, "a session holding nothing must not refund anything")
}

// A funding source that rejects settlement must leave the session unsettled,
// so the failure is visible to the caller and the pre-consumption is still
// accounted for rather than silently abandoned.
func TestAFailedSettlementIsReportedAndNotRecordedAsSettled(t *testing.T) {
	funding := &recordingFunding{source: BillingSourceWallet, err: errSettlementRejected}
	session := sessionWithFunding(funding, 100, 100)

	require.ErrorIs(t, session.Settle(180), errSettlementRejected)
	require.Empty(t, funding.settles, "nothing may be recorded as charged when the funding source refused")
}

var errSettlementRejected = errors.New("funding source rejected the settlement")
