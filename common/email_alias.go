/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package common

import "strings"

// IsEmailAliasAddress reports whether an address is a sub-address of another
// mailbox, as EmailAliasRestrictionEnabled defines it: a "+" tag on any
// domain, or a "." in a Gmail local part, which Gmail ignores for delivery
// (a.b.c@gmail.com reaches abc@gmail.com).
//
// UNIFYAPI-FORK: upstream rejected a "." on every domain, which refused
// ordinary corporate mailboxes such as john.smith@company.com. Outside Gmail a
// dot is part of the mailbox name, not an alias.
func IsEmailAliasAddress(email string) bool {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return false
	}
	localPart := email[:at]
	if strings.Contains(localPart, "+") {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(email[at+1:])) {
	case "gmail.com", "googlemail.com":
		return strings.Contains(localPart, ".")
	}
	return false
}
