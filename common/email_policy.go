/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package common

import "strings"

// UNIFYAPI-FORK: signup abuse controls. Added after 2026-09-28/29, when ~168
// bot accounts farmed the new-user credit through disposable mailboxes. Email
// verification alone did not stop them: a throwaway inbox receives the code.

// EmailDomainBlocklistEnabled rejects signup and email binding for addresses
// on EmailDomainBlocklist. Unlike EmailDomainRestrictionEnabled (an allowlist)
// it is on by default: it only refuses known throwaway providers.
var EmailDomainBlocklistEnabled = true

// EmailDomainBlocklist holds normalized domains. An entry also blocks its
// subdomains, so "auroracovia.com" covers "x7.auroracovia.com".
var EmailDomainBlocklist = ParseEmailDomainList(strings.Join(defaultDisposableEmailDomains, "\n"))

// RegisterIPDailyLimit caps successful account creations per client IP per
// 24h window, across password and OAuth signup. 0 disables the cap.
var RegisterIPDailyLimit = 3

// ParseEmailDomainList accepts one domain per line (commas also separate) and
// normalizes each: lower case, surrounding whitespace removed, and a leading
// "@", "*." or "." dropped, so pasted variants like "*.example.com" still
// match. Blank lines and duplicates are skipped.
func ParseEmailDomainList(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ','
	})
	seen := make(map[string]struct{}, len(fields))
	domains := make([]string, 0, len(fields))
	for _, field := range fields {
		domain := strings.ToLower(strings.TrimSpace(field))
		domain = strings.TrimPrefix(domain, "@")
		domain = strings.TrimPrefix(domain, "*")
		domain = strings.Trim(domain, ".")
		if domain == "" {
			continue
		}
		if _, ok := seen[domain]; ok {
			continue
		}
		seen[domain] = struct{}{}
		domains = append(domains, domain)
	}
	return domains
}

// IsEmailDomainBlocked reports whether the address's domain, or any parent
// domain of it, is on the blocklist. It ignores EmailDomainBlocklistEnabled;
// callers decide whether the policy applies.
func IsEmailDomainBlocked(email string, blocklist []string) bool {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return false
	}
	domain := strings.Trim(strings.ToLower(strings.TrimSpace(email[at+1:])), ".")
	if domain == "" {
		return false
	}
	for _, blocked := range blocklist {
		if domain == blocked || strings.HasSuffix(domain, "."+blocked) {
			return true
		}
	}
	return false
}

// defaultDisposableEmailDomains seeds EmailDomainBlocklist. It is a starting
// point, not a complete list: operators extend it from the admin settings.
// Keep it to providers whose only purpose is throwaway mail, so a legitimate
// customer is never refused by the default.
var defaultDisposableEmailDomains = []string{
	// Observed in the 2026-09-28/29 signup-farming incident.
	"maildrop.cc",
	"mailto.plus",
	"uberip.com",
	"yzcalo.com",
	"auroracovia.com",
	"gmeenramy.com",
	// tempmail.plus / mailto.plus family.
	"fexpost.com",
	"fexbox.org",
	"mailbox.in.ua",
	"rover.info",
	"chitthi.in",
	"fextemp.com",
	"any.pink",
	"merepost.com",
	// Mailinator.
	"mailinator.com",
	"mailinator.net",
	"mailinator2.com",
	// Guerrilla Mail.
	"guerrillamail.com",
	"guerrillamail.net",
	"guerrillamail.org",
	"guerrillamail.biz",
	"guerrillamail.de",
	"guerrillamailblock.com",
	"sharklasers.com",
	"grr.la",
	"pokemail.net",
	"spam4.me",
	// YOPmail.
	"yopmail.com",
	"yopmail.fr",
	"yopmail.net",
	// 10 Minute Mail, Temp-Mail and their rotating backends.
	"10minutemail.com",
	"10minutemail.net",
	"temp-mail.org",
	"temp-mail.io",
	"tempmail.com",
	"tempmail.net",
	"tempmailo.com",
	"tempmail.dev",
	"tmpmail.org",
	"tmpmail.net",
	"tempr.email",
	"tempinbox.com",
	"emlpro.com",
	"emltmp.com",
	"emlhub.com",
	"1secmail.com",
	"1secmail.org",
	"1secmail.net",
	"esiix.com",
	"wwjmp.com",
	// Fake Mail Generator.
	"armyspy.com",
	"cuvox.de",
	"dayrep.com",
	"einrot.com",
	"fleckens.hu",
	"gustr.com",
	"jourrapide.com",
	"rhyta.com",
	"superrito.com",
	"teleworm.us",
	// Other single-purpose throwaway providers.
	"getnada.com",
	"nada.email",
	"dispostable.com",
	"discard.email",
	"trashmail.com",
	"trashmail.net",
	"trashmail.de",
	"throwawaymail.com",
	"mailnesia.com",
	"mintemail.com",
	"mohmal.com",
	"emailondeck.com",
	"fakeinbox.com",
	"mailcatch.com",
	"mailnull.com",
	"getairmail.com",
	"moakt.com",
	"mailpoof.com",
	"inboxkitten.com",
	"harakirimail.com",
	"spambox.us",
	"mailsac.com",
	"byom.de",
	"trbvm.com",
	"emailfake.com",
	"mytemp.email",
	"burnermail.io",
}
