package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A model the gateway does not serve is the caller's mistake, not our outage.
// The distributor used to answer 503 to both, so a typo came back as a
// retryable server error: SDKs retried it with backoff and it counted against
// our own 5xx alarm. These pin the distinction the status code now rests on.
func TestModelIsServed(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&Ability{}))
	t.Cleanup(func() { DB.Where("1 = 1").Delete(&Ability{}) })

	enabledPriority := int64(0)
	require.NoError(t, DB.Create(&Ability{
		Group: "default", Model: "served-and-enabled", ChannelId: 1,
		Enabled: true, Priority: &enabledPriority,
	}).Error)
	// Every channel for this one is switched off. It is still ours, and the
	// outage is recoverable, so it must NOT be reported as an unknown model.
	require.NoError(t, DB.Create(&Ability{
		Group: "default", Model: "served-but-all-disabled", ChannelId: 2,
		Enabled: false, Priority: &enabledPriority,
	}).Error)

	cases := []struct {
		name     string
		model    string
		expected bool
	}{
		{name: "a model with an enabled channel is served", model: "served-and-enabled", expected: true},
		{name: "a model whose channels are all disabled is still served", model: "served-but-all-disabled", expected: true},
		{name: "a name with no row at all is not served", model: "definitely-not-a-real-model", expected: false},
		{name: "a near miss is not served", model: "served-and-enable", expected: false},
		{name: "the empty model is not served", model: "", expected: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, ModelIsServed(tc.model))
		})
	}
}
