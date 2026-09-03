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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCustomerPricingGroupKeyIsNamespacedAndBounded(t *testing.T) {
	require.Equal(t, "pricing-group:Chinhin", CustomerPricingGroupKey("Chinhin"))
	require.True(t, IsCustomerPricingGroupKey(CustomerPricingGroupKey("Chinhin")))

	longName := strings.Repeat("客户组", 40)
	key := CustomerPricingGroupKey(longName)
	require.LessOrEqual(t, len(key), 64)
	require.Equal(t, key, CustomerPricingGroupKey(longName))
	require.NotEqual(t, key, CustomerPricingGroupKey(longName+"x"))
}
