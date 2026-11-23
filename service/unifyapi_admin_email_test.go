/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package service

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAdminMailer replaces delivery for one test: nothing reaches SMTP, and
// the test reads exactly what would have been handed to the mail server.
type fakeAdminMailer struct {
	mu   sync.Mutex
	sent map[string]common.EmailMessage
	fail map[string]error
}

func useFakeAdminMailer(t *testing.T) *fakeAdminMailer {
	t.Helper()
	f := &fakeAdminMailer{sent: map[string]common.EmailMessage{}, fail: map[string]error{}}
	prevDeliver, prevGap := adminEmailDeliver, adminEmailSendGap
	adminEmailDeliver = func(_ string, receiver string, msg common.EmailMessage) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		if err := f.fail[receiver]; err != nil {
			return err
		}
		f.sent[receiver] = msg
		return nil
	}
	adminEmailSendGap = 0
	t.Cleanup(func() { adminEmailDeliver, adminEmailSendGap = prevDeliver, prevGap })
	return f
}

func setupAdminEmailTest(t *testing.T) *capturingSMTP {
	t.Helper()
	smtp := setupRetentionTest(t)
	require.NoError(t, model.MigrateAdminEmail())
	prevReply := system_setting.GetAdminEmailSettings().ReplyTo
	t.Cleanup(func() { system_setting.GetAdminEmailSettings().ReplyTo = prevReply })
	system_setting.GetAdminEmailSettings().ReplyTo = "support@unifyapi.ai"
	return smtp
}

func waitForAdminSends(t *testing.T, batch string) []model.AdminEmailSend {
	t.Helper()
	var rows []model.AdminEmailSend
	require.Eventually(t, func() bool {
		var err error
		rows, err = model.ListAdminEmailSends(0, batch, 100)
		require.NoError(t, err)
		for _, r := range rows {
			if r.Status == model.AdminEmailStatusQueued {
				return false
			}
		}
		return true
	}, 10*time.Second, 20*time.Millisecond)
	return rows
}

func byUser(rows []model.AdminEmailSend) map[int]model.AdminEmailSend {
	out := map[int]model.AdminEmailSend{}
	for _, r := range rows {
		out[r.UserId] = r
	}
	return out
}

func TestVariablesAreSubstitutedAndEscapedInTheHTMLPart(t *testing.T) {
	setupAdminEmailTest(t)
	u := &model.User{Id: 7, Username: `<script>alert(1)</script>`, DisplayName: `Tom & "Jerry"`, Email: "t@example.com", Group: "default"}
	msg, err := BuildAdminEmail(AdminEmailContent{
		Subject: "Hello {{display_name}}",
		Body:    "Hi {{ display_name }},\n\nyour login is {{username}}.\nDocs: https://docs.example.com/a?b=1&c=2.",
	}, u, false)
	require.NoError(t, err)
	assert.Equal(t, `Hello Tom & "Jerry"`, msg.Subject, "the subject is base64-encoded on the wire, so it stays literal")

	html, text, err := common.RenderEmail(UnifyAPIEmailBrand(), msg)
	require.NoError(t, err)
	assert.NotContains(t, html, "<script>alert(1)</script>", "a variable is never emitted as markup")
	assert.Contains(t, html, "&lt;script&gt;alert(1)&lt;/script&gt;")
	assert.Contains(t, html, "Tom &amp; &#34;Jerry&#34;")
	assert.Contains(t, html, `<a href="https://docs.example.com/a?b=1&amp;c=2" style="color:#0E0E0E;">`, "a URL becomes a link, without the full stop after it")
	assert.Contains(t, html, "<br><br>your login is", "a blank line is a paragraph break")
	assert.Contains(t, text, "Hi Tom & \"Jerry\",\n\nyour login is <script>alert(1)</script>.", "the text part is plain text, unescaped")
	assert.Contains(t, text, "You're receiving this because you have a")
}

func TestABodyCannotSmuggleHTMLOrAttributes(t *testing.T) {
	setupAdminEmailTest(t)
	msg, err := BuildAdminEmail(AdminEmailContent{Subject: "s",
		Body: `<img src=x onerror=alert(1)> https://evil.example/"onmouseover="x`}, &model.User{Username: "u"}, false)
	require.NoError(t, err)
	body := string(msg.IntroHTML)
	assert.NotContains(t, body, "<img")
	assert.Contains(t, body, `<a href="https://evil.example/" `, "the link stops at the quote")
	assert.NotContains(t, body, `"onmouseover`)
}

func TestSubjectAndHeadlineCannotCarryALineBreak(t *testing.T) {
	setupAdminEmailTest(t)
	u := &model.User{Username: "eve\r\nBcc: victim@example.com", Email: "e@example.com"}
	msg, err := BuildAdminEmail(AdminEmailContent{
		Subject:  "Hi {{username}}\r\nX-Injected: yes",
		Headline: "Line one\nLine two three",
		Body:     "b",
	}, u, false)
	require.NoError(t, err)
	assert.NotContains(t, msg.Subject, "\r")
	assert.NotContains(t, msg.Subject, "\n")
	assert.Equal(t, "Hi eve Bcc: victim@example.com X-Injected: yes", msg.Subject)
	assert.Equal(t, "Line one Line two three", msg.Headline)
}

func TestAnUnknownVariableIsRefusedNotSentLiterally(t *testing.T) {
	err := ValidateAdminEmailContent(AdminEmailContent{Subject: "Hi {{first_name}}", Body: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "{{first_name}}")
	assert.Contains(t, err.Error(), "{{display_name}}", "the error lists what is available")

	require.Error(t, ValidateAdminEmailContent(AdminEmailContent{Subject: "s", Body: "x", CTALabel: "Go"}), "a button needs a link")
	require.Error(t, ValidateAdminEmailContent(AdminEmailContent{Subject: "s", Body: "x", CTALabel: "Go", CTAURL: "javascript:alert(1)"}))
	require.Error(t, ValidateAdminEmailContent(AdminEmailContent{Subject: " ", Body: "x"}))
	require.Error(t, ValidateAdminEmailContent(AdminEmailContent{Subject: "s", Body: "  "}))
}

func TestTheBuiltInTemplatesRenderAndKeepToTheCopyRules(t *testing.T) {
	setupAdminEmailTest(t)
	rows, err := model.ListAdminEmailTemplates()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, tpl := range rows {
		require.NoError(t, ValidateAdminEmailContent(AdminEmailContent{Subject: tpl.Subject, Headline: tpl.Headline,
			Body: tpl.Body, CTALabel: tpl.CTALabel, CTAURL: tpl.CTAURL}), tpl.Name)
		all := tpl.Subject + tpl.Headline + tpl.Body
		assert.Contains(t, all, "Unify API", tpl.Name)
		assert.NotContains(t, all, "UnifyAPI", "customer copy spells the product with a space")
		assert.NotContains(t, strings.ToLower(all), "newapi")
		assert.NotRegexp(t, `\d+\+? models`, all, "no model count we have not verified")
	}
	mythos := rows[1]
	assert.Equal(t, model.AdminEmailBuiltinMythos, mythos.BuiltinKey)
	assert.NotContains(t, strings.ToLower(mythos.Body), "$", "no pricing claims")
	assert.NotContains(t, strings.ToLower(mythos.Body), "will be approved")
}

func TestBuiltInsAreSeededOnceAndADeletedOneStaysDeleted(t *testing.T) {
	setupAdminEmailTest(t)
	require.NoError(t, model.MigrateAdminEmail(), "a second boot migrates again")
	rows, err := model.ListAdminEmailTemplates()
	require.NoError(t, err)
	require.Len(t, rows, 2, "and seeds nothing twice")

	require.NoError(t, model.DeleteAdminEmailTemplate(rows[0].Id))
	require.NoError(t, model.MigrateAdminEmail())
	rows, err = model.ListAdminEmailTemplates()
	require.NoError(t, err)
	require.Len(t, rows, 1, "an admin's delete survives a restart")
}

func TestUsersWithoutAnAddressAreSkippedAndEverySendIsLogged(t *testing.T) {
	setupAdminEmailTest(t)
	mail := useFakeAdminMailer(t)
	alice := addRetentionUser(t, "alice", "alice@example.com", common.RoleCommonUser, common.UserStatusEnabled, 0, 0)
	noMail := addRetentionUser(t, "nomail", "", common.RoleCommonUser, common.UserStatusEnabled, 0, 0)
	bad := addRetentionUser(t, "bad", "a@example.com;b@example.com", common.RoleCommonUser, common.UserStatusEnabled, 0, 0)
	disabled := addRetentionUser(t, "off", "off@example.com", common.RoleCommonUser, common.UserStatusDisabled, 0, 0)
	optedOut := addRetentionUser(t, "optout", "optout@example.com", common.RoleCommonUser, common.UserStatusEnabled, 0, 0)
	failing := addRetentionUser(t, "bounce", "bounce@example.com", common.RoleCommonUser, common.UserStatusEnabled, 0, 0)
	require.NoError(t, model.UnsubscribeRetentionEmail(optedOut.Id))
	mail.fail["bounce@example.com"] = errors.New("550 mailbox unavailable")

	content := AdminEmailContent{TemplateId: 3, Subject: "Hello {{display_name}}", Body: "Hi"}
	ids := []int{alice.Id, noMail.Id, bad.Id, disabled.Id, optedOut.Id, failing.Id, 99999, alice.Id}

	preview, err := PreviewAdminEmail(content, ids)
	require.NoError(t, err)
	assert.True(t, preview.Bulk)
	assert.Equal(t, 2, preview.WillSend, "preview counts who would be sent to")
	assert.Equal(t, 5, preview.Skipped)
	assert.Equal(t, alice.Id, preview.PreviewUser, "the preview is the first recipient who gets it")
	assert.Contains(t, preview.HTML, "Unsubscribe", "a bulk email carries the opt-out link")
	assert.Empty(t, mail.sent, "preview sends nothing")
	none, err := model.ListAdminEmailSends(0, "", 100)
	require.NoError(t, err)
	assert.Empty(t, none, "and writes nothing")

	result, err := SendAdminEmail(content, ids, 42)
	require.NoError(t, err)
	assert.Equal(t, 2, result.Queued)
	assert.Equal(t, 5, result.Skipped)

	rows := byUser(waitForAdminSends(t, result.BatchId))
	require.Len(t, rows, 7, "one row per distinct requested id, skipped ones included")
	want := map[int]struct{ status, reason string }{
		alice.Id:    {model.AdminEmailStatusSent, ""},
		noMail.Id:   {model.AdminEmailStatusSkipped, "no email address"},
		bad.Id:      {model.AdminEmailStatusSkipped, "the email address is not valid"},
		disabled.Id: {model.AdminEmailStatusSkipped, "account is disabled (bulk sends skip it)"},
		optedOut.Id: {model.AdminEmailStatusSkipped, "unsubscribed from product email"},
		failing.Id:  {model.AdminEmailStatusFailed, "550 mailbox unavailable"},
		99999:       {model.AdminEmailStatusSkipped, "user not found"},
	}
	for id, w := range want {
		r := rows[id]
		assert.Equal(t, w.status, r.Status, "user %d", id)
		assert.Equal(t, w.reason, r.Error, "user %d", id)
		assert.Equal(t, 42, r.SenderId, "who sent it")
		assert.Equal(t, 3, r.TemplateId, "from which template")
		assert.NotZero(t, r.CreatedAt, "when")
	}
	assert.Equal(t, "Hello alice", rows[alice.Id].Subject, "the subject each recipient got")
	assert.NotZero(t, rows[alice.Id].SentAt)

	require.Len(t, mail.sent, 1)
	got := mail.sent["alice@example.com"]
	assert.Equal(t, "support@unifyapi.ai", got.ReplyTo)
	assert.NotEmpty(t, got.UnsubscribeURL)
}

func TestAMessageToOneUserIsPersonalAndCarriesNoUnsubscribe(t *testing.T) {
	setupAdminEmailTest(t)
	mail := useFakeAdminMailer(t)
	u := addRetentionUser(t, "solo", "solo@example.com", common.RoleCommonUser, common.UserStatusDisabled, 0, 0)
	require.NoError(t, model.UnsubscribeRetentionEmail(u.Id))

	result, err := SendAdminEmail(AdminEmailContent{Subject: "About your account", Body: "Hi"}, []int{u.Id}, 1)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Queued, "staff writing to one person is not product mail")
	rows := waitForAdminSends(t, result.BatchId)
	require.Len(t, rows, 1)
	assert.Equal(t, model.AdminEmailStatusSent, rows[0].Status)
	assert.Empty(t, mail.sent["solo@example.com"].UnsubscribeURL)
}

func TestNothingIsRecordedWhenOneRecipientCannotBeRendered(t *testing.T) {
	setupAdminEmailTest(t)
	useFakeAdminMailer(t)
	a := addRetentionUser(t, "a", "a@example.com", common.RoleCommonUser, common.UserStatusEnabled, 0, 0)
	_, err := SendAdminEmail(AdminEmailContent{Subject: "Hi {{nope}}", Body: "x"}, []int{a.Id}, 1)
	require.Error(t, err)
	rows, err := model.ListAdminEmailSends(0, "", 100)
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func TestTheWireMessageHasReplyToAndNoInjectedHeader(t *testing.T) {
	smtp := setupAdminEmailTest(t)
	prevGap := adminEmailSendGap
	adminEmailSendGap = 0
	t.Cleanup(func() { adminEmailSendGap = prevGap })
	a := addRetentionUser(t, "wire\r\nX-Evil: 1", "wire@example.com", common.RoleCommonUser, common.UserStatusEnabled, 0, 0)
	b := addRetentionUser(t, "wire2", "wire2@example.com", common.RoleCommonUser, common.UserStatusEnabled, 0, 0)

	result, err := SendAdminEmail(AdminEmailContent{Subject: "Hi {{username}}\nX-Evil: 2", Body: "Hello"}, []int{a.Id, b.Id}, 1)
	require.NoError(t, err)
	waitForAdminSends(t, result.BatchId)
	raw := smtp.sent()["wire@example.com"]
	require.NotEmpty(t, raw, "delivered to the local capture server, nowhere else")
	headers := raw[:strings.Index(raw, "\r\n\r\n")]
	assert.Contains(t, headers, "\r\nReply-To: support@unifyapi.ai\r\n")
	assert.Contains(t, headers, "\r\nList-Unsubscribe: <https://app.unifyapi.ai/api/email/unsubscribe?")
	assert.NotContains(t, headers, "X-Evil")
	assert.Contains(t, headers, "multipart/alternative")
}

func TestAReplyToThatIsNotOneBareAddressIsDropped(t *testing.T) {
	setupAdminEmailTest(t)
	for _, v := range []string{"Support <s@example.com>", "a@example.com, b@example.com", "s@example.com\r\nBcc: x@example.com", "not-an-address"} {
		assert.False(t, common.IsBareEmailAddress(v), v)
	}
	assert.True(t, common.IsBareEmailAddress("support@unifyapi.ai"))
}
