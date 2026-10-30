package controller

import (
	"net/url"
	"testing"

	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
)

// A plus-addressed mailbox is ordinary — Gmail hands them out for exactly this
// kind of sign-up. Interpolated raw, the "+" reached the browser as a space,
// the confirm step looked the code up under a different address, and the user
// was told the link was invalid or expired. Nothing logged an error.
func TestPasswordResetLinkSurvivesAPlusAddressedEmail(t *testing.T) {
	previousAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://app.example.com"
	t.Cleanup(func() { system_setting.ServerAddress = previousAddress })

	link := passwordResetLink("ada+unifyapi@example.com", "abc123")

	parsed, err := url.Parse(link)
	assert.NoError(t, err)
	assert.Equal(t, "/user/reset", parsed.Path)
	assert.Equal(t, "ada+unifyapi@example.com", parsed.Query().Get("email"))
	assert.Equal(t, "abc123", parsed.Query().Get("token"))
}

func TestPasswordResetLinkToleratesATrailingSlash(t *testing.T) {
	previousAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://app.example.com/"
	t.Cleanup(func() { system_setting.ServerAddress = previousAddress })

	assert.Equal(
		t,
		"https://app.example.com/user/reset?email=ada%40example.com&token=abc123",
		passwordResetLink("ada@example.com", "abc123"),
	)
}
