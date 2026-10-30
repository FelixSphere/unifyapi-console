/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupEmailDeliveryTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&EmailDeliveryState{}))
	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous })
}

// An address nobody has written to is reported reachable. Nothing has
// contradicted it, and calling untried addresses unreachable would be a guess
// presented as a fact.
func TestAnAddressNeverWrittenToIsNotCalledUnreachable(t *testing.T) {
	var missing *EmailDeliveryState
	assert.True(t, missing.Reachable())
}

// Acceptance by the mail server is what send time can observe, so that is what
// is recorded -- and it is recorded as "accepted", not as "delivered".
func TestAnAcceptedSendIsRecordedAsAcceptedNotDelivered(t *testing.T) {
	setupEmailDeliveryTestDB(t)
	RecordEmailDelivery("Someone@Example.COM", "verification", nil)

	states, err := GetEmailDeliveryStates([]string{"someone@example.com"})
	require.NoError(t, err)
	state := states["someone@example.com"]
	require.NotNil(t, state, "the address should be normalized before storage")
	assert.Equal(t, EmailDeliveryAccepted, state.LastStatus)
	assert.Equal(t, "verification", state.LastPurpose)
	assert.True(t, state.Reachable())
	assert.Empty(t, state.LastError)
	assert.Equal(t, 1, state.Attempts)
	assert.Zero(t, state.Rejections)
}

// A rejection is the one thing send time proves, so it must survive verbatim:
// "no such mailbox" and "relay denied" need completely different responses.
func TestARejectionIsKeptAsTheServerPhrasedIt(t *testing.T) {
	setupEmailDeliveryTestDB(t)
	RecordEmailDelivery("gone@example.com", "notification", errors.New("550 5.1.1 no such mailbox"))

	states, err := GetEmailDeliveryStates([]string{"gone@example.com"})
	require.NoError(t, err)
	state := states["gone@example.com"]
	require.NotNil(t, state)
	assert.Equal(t, EmailDeliveryRejected, state.LastStatus)
	assert.False(t, state.Reachable())
	assert.Contains(t, state.LastError, "no such mailbox")
	assert.Equal(t, 1, state.Rejections)
}

// Reachability is the LAST attempt, not the worst one. An address that failed
// once and then worked is reachable, or a single transient failure would
// condemn it forever.
func TestReachabilityFollowsTheLatestAttemptNotTheWorst(t *testing.T) {
	setupEmailDeliveryTestDB(t)
	RecordEmailDelivery("flaky@example.com", "notification", errors.New("451 temporary failure"))
	RecordEmailDelivery("flaky@example.com", "notification", nil)

	states, err := GetEmailDeliveryStates([]string{"flaky@example.com"})
	require.NoError(t, err)
	state := states["flaky@example.com"]
	require.NotNil(t, state)
	assert.True(t, state.Reachable(), "a later success must clear an earlier failure")
	assert.Empty(t, state.LastError, "a stale error must not linger on a healthy address")
	assert.Equal(t, 2, state.Attempts, "both attempts are counted")
	assert.Equal(t, 1, state.Rejections, "the history of failures is kept even once healthy")
}

// One row per address, however many times it is written.
func TestRepeatedSendsUpdateOneRowPerAddress(t *testing.T) {
	setupEmailDeliveryTestDB(t)
	for i := 0; i < 5; i++ {
		RecordEmailDelivery("busy@example.com", "notification", nil)
	}
	var count int64
	require.NoError(t, DB.Model(&EmailDeliveryState{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)

	states, _ := GetEmailDeliveryStates([]string{"busy@example.com"})
	assert.Equal(t, 5, states["busy@example.com"].Attempts)
}

// Recording is observability. It must never turn a delivered message into a
// failed one, so an unusable address is dropped rather than raising.
func TestRecordingNeverBreaksTheSendPath(t *testing.T) {
	setupEmailDeliveryTestDB(t)
	require.NotPanics(t, func() {
		RecordEmailDelivery("", "verification", nil)
		RecordEmailDelivery("   ", "verification", errors.New("boom"))
	})
	var count int64
	require.NoError(t, DB.Model(&EmailDeliveryState{}).Count(&count).Error)
	assert.Zero(t, count, "an empty address is not an address")
}

// The operator's actual question: which addresses need attention, worst first.
func TestUnreachableAddressesAreListedMostRecentFirst(t *testing.T) {
	setupEmailDeliveryTestDB(t)
	RecordEmailDelivery("ok@example.com", "notification", nil)
	RecordEmailDelivery("bad-old@example.com", "notification", errors.New("550 no such mailbox"))
	RecordEmailDelivery("bad-new@example.com", "notification", errors.New("550 no such mailbox"))
	require.NoError(t, DB.Model(&EmailDeliveryState{}).
		Where("email = ?", "bad-new@example.com").Update("last_attempt_at", 1<<40).Error)

	rows, err := GetUnreachableEmails(10)
	require.NoError(t, err)
	require.Len(t, rows, 2, "a healthy address must not appear in the problem list")
	assert.Equal(t, "bad-new@example.com", rows[0].Email)
}

// A batch lookup answers for many addresses at once, so an admin list does not
// issue one query per row.
func TestBatchLookupHandlesDuplicatesAndUnknownAddresses(t *testing.T) {
	setupEmailDeliveryTestDB(t)
	RecordEmailDelivery("known@example.com", "verification", nil)

	states, err := GetEmailDeliveryStates([]string{
		"KNOWN@example.com", "known@example.com", "never-written@example.com", "",
	})
	require.NoError(t, err)
	assert.Len(t, states, 1)
	assert.NotNil(t, states["known@example.com"])
	assert.Nil(t, states["never-written@example.com"], "an unknown address has no state, not a false one")
	assert.True(t, states["never-written@example.com"].Reachable())
}
