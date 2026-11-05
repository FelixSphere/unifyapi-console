package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every line below was copied from a real object in
// s3://unifyapi-alb-logs-my-871376691052 on 2026-11-05, not invented. The
// shapes are the point: three of them are indistinguishable on the TLS columns
// alone and mean completely different things.
const (
	// Handshake never completed. No protocol, no cipher, no status. This is
	// what ClientTLSNegotiationErrorCount counts.
	connHandshakeFailed = `2026-11-05T05:35:21.157264Z 115.207.110.183 37264 443 - - - "-" - - - TID_040ec8335263804299e2b347e705c209 - app/unifyapi-console-my/8051619ac00a3ef9 43.216.20.111`

	// TLS negotiated -- protocol AND cipher present -- then the connection
	// failed. CloudWatch does NOT count this one.
	connAfterHandshakeFailed = `2026-11-05T05:39:40.835503Z 38.145.218.25 41050 443 TLSv1.3 TLS_AES_128_GCM_SHA256 - "-" - - Failed:UnmappedConnectionError TID_ed7497d700c7ec42a7bda9a6fc8e753f - app/unifyapi-console-my/8051619ac00a3ef9 43.216.20.111`

	// A plain HTTP connection to the port 80 listener. Identical to
	// connHandshakeFailed on fields 5, 6 and 11 -- only the port separates
	// them. Scanners produce a steady trickle and none of it is a failure.
	connPlainHTTPScanner = `2026-11-05T05:10:02.000000Z 216.218.206.66 51000 80 - - - "-" - - - TID_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa - app/unifyapi-console-my/8051619ac00a3ef9 43.216.20.111`

	connSuccess = `2026-11-05T04:52:10.964371Z 3.227.0.122 45778 443 TLSv1.3 TLS_AES_128_GCM_SHA256 0.223 "-" - - Success TID_f0fee8bb06c6ae4c9263136e75cf6750 x25519 app/unifyapi-console-my/8051619ac00a3ef9 43.216.20.111`
)

func kinds(events []model.PlatformEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Kind)
	}
	return out
}

// The trap: on the TLS columns a port-80 scanner and a failed handshake are the
// same line. Classifying on verify status alone reports the scanners as TLS
// failures, and the count stops matching CloudWatch.
func TestPlainHTTPOnPort80IsNotATLSFailure(t *testing.T) {
	assert.Empty(t, parseConnectionLog([]string{connPlainHTTPScanner}),
		"a plain HTTP connection to the port 80 listener never attempted TLS")
}

func TestSuccessfulHandshakesAreNotRecorded(t *testing.T) {
	assert.Empty(t, parseConnectionLog([]string{connSuccess}))
}

// The two failures are different events and must not be collapsed. Only the
// first produces the "no response at all" a customer describes; the second got
// a connection and lost it, which has a different fix.
func TestTheTwoFailureShapesStaySeparate(t *testing.T) {
	events := parseConnectionLog([]string{connHandshakeFailed, connAfterHandshakeFailed})
	require.Len(t, events, 2)
	assert.Equal(t, []string{
		model.PlatformEventKindTLSHandshakeFailed,
		model.PlatformEventKindTLSConnectionFailed,
	}, kinds(events))

	assert.Equal(t, "115.207.110.183", events[0].ClientIP)
	assert.Empty(t, events[0].TLSProtocol, "a handshake that never completed has no protocol to report")

	assert.Equal(t, "38.145.218.25", events[1].ClientIP)
	assert.Equal(t, "TLSv1.3", events[1].TLSProtocol,
		"this connection DID negotiate TLS; losing that is what makes it look like the other shape")
	assert.Contains(t, events[1].Detail, "UnmappedConnectionError")
}

func TestConnectionDedupeIsTheTraceId(t *testing.T) {
	events := parseConnectionLog([]string{connHandshakeFailed})
	require.Len(t, events, 1)
	assert.Equal(t, "conn:TID_040ec8335263804299e2b347e705c209", events[0].Dedupe)
}

const (
	access401 = `https 2026-11-05T04:42:10.081636Z app/unifyapi-console-my/8051619ac00a3ef9 3.227.0.122:42656 10.21.0.134:3000 0.000 0.001 0.000 401 401 128 445 "GET https://app.unifyapi.ai:443/api/tenant/?x=1 HTTP/1.1" "unifyai-canary/1" TLS_AES_128_GCM_SHA256 TLSv1.3 arn:x "Root=1-6aec0922-75221115736e376f395d59d6" "app.unifyapi.ai" "arn:acm" 0 2026-11-05T04:42:10.080000Z "forward" "-" "-" "10.21.0.134:3000" "401" "-" "-"`
	access200 = `https 2026-11-05T04:42:11.081636Z app/unifyapi-console-my/8051619ac00a3ef9 3.227.0.122:42657 10.21.0.134:3000 0.000 0.001 0.000 200 200 128 445 "GET https://app.unifyapi.ai:443/api/status HTTP/1.1" "ELB-HealthChecker/2.0" TLS_AES_128_GCM_SHA256 TLSv1.3 arn:x "Root=1-6aec0923-aaaa" "app.unifyapi.ai" "arn:acm" 0 2026-11-05T04:42:11.080000Z "forward" "-" "-" "10.21.0.134:3000" "200" "-" "-"`
)

func TestOnlyFailedRequestsAreCopiedFromTheAccessLog(t *testing.T) {
	events := parseAccessLog([]string{access401, access200})
	require.Len(t, events, 1, "a 200 is already in `logs` with the model and the money; copying it here buries the failures")

	e := events[0]
	assert.Equal(t, 401, e.Status)
	assert.Equal(t, "3.227.0.122", e.ClientIP, "the port must be stripped")
	assert.Equal(t, "GET", e.Method)
	assert.Equal(t, "/api/tenant/", e.Path, "scheme, host and query string are not the path")
	assert.Equal(t, "access:Root=1-6aec0922-75221115736e376f395d59d6", e.Dedupe)
}

// Without a fallback every trace-less line gets the same key, the conflict
// clause keeps exactly one, and the rest vanish -- which looks identical to
// there having been only one.
func TestAccessLogWithoutATraceIdStillGetsDistinctKeys(t *testing.T) {
	a := `https 2026-11-05T04:42:10.081636Z alb 1.2.3.4:1111 10.0.0.1:3000 0 0 0 400 400 1 1 "GET https://h/a HTTP/1.1" "ua" c p arn "-" "h" "arn" 0 2026-11-05T04:42:10Z "forward" "-" "-" "-" "400" "-" "-"`
	b := `https 2026-11-05T04:42:10.081636Z alb 1.2.3.4:2222 10.0.0.1:3000 0 0 0 400 400 1 1 "GET https://h/a HTTP/1.1" "ua" c p arn "-" "h" "arn" 0 2026-11-05T04:42:10Z "forward" "-" "-" "-" "400" "-" "-"`
	events := parseAccessLog([]string{a, b})
	require.Len(t, events, 2)
	assert.NotEqual(t, events[0].Dedupe, events[1].Dedupe,
		"two different connections must not collapse into one row")
}
