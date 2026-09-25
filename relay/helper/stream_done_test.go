/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
 * `data: [DONE]` is a claim that the response is COMPLETE.
 *
 * Measured on 2026-09-24 against a mock upstream that streamed three chunks and
 * then dropped the TCP connection: the caller received the three chunks, a
 * synthesised usage frame, `data: [DONE]` and HTTP 200, and was billed for the
 * partial answer. The server logged `reason=scanner_error end_error="unexpected
 * EOF"` -- so we knew the answer was cut short and the customer did not.
 *
 * A truncated stream must end with an error an SDK can raise on, not with a
 * claim of success.
 */
package helper

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func doneWithEndReason(t *testing.T, reason relaycommon.StreamEndReason, endErr error) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	if reason != relaycommon.StreamEndReasonNone {
		status := relaycommon.NewStreamStatus()
		status.SetEndReason(reason, endErr)
		rememberStreamStatus(ctx, status)
	}
	Done(ctx)
	return recorder.Body.String()
}

func TestATruncatedStreamDoesNotClaimToBeDone(t *testing.T) {
	body := doneWithEndReason(t, relaycommon.StreamEndReasonScannerErr, assert.AnError)

	assert.NotContains(t, body, "[DONE]",
		"a stream cut short must not tell the client the response is complete")
	assert.Contains(t, body, `"error"`, "it must say what went wrong instead")
	assert.Contains(t, body, "upstream_error")

	// An SDK parses the frame, so it has to be the OpenAI error envelope.
	payload := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(body), "data:"))
	var parsed struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(payload), &parsed), "body was %q", body)
	assert.NotEmpty(t, parsed.Error.Message)
	assert.NotEmpty(t, parsed.Error.Type)
	assert.NotEmpty(t, parsed.Error.Code)
	assert.Contains(t, parsed.Error.Message, assert.AnError.Error(),
		"the upstream's own reason is the useful part")
}

func TestATimedOutStreamIsAlsoAnError(t *testing.T) {
	body := doneWithEndReason(t, relaycommon.StreamEndReasonTimeout, nil)
	assert.NotContains(t, body, "[DONE]")
	assert.Contains(t, body, `"error"`)
}

func TestACompleteStreamStillEndsWithDone(t *testing.T) {
	for _, reason := range []relaycommon.StreamEndReason{
		relaycommon.StreamEndReasonDone,
		relaycommon.StreamEndReasonEOF,
		relaycommon.StreamEndReasonHandlerStop,
	} {
		body := doneWithEndReason(t, reason, nil)
		assert.Contains(t, body, "[DONE]", "reason %q ends a stream normally", reason)
		assert.NotContains(t, body, `"error"`)
	}
}

func TestAStreamWithNoRecordedStatusKeepsTheOldBehaviour(t *testing.T) {
	// Several vendor handlers call Done without going through
	// StreamScannerHandler. They must be unaffected.
	body := doneWithEndReason(t, relaycommon.StreamEndReasonNone, nil)
	assert.Contains(t, body, "[DONE]")
}

func TestNothingIsWrittenToAClientThatHasGone(t *testing.T) {
	// There is nobody to tell, and an error frame written into a dead
	// connection is just noise in the logs.
	body := doneWithEndReason(t, relaycommon.StreamEndReasonClientGone, nil)
	assert.Empty(t, body)
}
