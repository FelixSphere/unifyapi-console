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

// CustomerOwnedGroups is the set of pricing groups that identify a customer.
//
// A read failure returns what is known rather than an error: this guards a
// disclosure, and the callers are list-building code with nowhere to report to.
// The conservative direction on failure is the LAST known set, never an empty
// one, so a database blip cannot briefly publish every customer name.
func CustomerOwnedGroups() map[string]struct{} {
	customerOwnedGroups.RLock()
	fresh := customerOwnedGroups.set != nil && time.Since(customerOwnedGroups.fetched) < customerOwnedGroupsTTL
	cached := customerOwnedGroups.set
	customerOwnedGroups.RUnlock()
	if fresh {
		return cached
	}

	if DB == nil || !DB.Migrator().HasTable(&PartnershipCustomer{}) {
		return cached
	}
	var groups []string
	if err := DB.Model(&PartnershipCustomer{}).
		Distinct().Pluck("group", &groups).Error; err != nil {
		return cached
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
	return set
}

// IsCustomerOwnedGroup reports whether group identifies a customer other than
// the caller's own.
func IsCustomerOwnedGroup(group string) bool {
	if group == "" {
		return false
	}
	_, owned := CustomerOwnedGroups()[group]
	return owned
}
