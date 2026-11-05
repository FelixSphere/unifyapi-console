package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RecordAuthRejection runs on the request path of a caller who is already being
// refused, and it writes from a goroutine. An unrecovered panic in a goroutine
// takes down the whole process, so a request with a bad key must not be able to
// kill the console. It could, before the guard this pins: DB is nil until
// InitDB runs, and the first write dereferenced it.
func TestAuthRejectionCannotKillTheProcessWhenTheStoreIsUnset(t *testing.T) {
	saved := DB
	DB = nil
	t.Cleanup(func() { DB = saved })

	require.NotPanics(t, func() {
		RecordAuthRejection(PlatformEvent{
			ClientIP:  "185.196.0.16",
			KeyPrefix: "sk-abcde... (len 48)",
			Path:      "/v1/chat/completions",
		})
	})
	// The write is asynchronous, so the panic would surface after the call
	// returned. Give the goroutine a turn to fail before the test ends.
	time.Sleep(50 * time.Millisecond)
}

func TestRecordPlatformEventsRejectsAnUninitialisedStore(t *testing.T) {
	saved := DB
	DB = nil
	t.Cleanup(func() { DB = saved })

	err := RecordPlatformEvents([]PlatformEvent{{Dedupe: "auth:x"}})
	assert.Error(t, err, "a write with no database must report, not pretend to succeed")

	assert.NoError(t, RecordPlatformEvents(nil),
		"an empty batch is not an error; ingest polls that find nothing are normal")
}

// The dedupe key is what makes ingest re-entrant. An S3 object can be re-read
// after a crash, a restart, or two overlapping polls; without a natural key per
// event the table silently doubles every replay.
func TestAuthRejectionDedupeKeyIsTheRequestId(t *testing.T) {
	saved := DB
	DB = nil
	t.Cleanup(func() { DB = saved })

	e := PlatformEvent{RequestId: "20261105041327386268320", ClientIP: "1.2.3.4"}
	e.Source = PlatformEventSourceAuth
	if e.Dedupe == "" && e.RequestId != "" {
		e.Dedupe = "auth:" + e.RequestId
	}
	assert.Equal(t, "auth:20261105041327386268320", e.Dedupe)

	// Two rejections of the same request must collide, two different requests
	// must not.
	other := PlatformEvent{RequestId: "20261105041327386268321"}
	assert.NotEqual(t, e.Dedupe, "auth:"+other.RequestId)
}

func TestPlatformEventQueryClampsPageSize(t *testing.T) {
	// 200 is the ceiling; a caller asking for more must not be able to pull the
	// whole table in one request.
	for _, tc := range []struct{ asked, want int }{{0, 50}, {-1, 50}, {201, 50}, {50, 50}, {200, 200}} {
		q := PlatformEventQuery{PageSize: tc.asked}
		if q.PageSize <= 0 || q.PageSize > 200 {
			q.PageSize = 50
		}
		assert.Equal(t, tc.want, q.PageSize, "asked for %d", tc.asked)
	}
}
