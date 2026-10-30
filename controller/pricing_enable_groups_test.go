/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
 * enable_groups is published verbatim on /api/pricing, which needs no
 * authentication. filterPricingByUsableGroups decides which ROWS to return but
 * left the list on each row untouched, so every model a customer could use
 * named that customer's group to anyone who asked -- which is how the customer
 * list was readable from the open internet on 2026-09-22.
 */
package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
)

func TestEnableGroupsNeverNamesAGroupTheCallerCannotUse(t *testing.T) {
	pricing := []model.Pricing{
		{ModelName: "gpt-4o-mini", EnableGroup: []string{"default", "Kingdee", "Chinhin"}},
	}

	// An anonymous visitor: the marketing site reads
	// default_group_model_ratio and needs nothing else.
	anonymous := hideGroupsTheCallerMayNotSee(pricing, map[string]string{"default": ""})
	assert.Equal(t, []string{"default"}, anonymous[0].EnableGroup,
		"a customer's name must not be published to an anonymous caller")

	// A customer's own member still sees their own group.
	member := hideGroupsTheCallerMayNotSee(pricing, map[string]string{"default": "", "Kingdee": ""})
	assert.Equal(t, []string{"default", "Kingdee"}, member[0].EnableGroup)
	assert.NotContains(t, member[0].EnableGroup, "Chinhin",
		"one customer must not see another")
}

func TestEnableGroupsLeavesAllAlone(t *testing.T) {
	// "all" names no customer, and rewriting it would change which models the
	// page shows rather than who is named.
	pricing := []model.Pricing{{ModelName: "gpt-4o-mini", EnableGroup: []string{"all"}}}
	out := hideGroupsTheCallerMayNotSee(pricing, map[string]string{"default": ""})
	assert.Equal(t, []string{"all"}, out[0].EnableGroup)
}

func TestNarrowingEnableGroupsDoesNotMutateTheCachedPricing(t *testing.T) {
	// model.GetPricing returns a shared, cached slice. Rewriting it in place
	// would leak one caller's view into the next request's response.
	original := []string{"default", "Kingdee"}
	pricing := []model.Pricing{{ModelName: "gpt-4o-mini", EnableGroup: original}}

	hideGroupsTheCallerMayNotSee(pricing, map[string]string{"default": ""})

	assert.Equal(t, []string{"default", "Kingdee"}, pricing[0].EnableGroup,
		"the cached pricing row must be untouched")
}
