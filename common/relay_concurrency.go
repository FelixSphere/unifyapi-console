package common

import "sync/atomic"

// maxConcurrentRelays is the only ceiling this process has on how many relay
// requests it carries at once.
//
// The existing limiters count requests per window, per IP, in Redis. A relay
// request is not an event, it is an occupation: a streaming response holds a
// goroutine, an upstream TCP connection, a scanner buffer and the caller's
// socket for its whole life, which can be minutes. Ten requests per minute is a
// trivial rate and can still be a hundred concurrent streams. Nothing measured
// that until now.
//
// 0 disables the cap and 0 is the default, deliberately. A ceiling set too low
// is indistinguishable from an outage, so an upgrade must never impose one that
// nobody chose. Set RELAY_MAX_CONCURRENT once the instance's real ceiling is
// known.
var maxConcurrentRelays atomic.Int64

// GetMaxConcurrentRelays returns the configured cap, or 0 when it is off.
func GetMaxConcurrentRelays() int64 {
	return maxConcurrentRelays.Load()
}

// SetMaxConcurrentRelays sets the cap. A negative value is treated as off
// rather than rejected, so a mistyped setting degrades to today's behaviour
// instead of refusing every request.
func SetMaxConcurrentRelays(limit int64) {
	if limit < 0 {
		limit = 0
	}
	maxConcurrentRelays.Store(limit)
}
