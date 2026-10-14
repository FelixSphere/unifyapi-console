package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsEmailAliasAddressTreatsDotsAsAliasesOnlyOnGmail(t *testing.T) {
	tests := []struct {
		email string
		alias bool
	}{
		// "+" sub-addressing is an alias on every domain.
		{email: "aenomkali+x7@gmail.com", alias: true},
		{email: "user+tag@company.com", alias: true},
		{email: "+@example.org", alias: true},
		// Gmail ignores dots, so a dotted Gmail address is another spelling
		// of the dotless mailbox.
		{email: "a.b.c@gmail.com", alias: true},
		{email: "a.b.c@googlemail.com", alias: true},
		{email: "first.last@GMAIL.COM", alias: true},
		// Everywhere else a dot is part of the mailbox name.
		{email: "john.smith@company.com", alias: false},
		{email: "first.last@outlook.com", alias: false},
		{email: "a.b@gmail.company.com", alias: false},
		// Plain addresses.
		{email: "abc@gmail.com", alias: false},
		{email: "ops@company.com", alias: false},
		{email: "not-an-email", alias: false},
	}
	for _, test := range tests {
		t.Run(test.email, func(t *testing.T) {
			assert.Equal(t, test.alias, IsEmailAliasAddress(test.email))
		})
	}
}
