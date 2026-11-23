/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package system_setting

import "github.com/QuantumNous/new-api/setting/config"

// UNIFYAPI-FORK: settings for the email an admin writes to customers from the
// Users page (service/unifyapi_admin_email.go). Stored in the options table as
// admin_email.<field>, like every other registered config.
type AdminEmailSettings struct {
	// ReplyTo is the bare address customers' replies go to. Empty means the
	// message carries no Reply-To header and a reply goes to SMTPFrom, which
	// in production is noreply@ -- so templates that ask the customer to
	// reply need this set.
	ReplyTo string `json:"reply_to"`
}

var defaultAdminEmailSettings = AdminEmailSettings{}

func init() {
	config.GlobalConfig.Register("admin_email", &defaultAdminEmailSettings)
}

func GetAdminEmailSettings() *AdminEmailSettings {
	return &defaultAdminEmailSettings
}
