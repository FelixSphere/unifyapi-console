/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package controller

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/gin-gonic/gin"
)

// accountEmailRejection returns the message that refuses email as the address
// of an account -- one being registered or one being bound -- or "" when the
// configured email policies allow it. UNIFYAPI-FORK: this used to live inline
// in SendEmailVerification only, so Register and EmailBind trusted whatever
// address had once received a code, even after the policy changed.
func accountEmailRejection(c *gin.Context, email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return "Invalid email address" // UNIFYAPI-BRAND: English copy
	}
	domainPart := email[at+1:]
	if common.EmailDomainRestrictionEnabled {
		allowed := false
		for _, domain := range common.EmailDomainWhitelist {
			if domainPart == domain {
				allowed = true
				break
			}
		}
		if !allowed {
			return "The administrator has enabled the email domain name whitelist, and your email address is not allowed due to special symbols or it's not in the whitelist."
		}
	}
	if common.EmailAliasRestrictionEnabled && common.IsEmailAliasAddress(email) {
		return "The administrator has enabled email alias restrictions; your address was rejected because it contains special symbols." // UNIFYAPI-BRAND: English copy
	}
	if common.EmailDomainBlocklistEnabled && common.IsEmailDomainBlocked(email, common.EmailDomainBlocklist) {
		return i18n.T(c, i18n.MsgUserEmailDomainBlocked)
	}
	return ""
}
