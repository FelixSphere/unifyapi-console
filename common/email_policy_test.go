package common

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsEmailDomainBlockedMatchesExactDomainsAndSubdomains(t *testing.T) {
	blocklist := ParseEmailDomainList("maildrop.cc\n*.auroracovia.com\n@Uberip.com\n")
	tests := []struct {
		email   string
		blocked bool
	}{
		{email: "bot@maildrop.cc", blocked: true},
		{email: "bot@MAILDROP.CC", blocked: true},
		{email: "bot@x7.auroracovia.com", blocked: true},
		{email: "bot@auroracovia.com", blocked: true},
		{email: "bot@uberip.com", blocked: true},
		{email: "bot@mx.uberip.com.", blocked: true},
		// A shared suffix is not a subdomain.
		{email: "user@notmaildrop.cc", blocked: false},
		{email: "user@maildrop.cc.example.com", blocked: false},
		{email: "user@gmail.com", blocked: false},
		{email: "not-an-email", blocked: false},
	}
	for _, test := range tests {
		t.Run(test.email, func(t *testing.T) {
			assert.Equal(t, test.blocked, IsEmailDomainBlocked(test.email, blocklist))
		})
	}
}

func TestParseEmailDomainListNormalizesPastedEntries(t *testing.T) {
	assert.Equal(t,
		[]string{"maildrop.cc", "auroracovia.com", "uberip.com", "yzcalo.com"},
		ParseEmailDomainList(" MailDrop.cc \r\n\n*.auroracovia.com,@uberip.com\n.yzcalo.com.\nmaildrop.cc\n"))
	assert.Empty(t, ParseEmailDomainList(" \n,\n"))
}

// The default list is what production enforces before anyone edits it: it
// must refuse every provider seen in the 2026-09-28/29 incident, and it must
// not refuse the mainstream mailbox providers real customers use.
func TestDefaultEmailDomainBlocklistCoversTheIncidentAndSparesMainstreamMail(t *testing.T) {
	blocklist := ParseEmailDomainList(strings.Join(defaultDisposableEmailDomains, "\n"))
	for _, email := range []string{
		"a@maildrop.cc", "a@mailto.plus", "a@uberip.com", "a@yzcalo.com",
		"a@q1.auroracovia.com", "a@gmeenramy.com",
	} {
		assert.True(t, IsEmailDomainBlocked(email, blocklist), email)
	}
	for _, email := range []string{
		"a@gmail.com", "a@googlemail.com", "a@outlook.com", "a@hotmail.com", "a@yahoo.com",
		"a@icloud.com", "a@proton.me", "a@qq.com", "a@163.com", "a@company.com",
	} {
		assert.False(t, IsEmailDomainBlocked(email, blocklist), email)
	}
}
