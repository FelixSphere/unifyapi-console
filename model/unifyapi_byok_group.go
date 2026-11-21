/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import "strings"

// UNIFYAPI-FORK: bring-your-own-key (BYOK) groups.
//
// A BYOK customer gives us their own provider API key. We put it on a channel
// and they call that channel through our API; the provider bills them for the
// tokens and we bill a platform fee (the group's ratio).
//
// Two things must then hold, and under the "every pricing group routes through
// every channel" rule in unifyapi_pricing_group_channel_access.go neither does:
//
//  1. Nobody else's traffic may reach the customer's key. With the union, every
//     customer group gains the BYOK channel the moment it is saved, so other
//     customers' requests spend this customer's money and their prompts land in
//     this customer's provider account.
//  2. The BYOK customer's traffic may reach only their own key. With the union,
//     their group gains every channel we pay for, and a request priced at a
//     platform fee of a few percent is served from supply that costs us 0.68 to
//     1.055 of list.
//
// Both are closed in routingGroups. A third follows from the second and is
// closed in service.GetUserUsableGroups: a BYOK login's usable groups are its
// own group (plus an explicit operator grant), so a token set to `auto` or to
// one of our groups cannot send that customer's requests through our
// suppliers either. Those requests would be billed at the other group's price,
// so that one is not about money; it is that a customer who brings a key is
// told their traffic goes to that key.
//
// The group is marked by its NAME rather than by an option row, on purpose.
// Every setting that lives only in the `options` table has been silently lost
// or half-saved here at least once, and the failure of this one would be a
// customer's key serving everyone. A name cannot be dropped by a careless
// save: it is the same string in channels.group, users.group and Group
// Pricing, and a group that is not named this way simply is not BYOK.
//
// The prefix is matched without regard to case so that "BYOK-acme" typed in
// the admin UI is isolated too. A misspelt prefix is still an ordinary group;
// the onboarding runbook checks the channel's ability rows for that reason.
const BYOKGroupPrefix = "byok-"

// IsBYOKGroup reports whether group is a bring-your-own-key group.
func IsBYOKGroup(group string) bool {
	group = strings.TrimSpace(group)
	return len(group) > len(BYOKGroupPrefix) &&
		strings.EqualFold(group[:len(BYOKGroupPrefix)], BYOKGroupPrefix)
}

// byokGroupsOf returns the BYOK groups in a channel's own group list, in the
// operator's order and without duplicates.
func byokGroupsOf(groups []string) []string {
	var byok []string
	seen := make(map[string]struct{})
	for _, group := range groups {
		if !IsBYOKGroup(group) {
			continue
		}
		if _, dup := seen[group]; dup {
			continue
		}
		seen[group] = struct{}{}
		byok = append(byok, group)
	}
	return byok
}
