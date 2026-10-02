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
// company address is an option so it can be set without a release; the
// footer omits it while empty.
const EmailFooterAddressOption = "EmailFooterAddress"

func UnifyAPIEmailBrand() common.EmailBrand {
	base := strings.TrimRight(system_setting.ServerAddress, "/")
	domain := base
	if u, err := url.Parse(base); err == nil && u.Host != "" {
		domain = u.Host
	}
	common.OptionMapRWMutex.RLock()
	address := common.OptionMap[EmailFooterAddressOption]
	common.OptionMapRWMutex.RUnlock()
	return common.EmailBrand{
		ProductName:    common.SystemName,
		ProductURL:     base,
		ProductDomain:  domain,
		AssetBase:      base + "/email",
		CompanyAddress: strings.TrimSpace(address),
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
