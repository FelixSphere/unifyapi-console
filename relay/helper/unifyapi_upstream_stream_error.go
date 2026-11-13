package helper

import (
	"encoding/json"
	"strings"

	"github.com/QuantumNous/new-api/common"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
)

// UNIFYAPI-FORK: fork-owned. Detects an error payload delivered INSIDE an
// otherwise well-formed SSE stream, so it stops being recorded as a success.
//
// # Why this exists
//
// Measured against the customer benchmark of 7-8 Nov 2026: the report
// attributed 351 "stream errors" to one model, while our own stream
// instrumentation recorded 8 broken streams across both days in total. The
// difference is this class -- the upstream answers 200, opens a stream, puts an
// error object in a data frame, and closes the stream cleanly with [DONE]. From
// the relay's point of view nothing failed: end_reason is "done", status is
// "ok", and the row is logged and billed as a completed request. The customer
// counts it as a failure, and they are right to.
//
// # Why detection lives in the scanner and not in the ten vendor handlers
//
// Every channel reaches the wire through StreamScannerHandler, and none of the
// vendor dataHandlers inspect a chunk for an error object -- they look for
// tokens and tool calls and ignore what they do not recognise. Putting the
// check in each handler would mean ten copies and a count that depends on which
// upstream served the request. One check in the scanner covers every channel.
//
// # Why this cannot change a response
//
// It calls StreamResult-equivalent soft-error recording only. StreamStatus
// soft errors are read in three places: the stream_status.status field of the
// log row, the choice between LogInfo and LogError when the stream ends, and
// controller.affinityOutcome, which counts such a request against the channel
// its affinity key is pinned to. They do not reach Done()'s terminal frame
// (which keys off EndReason via truncatedStreamEndReasons), the data forwarded
// to the client, or billing. So a false positive costs a mislabelled log row,
// never a broken response; several in a row on one affinity key would also move
// that key to another channel (service/unifyapi_channel_affinity_failover.go).

// upstreamStreamErrorProbe is the cheap gate. A chunk that does not contain the
// substring cannot be any of the shapes below, and the overwhelming majority of
// chunks are token deltas that do not contain it. A chunk whose generated
// CONTENT happens to mention the word is parsed and then correctly rejected:
// content arrives as a JSON string value and can never introduce a top-level
// "error" key. So this gate is followed by a real parse, not a guess.
// The needle is the bare word, not `"error`, so one gate covers every shape:
// the "error" key, "type":"error", "type":"upstream_error" and "error_msg".
// Gating on `"type"` instead would admit nearly every Responses API and Claude
// frame -- they all carry one -- and move a JSON parse onto the hot path.
const upstreamStreamErrorProbe = "error"

// upstreamStreamErrorPayload covers the shapes our upstreams actually send.
// Each field is optional; a chunk matching none of them yields no message.
//
// Error and Response.Error are RawMessage rather than a struct because some
// vendors send a bare string there ({"error":"overloaded"}). Parsing into a
// struct would fail on those and silently drop a real error.
type upstreamStreamErrorPayload struct {
	// OpenAI chat completions, Gemini, and most OpenAI-compatible vendors:
	//   {"error":{"message":"...","type":"...","code":"..."}}
	Error json.RawMessage `json:"error"`

	// OpenAI Responses API terminal error frame:
	//   {"type":"error","message":"...","code":"..."}
	// and the image streams' {"type":"upstream_error",...}.
	Type    string `json:"type"`
	Message string `json:"message"`

	// OpenAI Responses API failed-response frame:
	//   {"type":"response.failed","response":{"error":{...}}}
	Response *struct {
		Error json.RawMessage `json:"error"`
	} `json:"response"`

	// Baidu and other vendors that flatten the error:
	//   {"error_code":336003,"error_msg":"..."}
	ErrorMsg string `json:"error_msg"`
}

type upstreamStreamErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

// errorFrameTypes are the self-declared error frames. "upstream_error" is what
// the OpenAI image streams send; it is handled here rather than in the image
// handler so there is one detector and therefore one count.
var errorFrameTypes = map[string]struct{}{
	"error":          {},
	"upstream_error": {},
}

// genericUpstreamStreamError describes an error frame that carries no message
// of its own. It still has to be counted: no healthy frame has a top-level
// "error" key or declares itself an error type.
const genericUpstreamStreamError = "upstream returned an error frame"

// upstreamStreamError returns a short description of the error carried by this
// SSE data frame, or "" when the frame is not an error.
//
// data is the frame with its "data:" prefix already stripped by the scanner.
func upstreamStreamError(data string) string {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, "{") {
		return ""
	}
	if !strings.Contains(trimmed, upstreamStreamErrorProbe) {
		return ""
	}

	var payload upstreamStreamErrorPayload
	if err := common.UnmarshalJsonStr(trimmed, &payload); err != nil {
		return ""
	}

	if msg := describeRawError(payload.Error); msg != "" {
		return kitutil.MaskSensitiveInfo(msg)
	}
	if payload.Response != nil {
		if msg := describeRawError(payload.Response.Error); msg != "" {
			return kitutil.MaskSensitiveInfo(msg)
		}
	}
	if payload.ErrorMsg != "" {
		return kitutil.MaskSensitiveInfo(payload.ErrorMsg)
	}
	// A frame that only declares itself an error, e.g. the Responses API's
	// {"type":"error","message":"..."}. Keyed on Type so an ordinary delta
	// carrying a "message" field is not mistaken for one.
	if _, ok := errorFrameTypes[strings.ToLower(strings.TrimSpace(payload.Type))]; ok {
		if payload.Message != "" {
			return kitutil.MaskSensitiveInfo(payload.Message)
		}
		return genericUpstreamStreamError
	}
	return ""
}

// describeRawError turns the value of an "error" key into a description.
// An absent key and an explicit null both mean "no error" -- several vendors
// send {"error":null} on a healthy frame.
func describeRawError(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}

	var body upstreamStreamErrorBody
	if err := common.Unmarshal(raw, &body); err == nil {
		if msg := body.describe(); msg != "" {
			return msg
		}
		// A well-formed but empty object, {"error":{}}.
		return genericUpstreamStreamError
	}

	// Not an object: a bare string or number. Report it as given.
	var asString string
	if err := common.Unmarshal(raw, &asString); err == nil {
		if asString = strings.TrimSpace(asString); asString != "" {
			return asString
		}
		return genericUpstreamStreamError
	}
	return genericUpstreamStreamError
}

func (b *upstreamStreamErrorBody) describe() string {
	if b == nil {
		return ""
	}
	switch {
	case b.Message != "" && b.Type != "":
		return b.Type + ": " + b.Message
	case b.Message != "":
		return b.Message
	case b.Type != "":
		return b.Type
	default:
		return ""
	}
}

// upstreamStreamErrorPrefix marks these apart from the write errors that
// StreamResult.Error records, so a query can count one without the other.
const upstreamStreamErrorPrefix = "upstream_stream_error: "
