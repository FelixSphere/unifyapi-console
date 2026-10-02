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
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// UNIFYAPI-BRAND: the deployment half of every branded email. The product name
// is SystemName (also the sender display name, which common/email.go builds
// as "SystemName <SMTPFrom>"); images are served by this console from
// web/public/email, so they live on our own domain as the kit requires. The
// company line is a company fact (UI-STANDARD.md "Company facts"), the same
// in all five products, so it is a constant rather than a setting.
const UnifyAICompanyLine = "UnifyAI · Lebuh Bandar Utama PJU 6, 47800 Petaling Jaya, Selangor, Malaysia"

func UnifyAPIEmailBrand() common.EmailBrand {
	base := strings.TrimRight(system_setting.ServerAddress, "/")
	domain := base
	if u, err := url.Parse(base); err == nil && u.Host != "" {
		domain = u.Host
	}
	return common.EmailBrand{
		ProductName:    common.SystemName,
		ProductShort:   productShortName(common.SystemName),
		ProductURL:     base,
		ProductDomain:  domain,
		AssetBase:      base + "/email",
		CompanyAddress: UnifyAICompanyLine,
	}
}

// SendBrandedEmail sends one message in the shared UnifyAI design.
func SendBrandedEmail(purpose string, receiver string, msg common.EmailMessage) error {
	return common.SendBrandedEmail(purpose, receiver, UnifyAPIEmailBrand(), msg)
}

// notificationEyebrow names the kind of notice in the email's eyebrow.
func notificationEyebrow(notifyType string) string {
	switch notifyType {
	case "quota_exceed":
		return "Balance"
	case "operator_credit":
		return "Credit"
	case "channel_update", "channel_test":
		return "Channels"
	default:
		return "Notice"
	}
}

// productShortName is the header descriptor UI-STANDARD.md puts after the
// lockup and the divider: the product name without the company prefix, so
// "UnifyAI API" reads as "API". A name without the prefix is used whole.
func productShortName(name string) string {
	short := strings.TrimSpace(strings.TrimPrefix(name, "UnifyAI "))
	if short == "" {
		return name
	}
	return short
}
