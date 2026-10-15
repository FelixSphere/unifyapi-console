/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api (Copyright (C) 2023-2026
QuantumNous), distributed under the GNU Affero General Public License v3.
See BRANDING.md for the relationship between this fork and its upstream.

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
)

func TestTheBrandComesFromTheDeploymentAndTheCompanyLineFromTheStandard(t *testing.T) {
	prevName, prevAddr := common.SystemName, system_setting.ServerAddress
	common.SystemName = "UnifyAPI"
	system_setting.ServerAddress = "https://app.unifyapi.ai/"
	t.Cleanup(func() { common.SystemName = prevName; system_setting.ServerAddress = prevAddr })

	brand := UnifyAPIEmailBrand()
	assert.Equal(t, "UnifyAPI", brand.ProductName, "the product name is SystemName, which is also the sender display name")
	assert.Equal(t, "https://app.unifyapi.ai", brand.ProductURL)
	assert.Equal(t, "app.unifyapi.ai", brand.ProductDomain)
	assert.Equal(t, "https://app.unifyapi.ai/email", brand.AssetBase, "images are served by this console, on our own domain")
	assert.Equal(t, "UnifyAI · Operated by FelixSphere LLC · 6 Karen Ct, CA 94010, United States", brand.CompanyAddress,
		"the company line is the same in every UnifyAI product")
}

func TestEachNoticeKindGetsItsOwnEyebrow(t *testing.T) {
	assert.Equal(t, "Balance", notificationEyebrow("quota_exceed"))
	assert.Equal(t, "Credit", notificationEyebrow("operator_credit"))
	assert.Equal(t, "Channels", notificationEyebrow("channel_update"))
	assert.Equal(t, "Channels", notificationEyebrow("channel_test"))
	assert.Equal(t, "Notice", notificationEyebrow("anything-else"))
}
