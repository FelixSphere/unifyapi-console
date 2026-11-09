package types

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// UNIFYAPI-FORK: ErrOptionWithHideErrMsg used to destroy the cause.
//
// Upstream new-api overwrites Err outright, so once an error was constructed the
// only surviving text was the replacement. In production that meant every
// upstream transport failure read `upstream error: do request failed` in the log
// row AND in the backend log, and the real reason was unrecoverable. After the
// container was redeployed and docker logs rotated, ~278 such failures from the
// 2026-11-07/08 benchmark could not be explained at all -- we had to ask the
// provider what had happened on our own calls.

func TestHideErrMsgKeepsTheCauseForAdmins(t *testing.T) {
	cause := errors.New("Post \"https://router.example.ai/v1/chat/completions\": EOF")
	err := NewError(cause, ErrorCodeDoRequestFailed,
		ErrOptionWithHideErrMsg("upstream error: do request failed"))

	// What the client sees is unchanged -- hiding the text was the point.
	require.Equal(t, "upstream error: do request failed", err.Error())
	require.Equal(t, "status_code=500, upstream error: do request failed",
		err.ErrorWithStatusCode())
	require.Equal(t, "status_code=500, upstream error: do request failed",
		err.MaskSensitiveErrorWithStatusCode(),
		"the log's content column stays masked; the cause goes to admin_info only")

	// What an admin can now recover. MaskSensitiveInfo replaces the URL's host
	// and path, which costs nothing: the same log row carries channel_id and
	// channel_name. The failure KIND -- the part that was missing -- survives,
	// and that is what distinguishes a reset from a timeout from a GOAWAY.
	hidden := err.HiddenError()
	require.Contains(t, hidden, "EOF")
	require.NotContains(t, hidden, "router.example.ai")
}

// Each transport failure kind has to stay legible through the masker, or the
// accessor buys us nothing.
func TestHiddenErrorKeepsTheFailureKind(t *testing.T) {
	for _, kind := range []string{
		"EOF",
		"read: connection reset by peer",
		"context canceled",
		"net/http: timeout awaiting response headers",
		"http2: server sent GOAWAY and closed the connection",
	} {
		err := NewError(errors.New("Post \"https://router.example.ai/v1/chat/completions\": "+kind),
			ErrorCodeDoRequestFailed,
			ErrOptionWithHideErrMsg("upstream error: do request failed"))
		require.Contains(t, err.HiddenError(), kind, "kind %q must survive masking", kind)
	}
}

// Pins what the masking actually covers, so nobody later assumes more than it
// gives. URLs are masked; a bare credential in free text is NOT. That is a
// property of MaskSensitiveInfo shared with the existing MaskSensitiveError
// path, not something introduced here -- but it is the reason HiddenError is
// admin-only and must never reach a response body.
func TestHiddenErrorMaskingCoversURLsNotBareCredentials(t *testing.T) {
	withURL := NewError(errors.New("Post \"https://router.example.ai/v1/chat?key=abc\": EOF"),
		ErrorCodeDoRequestFailed, ErrOptionWithHideErrMsg("masked"))
	require.NotContains(t, withURL.HiddenError(), "router.example.ai")

	bare := NewError(errors.New("dial failed for key sk-abcdefghijklmnopqrstuvwxyz0123456789"),
		ErrorCodeDoRequestFailed, ErrOptionWithHideErrMsg("masked"))
	require.Contains(t, bare.HiddenError(), "sk-abcdefghijklmnopqrstuvwxyz0123456789",
		"documented limit: the masker does not scrub bare tokens in free text")
}

func TestHiddenErrorIsEmptyWhenNothingWasHidden(t *testing.T) {
	require.Equal(t, "", (*NewAPIError)(nil).HiddenError())

	plain := NewError(errors.New("boom"), ErrorCodeDoRequestFailed)
	require.Equal(t, "", plain.HiddenError(),
		"no hide option was applied, so there is no masked cause to report")
	require.Equal(t, "boom", plain.Error())
}

// The option is also used on non-500 paths; preserving the cause must not
// disturb the status code or the error code carried alongside it.
func TestHideErrMsgLeavesTheRestOfTheErrorAlone(t *testing.T) {
	err := NewErrorWithStatusCode(errors.New("real reason"),
		ErrorCodeChannelParamOverrideInvalid, http.StatusBadRequest,
		ErrOptionWithHideErrMsg("masked"))

	require.Equal(t, http.StatusBadRequest, err.StatusCode)
	require.Equal(t, ErrorCodeChannelParamOverrideInvalid, err.GetErrorCode())
	require.Equal(t, "masked", err.Error())
	require.Equal(t, "real reason", err.HiddenError())
}
