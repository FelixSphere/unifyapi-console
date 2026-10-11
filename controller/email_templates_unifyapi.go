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

// unifyapiVerificationEmail is the sign-up code. One focal element: the code.
func unifyapiVerificationEmail(code string) common.EmailMessage {
	return common.EmailMessage{
		Subject:   fmt.Sprintf("Your %s verification code", common.SystemName),
		Preheader: fmt.Sprintf("Enter it within %d minutes to finish creating your account.", common.VerificationValidMinutes),
		Eyebrow:   "Verify",
		Headline:  "Confirm your email address",
		Intro:     fmt.Sprintf("Enter this code in %s to finish creating your account.", common.SystemName),
		Code:      code,
		SafetyNote: fmt.Sprintf("This code expires in %d minutes. If you didn't request it, you can ignore this email: nothing changes without the code.",
			common.VerificationValidMinutes),
		FooterReason: "You're receiving this because this address was entered at sign-up.",
	}
}

// unifyapiPasswordResetEmail carries the reset link. One focal element: the button.
func unifyapiPasswordResetEmail(link string) common.EmailMessage {
	return common.EmailMessage{
		Subject:   fmt.Sprintf("Reset your %s password", common.SystemName),
		Preheader: fmt.Sprintf("The link works for %d minutes.", common.VerificationValidMinutes),
		Eyebrow:   "Reset",
		Headline:  "Choose a new password",
		Intro:     fmt.Sprintf("Someone asked to reset the password for the %s account at this address. If that was you, pick a new password below.", common.SystemName),
		CTALabel:  "Reset password",
		CTAURL:    link,
		SafetyNote: fmt.Sprintf("The link expires in %d minutes and can be used once. If you didn't ask for this, ignore this email: your password stays as it is.",
			common.VerificationValidMinutes),
		FooterReason: "You're receiving this because a password reset was requested for this address.",
	}
}
