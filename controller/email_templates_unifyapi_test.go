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
package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The two account emails in the shared design: the subject names the product
// and the action, the preheader adds something the subject does not say, and
// each carries exactly one focal element.
func TestVerificationEmailIsACodeEmail(t *testing.T) {
	previous := common.SystemName
	common.SystemName = "UnifyAPI"
	t.Cleanup(func() { common.SystemName = previous })

	msg := unifyapiVerificationEmail("482913")
	assert.Equal(t, "Your UnifyAPI verification code", msg.Subject)
	assert.NotEqual(t, msg.Subject, msg.Preheader)
	assert.Equal(t, "482913", msg.Code)
	assert.Empty(t, msg.CTAURL)
	assert.Contains(t, msg.SafetyNote, "10 minutes")
	htmlBody, textBody, err := common.RenderEmail(common.EmailBrand{ProductName: "UnifyAPI"}, msg)
	require.NoError(t, err)
	assert.Contains(t, htmlBody, ">482913<")
	assert.Contains(t, textBody, "482913")
}

func TestPasswordResetEmailIsAButtonEmail(t *testing.T) {
	previous := common.SystemName
	common.SystemName = "UnifyAPI"
	t.Cleanup(func() { common.SystemName = previous })

	link := "https://app.unifyapi.ai/user/reset?email=a%40b.c&token=t"
	msg := unifyapiPasswordResetEmail(link)
	assert.Equal(t, "Reset your UnifyAPI password", msg.Subject)
	assert.Equal(t, link, msg.CTAURL)
	assert.Equal(t, "Reset password", msg.CTALabel)
	assert.Empty(t, msg.Code)
	htmlBody, textBody, err := common.RenderEmail(common.EmailBrand{ProductName: "UnifyAPI"}, msg)
	require.NoError(t, err)
	assert.Contains(t, htmlBody, `href="https://app.unifyapi.ai/user/reset?email=a%40b.c&amp;token=t"`, "attribute-escaped, as HTML requires")
	assert.Contains(t, textBody, "Reset password: "+link)
}
