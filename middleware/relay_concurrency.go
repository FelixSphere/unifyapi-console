package middleware

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

var inFlightRelays atomic.Int64

// InFlightRelays reports how many relay requests are being served right now.
func InFlightRelays() int64 {
	return inFlightRelays.Load()
}

// RelayConcurrencyLimit refuses a relay request once too many are already in
// flight, and is a no-op while common.GetMaxConcurrentRelays() is 0.
//
// The backpressure here is rejection, not queueing, and that is the design
// decision worth stating. A queue in front of a streaming relay keeps the
// caller's connection open while it waits, so it holds exactly the memory and
// file descriptors the cap exists to protect and turns an overload into a
// slower overload that fails later and less clearly. Refusing at once with 503
// and Retry-After lets an SDK back off while the streams already running finish
// at full speed.
func RelayConcurrencyLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		limit := common.GetMaxConcurrentRelays()
		if limit <= 0 {
			c.Next()
			return
		}

		// Reserve first, then test. Reading the count and incrementing it
		// separately would let two racing requests both see room and both take
		// the last slot. The release is deferred on the same path that
		// reserved, so a normal finish, an error return, a panic unwinding
		// through here and a caller hanging up mid-stream all give the permit
		// back — a leaked permit would wedge the gateway permanently, which is
		// worse than having no cap at all.
		inFlight := inFlightRelays.Add(1)
		defer inFlightRelays.Add(-1)

		if inFlight > limit {
			err := types.NewErrorWithStatusCode(
				fmt.Errorf("server is at its relay concurrency limit of %d; retry shortly", limit),
				"server_busy", http.StatusServiceUnavailable)
			c.Header("Retry-After", "1")
			if strings.HasPrefix(c.Request.URL.Path, "/v1/messages") {
				c.JSON(err.StatusCode, gin.H{"error": err.ToClaudeError()})
			} else {
				c.JSON(err.StatusCode, gin.H{"error": err.ToOpenAIError()})
			}
			c.Abort()
			return
		}

		c.Next()
	}
}
