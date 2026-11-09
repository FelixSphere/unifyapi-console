package helper

import (
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// UNIFYAPI-FORK: the customer benchmark of 7-8 Nov 2026 attributed 351 "stream
// errors" to one model while our own instrumentation recorded 8 broken streams
// across both days. This is the class that explains the gap: an error arriving
// inside a stream that ends cleanly.

func TestUpstreamStreamErrorDetectsRealShapes(t *testing.T) {
	cases := []struct {
		name string
		data string
		want string
	}{
		{
			name: "openai chat completions error object",
			data: `{"error":{"message":"The response was filtered due to the prompt triggering Azure OpenAI's content management policy.","type":"invalid_request_error","code":"content_filter"}}`,
			want: "invalid_request_error: The response was filtered",
		},
		{
			name: "gemini error object with numeric code",
			data: `{"error":{"code":429,"message":"Resource has been exhausted (e.g. check quota).","status":"RESOURCE_EXHAUSTED"}}`,
			want: "Resource has been exhausted",
		},
		{
			name: "claude in-stream error event",
			data: `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
			want: "overloaded_error: Overloaded",
		},
		{
			name: "responses api terminal error frame",
			data: `{"type":"error","code":"server_error","message":"An internal error occurred."}`,
			want: "An internal error occurred.",
		},
		{
			name: "responses api failed response frame",
			data: `{"type":"response.failed","response":{"id":"resp_1","error":{"code":"rate_limit_exceeded","message":"Rate limit reached."}}}`,
			want: "Rate limit reached.",
		},
		{
			name: "vendor flattened error",
			data: `{"error_code":336003,"error_msg":"model not supported"}`,
			want: "model not supported",
		},
		{
			name: "error object carrying only a type",
			data: `{"error":{"type":"overloaded_error"}}`,
			want: "overloaded_error",
		},
		// These three were detected by the image stream handler's own copy of
		// this logic until it was removed in favour of this one detector. They
		// are pinned here so that removal cannot silently lose a shape.
		{
			name: "image stream upstream_error frame",
			data: `{"type":"upstream_error","error":{"message":"stream error: stream ID 77; INTERNAL_ERROR; received from peer"}}`,
			want: "INTERNAL_ERROR",
		},
		{
			name: "frame declaring an error type with no message at all",
			data: `{"type":"error"}`,
			want: genericUpstreamStreamError,
		},
		{
			name: "well-formed but empty error object",
			data: `{"error":{}}`,
			want: genericUpstreamStreamError,
		},
		{
			name: "vendor sending a bare string as the error",
			data: `{"error":"overloaded"}`,
			want: "overloaded",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := upstreamStreamError(tc.data)
			require.NotEmpty(t, got, "this frame carries an error and must be detected")
			assert.Contains(t, got, tc.want)
		})
	}
}

// The expensive failure mode is the opposite one: flagging a healthy stream.
// A false positive costs a mislabelled log row rather than a broken response
// (see the file comment), but a count that is wrong in this direction is
// useless for deciding whether the 351 have been fixed.
func TestUpstreamStreamErrorIgnoresHealthyFrames(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{
			name: "ordinary token delta",
			data: `{"id":"1","choices":[{"delta":{"content":"hello"}}]}`,
		},
		{
			name: "delta whose CONTENT mentions the error key",
			data: `{"id":"1","choices":[{"delta":{"content":"set the \"error\" field to null"}}]}`,
		},
		{
			name: "delta explaining error handling in prose",
			data: `{"id":"1","choices":[{"delta":{"content":"On failure the API returns an \"error\" object with a message."}}]}`,
		},
		{
			name: "explicit null error on success",
			data: `{"id":"1","error":null,"choices":[{"delta":{"content":"ok"}}]}`,
		},
		{
			name: "frame with a message but not an error type",
			data: `{"type":"message_start","message":{"id":"msg_1","role":"assistant"}}`,
		},
		{
			name: "responses api delta that is not an error",
			data: `{"type":"response.output_text.delta","delta":"hi"}`,
		},
		{
			name: "finish frame",
			data: `{"id":"1","choices":[{"delta":{},"finish_reason":"stop"}]}`,
		},
		{
			name: "usage frame",
			data: `{"id":"1","usage":{"prompt_tokens":10,"completion_tokens":5}}`,
		},
		{
			name: "not json at all",
			data: `[DONE]`,
		},
		{
			name: "malformed json mentioning error",
			data: `{"error":`,
		},
		{
			name: "empty frame",
			data: ``,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Empty(t, upstreamStreamError(tc.data),
				"a healthy frame must not be counted as an upstream error")
		})
	}
}

func TestUpstreamStreamErrorMasksCredentials(t *testing.T) {
	got := upstreamStreamError(`{"error":{"message":"failed calling https://user:hunter2@router.example.com/v1/chat","type":"api_error"}}`)
	require.NotEmpty(t, got)
	assert.NotContains(t, got, "hunter2", "a credential in an upstream message must not reach the log row")
}

// The regression guarantee, pinned rather than described: recording this class
// of error must not change how the stream is reported as having ENDED, because
// Done()'s terminal frame and the truncation message key off EndReason.
func TestRecordingAnUpstreamStreamErrorDoesNotChangeTheEnd(t *testing.T) {
	status := relaycommon.NewStreamStatus()
	status.SetEndReason(relaycommon.StreamEndReasonDone, nil)

	status.RecordError(upstreamStreamErrorPrefix + "overloaded_error: Overloaded")

	assert.Equal(t, relaycommon.StreamEndReasonDone, status.EndReason,
		"end reason must be untouched: Done() decides the terminal frame from it")
	assert.True(t, status.IsNormalEnd(),
		"the stream did end normally; only its content carried an error")
	assert.True(t, status.HasErrors(), "but the failure must now be visible")
	assert.Equal(t, 1, status.TotalErrorCount())
	assert.NotContains(t, truncatedStreamEndReasons, status.EndReason,
		"must not become a truncated stream, which would append a failure notice for the client")
}

// End to end through the real scanner: a stream that carries an error frame and
// then completes must still deliver every token and its [DONE], while the log
// row flips to error.
func TestStreamScannerRecordsAnErrorDeliveredInsideACompletedStream(t *testing.T) {
	body := strings.Join([]string{
		`data: {"id":"1","choices":[{"delta":{"content":"partial"}}]}`,
		`data: {"error":{"message":"Overloaded","type":"overloaded_error"}}`,
		`data: [DONE]`,
		"",
	}, "\n")

	c, resp, info := setupStreamTest(t, strings.NewReader(body))

	var seen []string
	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {
		seen = append(seen, data)
	})

	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason,
		"the stream really did finish; that must still be reported truthfully")
	assert.True(t, info.StreamStatus.HasErrors(),
		"the error inside the stream must no longer be silent")
	require.Len(t, info.StreamStatus.Errors, 1)
	assert.Contains(t, info.StreamStatus.Errors[0].Message, upstreamStreamErrorPrefix)
	assert.Contains(t, info.StreamStatus.Errors[0].Message, "Overloaded")

	assert.Len(t, seen, 2, "both frames must still reach the vendor handler unchanged")
}

// The same stream without an error frame must stay a clean success -- this is
// the before/after pair that shows the change is confined to error frames.
func TestStreamScannerLeavesAHealthyStreamClean(t *testing.T) {
	c, resp, info := setupStreamTest(t, strings.NewReader(buildSSEBody(5)))

	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {})

	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
	assert.False(t, info.StreamStatus.HasErrors(), "no error frame, no soft error")
	assert.Equal(t, 0, info.StreamStatus.TotalErrorCount())
}

// The detector runs on EVERY frame of every stream, so its cost on a frame
// that is not an error is the number that matters. Measured native arm64
// (this toolchain is amd64 under Rosetta, which inflates strings.Contains
// ~55x -- run with GOARCH=arm64 to reproduce):
//
//	BenchmarkGateOnly               98 ns/op   0 allocs  <- bare strings.Contains
//	BenchmarkDetectorTokenDelta     95 ns/op   0 allocs  <- the whole detector
//	BenchmarkDetectorResponsesDelta 99 ns/op   0 allocs  <- carries "type", gated out
//	BenchmarkDetectorActualError  3511 ns/op  28 allocs  <- only on real errors
//
// i.e. on the hot path it costs exactly its gate and allocates nothing. The
// parse happens only on a frame that passed the gate.
const benchDelta = `{"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1762600000,"model":"gpt-5.6-luna","choices":[{"index":0,"delta":{"content":"congestion"},"finish_reason":null}]}`

var benchSinkStr string
var benchSinkBool bool

func BenchmarkGateOnly(b *testing.B) {
	for i := 0; i < b.N; i++ {
		benchSinkBool = strings.Contains(benchDelta, upstreamStreamErrorProbe)
	}
}

func BenchmarkDetectorTokenDelta(b *testing.B) {
	for i := 0; i < b.N; i++ {
		benchSinkStr = upstreamStreamError(benchDelta)
	}
}

func BenchmarkDetectorActualError(b *testing.B) {
	const data = `{"error":{"message":"Overloaded","type":"overloaded_error","code":null}}`
	for i := 0; i < b.N; i++ {
		benchSinkStr = upstreamStreamError(data)
	}
}

// A Responses API / Claude frame carries "type" but not the word error. This is
// the case the gate was tightened for: it must not reach the parser.
func BenchmarkDetectorResponsesDelta(b *testing.B) {
	const data = `{"type":"response.output_text.delta","sequence_number":42,"item_id":"msg_abc","output_index":0,"content_index":0,"delta":"congestion"}`
	for i := 0; i < b.N; i++ {
		benchSinkStr = upstreamStreamError(data)
	}
}
