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

// UNIFYAPI-BRAND: one email design for every UnifyAI product. This is the
// UnifyAPI port of ~/unifyai-brand-kit/email/base.html (kept as a copy on
// purpose: the kit says never import it across repos). Table layout, inline
// styles, a 600px card; renders in Gmail, Outlook (VML button) and Apple Mail.
//
// The rules the template enforces rather than documents:
//   - one focal element: an amber CTA with INK text, or a code block -- never
//     both, and amber appears once per email;
//   - every email also goes out as plain text (multipart/alternative);
//   - no SVG, no images-only content: the lockup's alt text is "UnifyAI";
//   - the preheader adds information and never repeats the subject.

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"mime/multipart"
	"net/textproto"
	"regexp"
	"strings"
)

// EmailBrand is the per-deployment half of an email: who sends it and where
// its images live. See service.UnifyAPIEmailBrand for how it is filled.
type EmailBrand struct {
	ProductName    string // "UnifyAI API": the full product name (titles, footer, sender)
	ProductShort   string // "API": the header descriptor after the lockup and divider
	ProductURL     string // https://app.unifyapi.ai
	ProductDomain  string // app.unifyapi.ai
	AssetBase      string // https://app.unifyapi.ai/email -- our own domain, PNG only
	CompanyAddress string // footer; omitted while empty
}

// EmailDetail is one key/value row under the focal element.
type EmailDetail struct {
	Label string
	Value string
}

// EmailMessage is the per-message half. Exactly one of CTAURL and Code may be
// set; Intro is plain text, IntroHTML is operator-authored HTML that is
// trusted as written (notification bodies carry <br/> and links).
type EmailMessage struct {
	Subject      string
	Preheader    string
	Eyebrow      string // VERIFY, RESET, BALANCE ... rendered uppercase mono
	Headline     string
	Intro        string
	IntroHTML    template.HTML
	CTALabel     string
	CTAURL       string
	Code         string
	Details      []EmailDetail
	SafetyNote   string
	FooterReason string
}

// html/template drops HTML comments, and Outlook's conditional comments are
// the only way it renders a rounded button or skips a web-font link. They are
// built here as trusted HTML with the two user values escaped.
var (
	noMsoOpen = template.HTML("<!--[if !mso]><!-->")
	noMsoEnd  = template.HTML("<!--<![endif]-->")
)

func outlookButton(url, label string) template.HTML {
	if url == "" {
		return ""
	}
	return template.HTML(`<!--[if mso]><v:roundrect xmlns:v="urn:schemas-microsoft-com:vml" href="` + template.HTMLEscapeString(url) +
		`" style="height:48px;v-text-anchor:middle;width:240px;" arcsize="12%" stroke="f" fillcolor="#F5A623"><center style="color:#0E0E0E;font-family:Arial,sans-serif;font-size:16px;font-weight:bold;">` +
		template.HTMLEscapeString(label) + `</center></v:roundrect><![endif]-->`)
}

var errEmailTwoFocalElements = fmt.Errorf("an email has one focal element: a button or a code, not both")

// RenderEmail produces the HTML and the plain-text part of one message.
func RenderEmail(brand EmailBrand, msg EmailMessage) (htmlBody string, textBody string, err error) {
	if msg.CTAURL != "" && msg.Code != "" {
		return "", "", errEmailTwoFocalElements
	}
	var out bytes.Buffer
	if err := unifyaiEmailTemplate.Execute(&out, struct {
		Brand     EmailBrand
		Msg       EmailMessage
		NoMsoOpen template.HTML
		NoMsoEnd  template.HTML
		MsoButton template.HTML
	}{brand, msg, noMsoOpen, noMsoEnd, outlookButton(msg.CTAURL, msg.CTALabel)}); err != nil {
		return "", "", err
	}
	return out.String(), renderEmailText(brand, msg), nil
}

// renderEmailText is the same content with no markup, for clients that show
// the text part and for the people who read mail that way.
func renderEmailText(brand EmailBrand, msg EmailMessage) string {
	var b strings.Builder
	b.WriteString(brand.ProductName + "\n\n")
	if msg.Eyebrow != "" {
		b.WriteString(strings.ToUpper(msg.Eyebrow) + "\n")
	}
	b.WriteString(msg.Headline + "\n\n")
	if msg.IntroHTML != "" {
		b.WriteString(htmlToText(string(msg.IntroHTML)) + "\n\n")
	} else if msg.Intro != "" {
		b.WriteString(msg.Intro + "\n\n")
	}
	if msg.Code != "" {
		b.WriteString(msg.Code + "\n\n")
	}
	if msg.CTAURL != "" {
		label := msg.CTALabel
		if label == "" {
			label = "Open"
		}
		b.WriteString(label + ": " + msg.CTAURL + "\n\n")
	}
	for _, d := range msg.Details {
		b.WriteString(d.Label + ": " + d.Value + "\n")
	}
	if len(msg.Details) > 0 {
		b.WriteString("\n")
	}
	if msg.SafetyNote != "" {
		b.WriteString(msg.SafetyNote + "\n\n")
	}
	b.WriteString("-- \n" + brand.ProductName + " · " + brand.ProductDomain + "\n")
	if msg.FooterReason != "" {
		b.WriteString(msg.FooterReason + "\n")
	}
	if brand.CompanyAddress != "" {
		b.WriteString(brand.CompanyAddress + "\n")
	}
	return b.String()
}

var (
	emailBreakTag  = regexp.MustCompile(`(?i)<br\s*/?>`)
	emailAnchorTag = regexp.MustCompile(`(?is)<a\s[^>]*href=['"]([^'"]+)['"][^>]*>(.*?)</a>`)
	emailAnyTag    = regexp.MustCompile(`<[^>]+>`)
)

// htmlToText flattens the small HTML vocabulary notification bodies use:
// line breaks, bold, and links whose text is the URL itself.
func htmlToText(s string) string {
	s = emailBreakTag.ReplaceAllString(s, "\n")
	s = emailAnchorTag.ReplaceAllStringFunc(s, func(a string) string {
		m := emailAnchorTag.FindStringSubmatch(a)
		if len(m) < 3 {
			return a
		}
		text := strings.TrimSpace(emailAnyTag.ReplaceAllString(m[2], ""))
		if text == "" || text == m[1] {
			return m[1]
		}
		return text + " (" + m[1] + ")"
	})
	s = emailAnyTag.ReplaceAllString(s, "")
	return strings.TrimSpace(html.UnescapeString(s))
}

// BuildMultipartEmail assembles a multipart/alternative body: text first, HTML
// last, so a client that takes the last part it understands shows the HTML.
// Returns the Content-Type header value and the body.
func BuildMultipartEmail(textBody, htmlBody string) (contentType string, body []byte, err error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, part := range []struct{ ctype, content string }{
		{"text/plain; charset=UTF-8", textBody},
		{"text/html; charset=UTF-8", htmlBody},
	} {
		h := textproto.MIMEHeader{}
		h.Set("Content-Type", part.ctype)
		h.Set("Content-Transfer-Encoding", "8bit")
		pw, err := w.CreatePart(h)
		if err != nil {
			return "", nil, err
		}
		if _, err := pw.Write([]byte(part.content)); err != nil {
			return "", nil, err
		}
	}
	if err := w.Close(); err != nil {
		return "", nil, err
	}
	return "multipart/alternative; boundary=" + w.Boundary(), buf.Bytes(), nil
}

// SendBrandedEmail renders msg in the shared design and sends it as
// multipart/alternative, recording the attempt under purpose like
// SendEmailForPurpose does.
func SendBrandedEmail(purpose string, receiver string, brand EmailBrand, msg EmailMessage) error {
	htmlBody, textBody, err := RenderEmail(brand, msg)
	if err != nil {
		return err
	}
	contentType, body, err := BuildMultipartEmail(textBody, htmlBody)
	if err != nil {
		return err
	}
	err = sendEmailWithContentType(msg.Subject, receiver, contentType, body)
	if recorder := emailDeliveryRecorder.Load(); recorder != nil {
		(*recorder)(receiver, purpose, err)
	}
	return err
}

var unifyaiEmailTemplate = template.Must(template.New("unifyai-email").Parse(`<!doctype html>
<html lang="en" xmlns="http://www.w3.org/1999/xhtml">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="x-apple-disable-message-reformatting">
<meta name="color-scheme" content="light">
<meta name="supported-color-schemes" content="light">
<title>{{.Msg.Subject}}</title>
{{.NoMsoOpen}}
<link href="https://fonts.googleapis.com/css2?family=Space+Grotesk:wght@700&family=Instrument+Sans:wght@400;600&family=IBM+Plex+Mono:wght@500&display=swap" rel="stylesheet">
{{.NoMsoEnd}}
<style>
  body { margin: 0; padding: 0; background: #FAFAF7; }
  a { color: #0E0E0E; }
  @media (max-width: 620px) {
    .u-card { width: 100% !important; }
    .u-pad { padding-left: 24px !important; padding-right: 24px !important; }
    .u-h1 { font-size: 24px !important; line-height: 30px !important; }
  }
</style>
</head>
<body style="margin:0; padding:0; background:#FAFAF7;">
<div style="display:none; max-height:0; overflow:hidden; mso-hide:all;">{{.Msg.Preheader}}&#8199;&#65279;&#847;&#8199;&#65279;&#847;&#8199;&#65279;&#847;</div>

<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background:#FAFAF7;">
<tr><td align="center" style="padding:32px 12px;">

  <table role="presentation" class="u-card" width="600" cellpadding="0" cellspacing="0" border="0" style="width:600px; max-width:600px; background:#FFFFFF; border:1px solid #E3E3DE; border-radius:6px;">

    <tr><td class="u-pad" style="padding:28px 40px 24px; border-bottom:1px solid #E3E3DE;">
      <table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr>
        <td style="vertical-align:middle;"><img src="{{.Brand.AssetBase}}/unifyai-lockup-black@2x.png" width="132" height="40" alt="UnifyAI" style="display:block; border:0; width:132px; height:40px;"></td>
        <td style="vertical-align:middle; padding:0 14px;"><div style="width:1px; height:22px; background:#CFCFC9;"></div></td>
        <td style="vertical-align:middle; font-family:'Space Grotesk',Helvetica,Arial,sans-serif; font-weight:700; font-size:16px; color:#0E0E0E; white-space:nowrap;">{{.Brand.ProductShort}}</td>
      </tr></table>
    </td></tr>

    <tr><td class="u-pad" style="padding:36px 40px 8px;">
      <div style="font-family:'IBM Plex Mono',Menlo,Consolas,monospace; font-size:12px; letter-spacing:1.6px; text-transform:uppercase; color:#6B6B66;">{{.Msg.Eyebrow}}</div>
      <h1 class="u-h1" style="margin:10px 0 0; font-family:'Space Grotesk',Helvetica,Arial,sans-serif; font-weight:700; font-size:28px; line-height:34px; letter-spacing:-0.4px; color:#0E0E0E;">{{.Msg.Headline}}</h1>
      {{if .Msg.IntroHTML}}<p style="margin:16px 0 0; font-family:'Instrument Sans',Helvetica,Arial,sans-serif; font-size:16px; line-height:26px; color:#3A3A37;">{{.Msg.IntroHTML}}</p>{{else if .Msg.Intro}}<p style="margin:16px 0 0; font-family:'Instrument Sans',Helvetica,Arial,sans-serif; font-size:16px; line-height:26px; color:#3A3A37;">{{.Msg.Intro}}</p>{{end}}
    </td></tr>
{{if .Msg.CTAURL}}
    <tr><td class="u-pad" style="padding:28px 40px 4px;">
      <table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr>
        <td style="border-radius:6px; background:#F5A623;">
          {{.MsoButton}}
          {{.NoMsoOpen}}<a href="{{.Msg.CTAURL}}" style="display:inline-block; padding:14px 26px; font-family:'Instrument Sans',Helvetica,Arial,sans-serif; font-weight:600; font-size:16px; line-height:20px; color:#0E0E0E; text-decoration:none; border-radius:6px;">{{.Msg.CTALabel}}</a>{{.NoMsoEnd}}
        </td>
      </tr></table>
      <p style="margin:14px 0 0; font-family:'Instrument Sans',Helvetica,Arial,sans-serif; font-size:13px; line-height:20px; color:#6B6B66;">Button not working? Paste this link into your browser:<br><a href="{{.Msg.CTAURL}}" style="color:#0E0E0E; word-break:break-all;">{{.Msg.CTAURL}}</a></p>
    </td></tr>
{{end}}{{if .Msg.Code}}
    <tr><td class="u-pad" style="padding:28px 40px 4px;">
      <div style="background:#0E0E0E; border-radius:6px; padding:20px 24px; font-family:'IBM Plex Mono',Menlo,Consolas,monospace; font-weight:500; font-size:32px; letter-spacing:8px; color:#FAFAF7; text-align:center;">{{.Msg.Code}}</div>
    </td></tr>
{{end}}{{if .Msg.Details}}
    <tr><td class="u-pad" style="padding:28px 40px 0;">
      <table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="border-top:1px solid #E3E3DE;">
{{range .Msg.Details}}        <tr>
          <td style="padding:10px 0; border-bottom:1px solid #E3E3DE; font-family:'Instrument Sans',Helvetica,Arial,sans-serif; font-size:14px; color:#6B6B66;">{{.Label}}</td>
          <td align="right" style="padding:10px 0; border-bottom:1px solid #E3E3DE; font-family:'IBM Plex Mono',Menlo,Consolas,monospace; font-size:14px; color:#0E0E0E;">{{.Value}}</td>
        </tr>
{{end}}      </table>
    </td></tr>
{{end}}
    <tr><td class="u-pad" style="padding:24px 40px 36px;">
      <p style="margin:0; font-family:'Instrument Sans',Helvetica,Arial,sans-serif; font-size:13px; line-height:20px; color:#6B6B66;">{{.Msg.SafetyNote}}</p>
    </td></tr>

  </table>

  <table role="presentation" class="u-card" width="600" cellpadding="0" cellspacing="0" border="0" style="width:600px; max-width:600px;">
    <tr><td class="u-pad" style="padding:24px 40px; font-family:'Instrument Sans',Helvetica,Arial,sans-serif; font-size:12px; line-height:19px; color:#6B6B66;">
      <img src="{{.Brand.AssetBase}}/unifyai-mark-black@2x.png" width="24" height="24" alt="" style="display:block; border:0; width:24px; height:24px; margin-bottom:10px;">
      {{.Brand.ProductName}} · <a href="{{.Brand.ProductURL}}" style="color:#6B6B66;">{{.Brand.ProductDomain}}</a><br>
      {{.Msg.FooterReason}}{{if .Brand.CompanyAddress}}<br>
      {{.Brand.CompanyAddress}}{{end}}
    </td></tr>
  </table>

</td></tr>
</table>
</body>
</html>
`))
