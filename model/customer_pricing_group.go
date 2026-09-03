/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"crypto/sha256"
	"encoding/hex"
)

const customerPricingGroupPrefix = "pricing-group:"

// CustomerPricingGroupKey namespaces Pricing Group settlement identities away
// from legacy user ids and old bare group names. Short names stay readable;
// long names are hashed so the key always fits Settlement.Counterparty.
func CustomerPricingGroupKey(group string) string {
	if group == "" {
		return ""
	}
	key := customerPricingGroupPrefix + group
	if len(key) <= 64 {
		return key
	}
	sum := sha256.Sum256([]byte(group))
	return customerPricingGroupPrefix + "sha256:" + hex.EncodeToString(sum[:20])
}

func IsCustomerPricingGroupKey(key string) bool {
	return len(key) > len(customerPricingGroupPrefix) && key[:len(customerPricingGroupPrefix)] == customerPricingGroupPrefix
}

// ResolveCurrentCustomerPricingGroups reads the present billing owner for each
// login account. Missing/deleted users are intentionally absent from the map;
// callers preserve their immutable request-time group as a fallback.
func ResolveCurrentCustomerPricingGroups(userIDs []int) (map[int]string, error) {
	unique := make(map[int]bool, len(userIDs))
	ids := make([]int, 0, len(userIDs))
	for _, id := range userIDs {
		if id != 0 && !unique[id] {
			unique[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return map[int]string{}, nil
	}
	// Some installations keep request logs in a separate database, and model
	// unit tests intentionally exercise that database without a users table.
	// In either case the immutable log group is the safe billing fallback.
	if DB == nil || !DB.Migrator().HasTable(&User{}) {
		return map[int]string{}, nil
	}

	var users []User
	if err := DB.Select([]string{"id", "group"}).Where("id IN ?", ids).Find(&users).Error; err != nil {
		return nil, err
	}
	out := make(map[int]string, len(users))
	for _, user := range users {
		out[user.Id] = user.Group
	}
	return out, nil
}
