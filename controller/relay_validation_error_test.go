package controller

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A request the caller got wrong must not be reported as a server fault.
// Observed in production 2026-09-24: an empty messages array, a wrong-typed
// messages field and a negative max_tokens all returned 500 while the body
// said "invalid_request". SDKs retry 5xx, so a typo was retried with backoff,
// and the same requests inflated our own 5xx alarms.
func TestRequestValidationErrorUsesBadRequest(t *testing.T) {
	for _, message := range []string{
		"field messages is required",
		"model is required",
		"max_tokens is invalid",
		"invalid search_context_size, must be one of: high, medium, low",
		"json: cannot unmarshal number -5 into Go struct field GeneralOpenAIRequest.max_tokens of type uint",
	} {
		t.Run(message, func(t *testing.T) {
			apiErr := requestValidationError(errors.New(message))
			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
		})
	}
}

// An oversized body keeps its own status: 413 is what lets a client tell
// "too big" apart from "malformed".
func TestRequestValidationErrorKeepsRequestEntityTooLarge(t *testing.T) {
	apiErr := requestValidationError(common.ErrRequestBodyTooLarge)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusRequestEntityTooLarge, apiErr.StatusCode)

	wrapped := requestValidationError(fmt.Errorf("read body: %w", common.ErrRequestBodyTooLarge))
	require.NotNil(t, wrapped)
	assert.Equal(t, http.StatusRequestEntityTooLarge, wrapped.StatusCode)
}

// A validator that already chose a status keeps it. Flattening everything to
// 400 would be the same class of bug in the other direction.
func TestRequestValidationErrorPreservesAlreadyChosenStatus(t *testing.T) {
	for _, status := range []int{
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusRequestEntityTooLarge,
		http.StatusInternalServerError,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			original := types.NewErrorWithStatusCode(
				errors.New("chosen deeper in validation"),
				types.ErrorCodeInvalidRequest,
				status,
			)
			apiErr := requestValidationError(fmt.Errorf("wrapped: %w", original))
			require.NotNil(t, apiErr)
			assert.Equal(t, status, apiErr.StatusCode)
		})
	}
}
