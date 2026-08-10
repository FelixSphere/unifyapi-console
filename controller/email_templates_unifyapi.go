// UNIFYAPI-BRAND: ours. English copy for the transactional emails.
//
// Upstream hardcodes the subject and body of every account email as Chinese
// string literals in controller/misc.go and service/quota.go -- they do not go
// through i18n/, so no option, Accept-Language header or user setting can change
// them. UnifyAPI sells to an English-speaking market, so they are replaced.
//
// They live in this file rather than inline in misc.go to keep the delta to an
// upstream file down to one call per site. Upstream ships ~4 commits a day and
// misc.go is actively edited; a multi-line literal replaced in place is a merge
// conflict every release, a single function call usually is not.
package controller

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
)

func unifyapiVerificationEmail(code string) (subject, content string) {
	subject = fmt.Sprintf("%s email verification", common.SystemName)
	content = fmt.Sprintf("<p>Hello,</p>"+
		"<p>You are verifying your email address for %s.</p>"+
		"<p>Your verification code is: <strong>%s</strong></p>"+
		"<p>The code is valid for %d minutes. If you did not request this, "+
		"you can safely ignore this email.</p>",
		common.SystemName, code, common.VerificationValidMinutes)
	return subject, content
}

func unifyapiPasswordResetEmail(link string) (subject, content string) {
	subject = fmt.Sprintf("%s password reset", common.SystemName)
	content = fmt.Sprintf("<p>Hello,</p>"+
		"<p>You are resetting the password for your %s account.</p>"+
		"<p>Click <a href='%s'>here</a> to choose a new password.</p>"+
		"<p>If the link does not work, copy this address into your browser:<br>%s</p>"+
		"<p>The link is valid for %d minutes. If you did not request this, "+
		"you can safely ignore this email.</p>",
		common.SystemName, link, link, common.VerificationValidMinutes)
	return subject, content
}
