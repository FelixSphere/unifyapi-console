/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"strings"
	"sync"
	"time"
)

// A pricing group that belongs to a provisioned customer is that customer's
// identity: its NAME is the customer's name, and its ratio is their commercial
// terms. Neither may be shown to anybody else, and nobody else may bill under
// it.
//
// This exists because the list that decides both -- UserUsableGroups -- is an
// operator-editable option, so keeping customer groups out of it at
// provisioning time is necessary but not sufficient: one hand-edit in the admin
// UI puts them back, and hand-editing options has already caused one incident
// here. The check therefore reads the customer registry, which is the only
// authoritative record of which group belongs to whom.
//
// Cached because it is consulted on the request path (the playground group
// check in middleware/distributor.go), with the same one-minute TTL the pricing
// cache uses.

const customerOwnedGroupsTTL = time.Minute

var customerOwnedGroups = struct {
	sync.RWMutex
	set     map[string]struct{}
	fetched time.Time
}{}

// InvalidateCustomerOwnedGroupsCache forces the next read to hit the database.
// Provisioning calls it so a new customer's group is protected immediately
// rather than up to a TTL later.
func InvalidateCustomerOwnedGroupsCache() {
	customerOwnedGroups.Lock()
	customerOwnedGroups.set = nil
	customerOwnedGroups.fetched = time.Time{}
	customerOwnedGroups.Unlock()
}

// CustomerOwnedGroups is the set of pricing groups that identify a customer,
// and whether that set is KNOWN.
//
// The second return is the whole point. This guards a disclosure, so the only
// safe answer to "I cannot read the registry" is to treat every group as some
// customer's and show none of it. Returning the last known set and a bare
// map would read as "no customers exist" on the first call of a fresh process
// -- which is the first /api/pricing after every release, on an
// unauthenticated route. A stale private list is safe; an empty one is the
// incident this file exists to prevent.
func CustomerOwnedGroups() (map[string]struct{}, bool) {
	customerOwnedGroups.RLock()
	fresh := customerOwnedGroups.set != nil && time.Since(customerOwnedGroups.fetched) < customerOwnedGroupsTTL
	cached := customerOwnedGroups.set
	customerOwnedGroups.RUnlock()
	if fresh {
		return cached, true
	}

	if DB == nil || !DB.Migrator().HasTable(&PartnershipCustomer{}) {
		// No registry TABLE is not the same as a registry we failed to read.
		// An install that never provisioned a customer has none, and so does
		// a unit test on an ad-hoc database; both genuinely know the answer is
		// "nobody". Reporting those as unknown would hide every ordinary tier
		// from every user, which is how the first version of this hardening
		// broke three auto-group tests.
		return map[string]struct{}{}, true
	}
	// `group` is a reserved word; every other query in this package reaches
	// the column through commonGroupCol rather than trusting the driver to
	// quote it, and the tests here run on SQLite while production is Postgres,
	// so this is not a difference worth discovering in production.
	groupCol := commonGroupCol
	if groupCol == "" {
		groupCol = "`group`"
	}
	var groups []string
	if err := DB.Model(&PartnershipCustomer{}).
		Distinct(groupCol).Pluck(groupCol, &groups).Error; err != nil {
		return cached, cached != nil
	}

	set := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		group = strings.TrimSpace(group)
		if group == "" {
			continue
		}
		set[group] = struct{}{}
	}
	customerOwnedGroups.Lock()
	customerOwnedGroups.set = set
	customerOwnedGroups.fetched = time.Now()
	customerOwnedGroups.Unlock()
	return set, true
}

// IsCustomerOwnedGroup reports whether group identifies a customer.
//
// When the registry cannot be read it answers TRUE for every non-empty group.
// Callers use this to decide what to reveal, so an unreadable registry must
// hide everything rather than reveal everything. The cost of being wrong that
// way is a user briefly not seeing an ordinary tier they could have picked;
// the cost of being wrong the other way is publishing the customer list.
func IsCustomerOwnedGroup(group string) bool {
	if group == "" {
		return false
	}
	set, known := CustomerOwnedGroups()
	if !known {
		return true
	}
	_, owned := set[group]
	return owned
}
