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
package common

import (
	"html/template"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testBrand = EmailBrand{
	ProductName: "Unify API", ProductShort: "API", ProductURL: "https://app.unifyapi.ai", ProductDomain: "app.unifyapi.ai",
	AssetBase: "https://app.unifyapi.ai/email", CompanyAddress: "UnifyAI · Operated by FelixSphere LLC · 6 Karen Ct, CA 94010, United States",
}

func TestAnEmailHasExactlyOneFocalElementAndAmberAppearsOnce(t *testing.T) {
	htmlBody, _, err := RenderEmail(testBrand, EmailMessage{
		Subject: "Reset your UnifyAPI password", Eyebrow: "Reset", Headline: "Choose a new password",
		CTALabel: "Reset password", CTAURL: "https://app.unifyapi.ai/user/reset?token=x",
	})
	require.NoError(t, err)
	assert.Equal(t, 2, strings.Count(htmlBody, "#F5A623"), "amber on the button only: the live cell plus its Outlook fallback")
	assert.Contains(t, htmlBody, `color:#0E0E0E; text-decoration:none`, "the button text is ink, not white")
	assert.Contains(t, htmlBody, `<!--[if mso]><v:roundrect`, "Outlook gets its VML button; html/template must not have eaten the conditional comment")
	assert.Contains(t, htmlBody, `<!--[if !mso]><!-->`)
	assert.NotContains(t, htmlBody, "letter-spacing:8px", "no code block beside a button")

	_, _, err = RenderEmail(testBrand, EmailMessage{Headline: "x", CTAURL: "https://a", Code: "123456"})
	require.ErrorIs(t, err, errEmailTwoFocalElements)
}

func TestACodeEmailShowsTheCodeInInkAndInTheTextPart(t *testing.T) {
	htmlBody, textBody, err := RenderEmail(testBrand, EmailMessage{
		Subject: "Your UnifyAPI verification code", Preheader: "Enter it within 10 minutes.",
		Eyebrow: "Verify", Headline: "Confirm your email address", Intro: "Enter this code.", Code: "482913",
		SafetyNote: "Expires in 10 minutes.", FooterReason: "Entered at sign-up.",
	})
	require.NoError(t, err)
	assert.Contains(t, htmlBody, `background:#0E0E0E; border-radius:6px; padding:20px 24px`)
	assert.Contains(t, htmlBody, ">482913<")
	assert.Zero(t, strings.Count(htmlBody, "#F5A623"), "a code email has no button, so no amber at all")
	assert.Contains(t, textBody, "482913")
	assert.Contains(t, textBody, "VERIFY")
	assert.Contains(t, textBody, "Confirm your email address")
	assert.True(t, strings.HasPrefix(textBody, "Unify API\n"), "the text part opens with the full product name")
	assert.Contains(t, textBody, "Expires in 10 minutes.")
	assert.Contains(t, textBody, "Unify API · app.unifyapi.ai\n")
	assert.NotContains(t, textBody, "FelixSphere", "UI-STANDARD.md: the company is UnifyAI; no FelixSphere tagline anywhere")
	assert.Contains(t, textBody, "UnifyAI · Operated by FelixSphere LLC · 6 Karen Ct, CA 94010, United States")
	assert.NotContains(t, textBody, "<", "the text part carries no markup")
}

func TestTheEmailReadsWithImagesBlockedAndCarriesNoSVG(t *testing.T) {
	htmlBody, _, err := RenderEmail(testBrand, EmailMessage{Headline: "h", Code: "1"})
	require.NoError(t, err)
	assert.Contains(t, htmlBody, `src="https://app.unifyapi.ai/email/unifyai-lockup-black@2x.png"`)
	assert.Contains(t, htmlBody, `alt="UnifyAI"`, "the lockup reads as the brand when images are blocked")
	assert.NotContains(t, strings.ToLower(htmlBody), "<svg")
	assert.NotContains(t, strings.ToLower(htmlBody), ".svg")
	assert.Contains(t, htmlBody, `<meta name="color-scheme" content="light">`)
	assert.Contains(t, htmlBody, "API</td>", "the header descriptor sits beside the lockup; the full name is in the footer")
	assert.Contains(t, htmlBody, "Unify API · <a")
}

func TestValuesAreEscapedButOperatorHTMLIsKept(t *testing.T) {
	htmlBody, textBody, err := RenderEmail(testBrand, EmailMessage{
		Headline:  "Credit <added>",
		IntroHTML: template.HTML(`$5 added.<br/>Balance <b>$12</b>.<br/>Sign in: <a href='https://app.unifyapi.ai'>https://app.unifyapi.ai</a>`),
	})
	require.NoError(t, err)
	assert.Contains(t, htmlBody, "Credit &lt;added&gt;")
	assert.Contains(t, htmlBody, "<br/>Balance <b>$12</b>")
	assert.Equal(t, "$5 added.\nBalance $12.\nSign in: https://app.unifyapi.ai", strings.TrimSpace(strings.SplitN(strings.SplitN(textBody, "Credit <added>\n\n", 2)[1], "\n\n", 2)[0]))
}

func TestAnEmptyCompanyAddressLeavesNoDanglingLine(t *testing.T) {
	brand := testBrand
	brand.CompanyAddress = ""
	htmlBody, textBody, err := RenderEmail(brand, EmailMessage{Headline: "h", FooterReason: "Because."})
	require.NoError(t, err)
	assert.Contains(t, htmlBody, "Because.\n")
	assert.NotContains(t, htmlBody, "Because.<br>")
	assert.True(t, strings.HasSuffix(strings.TrimSpace(textBody), "Because."))
}

func TestMultipartCarriesTextFirstThenHTML(t *testing.T) {
	contentType, body, err := BuildMultipartEmail("plain words", "<p>rich words</p>")
	require.NoError(t, err)
	assert.Regexp(t, regexp.MustCompile(`^multipart/alternative; boundary=\S+$`), contentType)
	s := string(body)
	textAt := strings.Index(s, "Content-Type: text/plain; charset=UTF-8")
	htmlAt := strings.Index(s, "Content-Type: text/html; charset=UTF-8")
	require.True(t, textAt >= 0 && htmlAt >= 0)
	assert.Less(t, textAt, htmlAt, "clients that take the last part they understand must get the HTML")
	assert.Contains(t, s, "plain words")
	assert.Contains(t, s, "<p>rich words</p>")
}

func TestHTMLToTextFlattensTheNotificationVocabulary(t *testing.T) {
	assert.Equal(t, "a\nb", htmlToText("a<br>b"))
	assert.Equal(t, "see https://x.y", htmlToText(`see <a href="https://x.y">https://x.y</a>`))
	assert.Equal(t, "see here (https://x.y)", htmlToText(`see <a href='https://x.y'>here</a>`))
	assert.Equal(t, "5 & 6", htmlToText("<b>5</b> &amp; 6"))
}
