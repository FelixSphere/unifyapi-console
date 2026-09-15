package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every SendEmail caller must be recorded without having to remember to. The
// wrapper is what guarantees that, so this pins the wiring rather than the
// recorder's own behaviour.
func TestEverySendIsHandedToTheRecorder(t *testing.T) {
	type call struct {
		receiver string
		purpose  string
		failed   bool
	}
	var calls []call
	SetEmailDeliveryRecorder(func(receiver, purpose string, err error) {
		calls = append(calls, call{receiver, purpose, err != nil})
	})
	t.Cleanup(func() { SetEmailDeliveryRecorder(nil) })

	// SMTP is unconfigured in tests, so the send fails and the recorder must
	// still be told -- a rejection is precisely what we are trying to capture.
	require.Error(t, SendEmail("subject", "someone@example.com", "body"))
	require.Error(t, SendEmailForPurpose("verification", "subject", "other@example.com", "body"))

	require.Len(t, calls, 2)
	assert.Equal(t, call{"someone@example.com", "", true}, calls[0])
	assert.Equal(t, call{"other@example.com", "verification", true}, calls[1],
		"the purpose must reach the recorder so a failed code differs from a failed invoice")
}

// Sending before the recorder is installed must not panic; recording is
// optional observability, not a precondition for mail.
func TestSendingWithoutARecorderIsSafe(t *testing.T) {
	SetEmailDeliveryRecorder(nil)
	assert.NotPanics(t, func() { _ = SendEmail("subject", "someone@example.com", "body") })
}
