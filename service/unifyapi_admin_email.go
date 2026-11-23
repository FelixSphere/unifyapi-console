/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package service

// UNIFYAPI-FORK: email an admin writes to customers from the Users page, one
// user or many, from a template or from scratch. Persistence is in
// model/unifyapi_admin_email.go; the routes are AdminAuth.
//
// What it guarantees:
//   - The body is plain text. Paragraphs and line breaks are kept and URLs
//     become links; nothing an admin or a customer typed is ever emitted as
//     HTML. Variables are substituted BEFORE escaping, so a username like
//     "<b>x</b>" reaches the HTML part as text.
//   - The subject and headline cannot carry a line break, so no value can
//     start a header of its own.
//   - An unknown {{variable}} is an error at preview, save and send, never a
//     literal "{{foo}}" in a customer's inbox.
//   - Every recipient gets a row in admin_email_sends before anything is
//     queued, including the ones skipped (no address, an address that is not
//     one bare address, a disabled account or an opt-out on a bulk send).
//   - Messages leave one at a time, adminEmailSendGap apart (4/s), from one
//     worker per process. SES allows 14/s; this and the other two product
//     senders (weekly update, launch announcements) at the same gap stay
//     under it with room for verification codes.
//   - A bulk send (more than one recipient) honours the product-mail opt-out
//     the weekly update and launch emails share, and carries the same
//     Unsubscribe link and List-Unsubscribe header. A message to one user is
//     a personal message from staff and is sent even if they opted out of
//     product mail, the way a support reply would be.

import (
	"errors"
	"fmt"
	"html/template"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

const (
	adminEmailPurpose       = "admin-message"
	AdminEmailMaxRecipients = 500
	adminEmailMaxBody       = 20000
	adminEmailMaxLine       = 255
)

// adminEmailSendGap is the pause after each message. A variable so tests do
// not wait.
var adminEmailSendGap = 250 * time.Millisecond

// adminEmailDeliver hands one message to the mail server. Tests replace it;
// nothing in a test may reach SMTP.
var adminEmailDeliver = SendBrandedEmail

// AdminEmailContent is what an admin composes. TemplateId is recorded with
// each send; the text itself always comes from the request, because the
// compose dialog lets the admin edit a template before sending it.
type AdminEmailContent struct {
	TemplateId int    `json:"template_id"`
	Subject    string `json:"subject"`
	Headline   string `json:"headline"`
	Body       string `json:"body"`
	CTALabel   string `json:"cta_label"`
	CTAURL     string `json:"cta_url"`
}

// AdminEmailVariables are the names a template may use, in display order.
var AdminEmailVariables = []string{"username", "display_name", "email", "group", "console_url", "docs_url", "api_base_url"}

var (
	errAdminEmailNoSubject = errors.New("the subject is empty")
	errAdminEmailNoBody    = errors.New("the message is empty")
	adminEmailVariable     = regexp.MustCompile(`\{\{\s*([^{}]*?)\s*\}\}`)
	adminEmailURL          = regexp.MustCompile(`https?://[^\s<>"']+`)
)

// adminEmailVars are the values for one recipient.
func adminEmailVars(u *model.User) map[string]string {
	display := strings.TrimSpace(u.DisplayName)
	if display == "" {
		display = u.Username
	}
	docs := strings.TrimSpace(operation_setting.GetGeneralSetting().DocsLink)
	return map[string]string{
		"username":     u.Username,
		"display_name": display,
		"email":        u.Email,
		"group":        u.Group,
		"console_url":  strings.TrimRight(system_setting.ServerAddress, "/"),
		"docs_url":     docs,
		"api_base_url": RetentionAPIBase,
	}
}

// substituteAdminEmailVars replaces every {{name}}; an unknown name is an
// error naming it.
func substituteAdminEmailVars(s string, vars map[string]string) (string, error) {
	var unknown []string
	out := adminEmailVariable.ReplaceAllStringFunc(s, func(m string) string {
		name := adminEmailVariable.FindStringSubmatch(m)[1]
		v, ok := vars[name]
		if !ok {
			unknown = append(unknown, "{{"+name+"}}")
			return m
		}
		return v
	})
	if len(unknown) > 0 {
		return "", fmt.Errorf("unknown variable %s; available: %s", strings.Join(unknown, ", "), adminEmailVariableList())
	}
	return out, nil
}

func adminEmailVariableList() string {
	names := make([]string, len(AdminEmailVariables))
	for i, n := range AdminEmailVariables {
		names[i] = "{{" + n + "}}"
	}
	return strings.Join(names, " ")
}

// singleLine folds any line break (CR, LF, or the Unicode separators) into a
// space: a subject is one header line and must stay one.
func singleLine(s string) string {
	s = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ", " ", " ", " ", " ", "\u0085", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// adminEmailBodyHTML renders plain text as escaped HTML: blank lines separate
// paragraphs, single newlines are kept, http(s) URLs become links. It goes
// inside the layout's one intro <p>, so paragraphs are double breaks.
func adminEmailBodyHTML(body string) string {
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n")
	paragraphs := regexp.MustCompile(`\n[ \t]*\n+`).Split(strings.TrimSpace(body), -1)
	out := make([]string, 0, len(paragraphs))
	for _, p := range paragraphs {
		lines := strings.Split(p, "\n")
		for i, line := range lines {
			lines[i] = linkifyEscaped(strings.TrimRight(line, " \t"))
		}
		out = append(out, strings.Join(lines, "<br>"))
	}
	return strings.Join(out, "<br><br>")
}

// linkifyEscaped escapes a line of text and turns its URLs into anchors.
// Trailing sentence punctuation is left outside the link.
func linkifyEscaped(line string) string {
	var b strings.Builder
	last := 0
	for _, loc := range adminEmailURL.FindAllStringIndex(line, -1) {
		start, end := loc[0], loc[1]
		for end > start && strings.ContainsRune(".,;:!?)]", rune(line[end-1])) {
			end--
		}
		b.WriteString(htmlEscape(line[last:start]))
		u := htmlEscape(line[start:end])
		b.WriteString(`<a href="` + u + `" style="color:#0E0E0E;">` + u + `</a>`)
		last = end
	}
	b.WriteString(htmlEscape(line[last:]))
	return b.String()
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;", "'", "&#39;").Replace(s)
}

// ValidateAdminEmailContent checks the shape of a message (or a template)
// without a recipient: lengths, the CTA pair, and that every variable is
// known. Rendering for a real recipient checks the substituted values.
func ValidateAdminEmailContent(c AdminEmailContent) error {
	_, err := BuildAdminEmail(c, &model.User{Username: "username", DisplayName: "Display Name", Email: "user@example.com", Group: "default"}, false)
	return err
}

// BuildAdminEmail renders c for one recipient. bulk adds the unsubscribe link.
func BuildAdminEmail(c AdminEmailContent, u *model.User, bulk bool) (common.EmailMessage, error) {
	vars := adminEmailVars(u)
	subject, err := substituteAdminEmailVars(c.Subject, vars)
	if err != nil {
		return common.EmailMessage{}, fmt.Errorf("subject: %w", err)
	}
	subject = singleLine(subject)
	if subject == "" {
		return common.EmailMessage{}, errAdminEmailNoSubject
	}
	if utf8.RuneCountInString(subject) > adminEmailMaxLine {
		return common.EmailMessage{}, fmt.Errorf("the subject is longer than %d characters", adminEmailMaxLine)
	}
	headline, err := substituteAdminEmailVars(c.Headline, vars)
	if err != nil {
		return common.EmailMessage{}, fmt.Errorf("headline: %w", err)
	}
	headline = singleLine(headline)
	if utf8.RuneCountInString(headline) > adminEmailMaxLine {
		return common.EmailMessage{}, fmt.Errorf("the headline is longer than %d characters", adminEmailMaxLine)
	}
	if utf8.RuneCountInString(c.Body) > adminEmailMaxBody {
		return common.EmailMessage{}, fmt.Errorf("the message is longer than %d characters", adminEmailMaxBody)
	}
	body, err := substituteAdminEmailVars(c.Body, vars)
	if err != nil {
		return common.EmailMessage{}, fmt.Errorf("message: %w", err)
	}
	if strings.TrimSpace(body) == "" {
		return common.EmailMessage{}, errAdminEmailNoBody
	}
	ctaLabel, err := substituteAdminEmailVars(c.CTALabel, vars)
	if err != nil {
		return common.EmailMessage{}, fmt.Errorf("button label: %w", err)
	}
	ctaURL, err := substituteAdminEmailVars(c.CTAURL, vars)
	if err != nil {
		return common.EmailMessage{}, fmt.Errorf("button link: %w", err)
	}
	ctaLabel, ctaURL = singleLine(ctaLabel), strings.TrimSpace(ctaURL)
	if (ctaLabel == "") != (ctaURL == "") {
		return common.EmailMessage{}, errors.New("a button needs both a label and a link")
	}
	if ctaURL != "" {
		parsed, perr := url.Parse(ctaURL)
		if perr != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || strings.ContainsAny(ctaURL, " \t\r\n\"'<>") {
			return common.EmailMessage{}, fmt.Errorf("the button link %q is not an http(s) URL", ctaURL)
		}
	}

	brand := UnifyAPIEmailBrand()
	msg := common.EmailMessage{
		Subject:      subject,
		Preheader:    adminEmailPreheader(body),
		Headline:     headline,
		IntroHTML:    template.HTML(adminEmailBodyHTML(body)), // escaped above, line by line
		CTALabel:     ctaLabel,
		CTAURL:       ctaURL,
		FooterReason: fmt.Sprintf("You're receiving this because you have a %s account.", brand.ProductName),
		ReplyTo:      strings.TrimSpace(system_setting.GetAdminEmailSettings().ReplyTo),
	}
	if bulk {
		msg.UnsubscribeURL = RetentionUnsubscribeURL(u.Id)
	}
	return msg, nil
}

// adminEmailPreheader is the inbox snippet: the first line of the body, cut
// at a word. The layout's rule is that it adds to the subject, and the body's
// opening does.
func adminEmailPreheader(body string) string {
	first := singleLine(strings.SplitN(strings.TrimSpace(body), "\n", 2)[0])
	if utf8.RuneCountInString(first) <= 110 {
		return first
	}
	r := []rune(first)[:110]
	if i := strings.LastIndex(string(r), " "); i > 60 {
		return string(r)[:i] + "…"
	}
	return string(r) + "…"
}

// AdminEmailRecipientResult is what happened, or will happen, to one id.
type AdminEmailRecipientResult struct {
	UserId   int    `json:"user_id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Status   string `json:"status"` // queued (or "will send" on preview) / skipped
	Reason   string `json:"reason,omitempty"`
}

type adminEmailPlan struct {
	bulk     bool
	eligible []model.User
	results  []AdminEmailRecipientResult
}

// planAdminEmail decides, per requested id, whether it can be sent to.
func planAdminEmail(userIds []int) (*adminEmailPlan, error) {
	ids := uniquePositive(userIds)
	if len(ids) == 0 {
		return nil, errors.New("no recipients")
	}
	if len(ids) > AdminEmailMaxRecipients {
		return nil, fmt.Errorf("at most %d recipients per send; %d were selected", AdminEmailMaxRecipients, len(ids))
	}
	users, err := model.AdminEmailRecipients(ids)
	if err != nil {
		return nil, err
	}
	byId := make(map[int]model.User, len(users))
	for _, u := range users {
		byId[u.Id] = u
	}
	plan := &adminEmailPlan{bulk: len(ids) > 1}
	var unsubscribed map[int]bool
	if plan.bulk {
		if unsubscribed, err = model.AdminEmailUnsubscribed(ids); err != nil {
			return nil, err
		}
	}
	for _, id := range ids {
		u, ok := byId[id]
		r := AdminEmailRecipientResult{UserId: id, Username: u.Username, Email: strings.TrimSpace(u.Email), Status: model.AdminEmailStatusQueued}
		switch {
		case !ok:
			r.Status, r.Reason = model.AdminEmailStatusSkipped, "user not found"
		case r.Email == "":
			r.Status, r.Reason = model.AdminEmailStatusSkipped, "no email address"
		case !common.IsBareEmailAddress(r.Email):
			r.Status, r.Reason = model.AdminEmailStatusSkipped, "the email address is not valid"
		case plan.bulk && u.Status != common.UserStatusEnabled:
			r.Status, r.Reason = model.AdminEmailStatusSkipped, "account is disabled (bulk sends skip it)"
		case plan.bulk && unsubscribed[id]:
			r.Status, r.Reason = model.AdminEmailStatusSkipped, "unsubscribed from product email"
		default:
			u.Email = r.Email
			plan.eligible = append(plan.eligible, u)
		}
		plan.results = append(plan.results, r)
	}
	return plan, nil
}

func uniquePositive(ids []int) []int {
	seen := map[int]bool{}
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Ints(out)
	return out
}

// AdminEmailPreview is the rendered email for one recipient plus who the
// send would reach. Nothing is written or sent.
type AdminEmailPreview struct {
	Subject     string                      `json:"subject"`
	HTML        string                      `json:"html"`
	Text        string                      `json:"text"`
	PreviewUser int                         `json:"preview_user_id"`
	Bulk        bool                        `json:"bulk"`
	WillSend    int                         `json:"will_send"`
	Skipped     int                         `json:"skipped"`
	Recipients  []AdminEmailRecipientResult `json:"recipients"`
	ReplyTo     string                      `json:"reply_to"`
	SMTPReady   bool                        `json:"smtp_ready"`
	Unsubscribe bool                        `json:"unsubscribe_link"`
}

// PreviewAdminEmail renders c for the first recipient who would receive it
// (or the first requested user when none would).
func PreviewAdminEmail(c AdminEmailContent, userIds []int) (*AdminEmailPreview, error) {
	plan, err := planAdminEmail(userIds)
	if err != nil {
		return nil, err
	}
	var sample *model.User
	if len(plan.eligible) > 0 {
		sample = &plan.eligible[0]
	} else {
		sample = &model.User{Id: plan.results[0].UserId, Username: plan.results[0].Username, Email: plan.results[0].Email}
	}
	msg, err := BuildAdminEmail(c, sample, plan.bulk)
	if err != nil {
		return nil, err
	}
	html, text, err := common.RenderEmail(UnifyAPIEmailBrand(), msg)
	if err != nil {
		return nil, err
	}
	p := &AdminEmailPreview{Subject: msg.Subject, HTML: html, Text: text, PreviewUser: sample.Id, Bulk: plan.bulk,
		Recipients: plan.results, ReplyTo: msg.ReplyTo, SMTPReady: common.SMTPServer != "", Unsubscribe: plan.bulk}
	p.WillSend = len(plan.eligible)
	p.Skipped = len(plan.results) - p.WillSend
	return p, nil
}

// AdminEmailSendResult is returned by SendAdminEmail: the batch to poll and
// what was decided for each recipient.
type AdminEmailSendResult struct {
	BatchId    string                      `json:"batch_id"`
	Queued     int                         `json:"queued"`
	Skipped    int                         `json:"skipped"`
	Recipients []AdminEmailRecipientResult `json:"recipients"`
}

type adminEmailJob struct {
	sendId int
	email  string
	msg    common.EmailMessage
}

var (
	adminEmailQueue      = make(chan adminEmailJob, 5000)
	adminEmailWorkerOnce sync.Once
)

// SendAdminEmail records one row per recipient and queues the eligible ones.
// It returns once everything is recorded and queued; delivery happens in the
// background at adminEmailSendGap per message.
func SendAdminEmail(c AdminEmailContent, userIds []int, senderId int) (*AdminEmailSendResult, error) {
	if common.SMTPServer == "" {
		return nil, errors.New("SMTP is not configured, so nothing can be sent")
	}
	plan, err := planAdminEmail(userIds)
	if err != nil {
		return nil, err
	}
	if len(plan.eligible) > cap(adminEmailQueue)-len(adminEmailQueue) {
		return nil, errors.New("the send queue is full; try again in a few minutes")
	}
	// Render every message before recording anything, so a bad value for one
	// recipient refuses the whole send instead of half of it.
	msgs := make(map[int]common.EmailMessage, len(plan.eligible))
	for i := range plan.eligible {
		u := &plan.eligible[i]
		msg, err := BuildAdminEmail(c, u, plan.bulk)
		if err != nil {
			return nil, fmt.Errorf("user %d: %w", u.Id, err)
		}
		msgs[u.Id] = msg
	}
	if len(msgs) == 0 {
		// Still recorded: an admin who pressed Send should see why nobody got it.
		_, _, _ = recordAdminEmailRows(c, plan, msgs, senderId)
		return &AdminEmailSendResult{Skipped: len(plan.results), Recipients: plan.results}, nil
	}
	batch, rows, err := recordAdminEmailRows(c, plan, msgs, senderId)
	if err != nil {
		return nil, err
	}
	ensureAdminEmailWorker()
	queued := 0
	for _, row := range rows {
		if row.Status != model.AdminEmailStatusQueued {
			continue
		}
		adminEmailQueue <- adminEmailJob{sendId: row.Id, email: row.Email, msg: msgs[row.UserId]}
		queued++
	}
	return &AdminEmailSendResult{BatchId: batch, Queued: queued, Skipped: len(plan.results) - queued, Recipients: plan.results}, nil
}

func recordAdminEmailRows(c AdminEmailContent, plan *adminEmailPlan, msgs map[int]common.EmailMessage, senderId int) (string, []model.AdminEmailSend, error) {
	batch := model.NewAdminEmailBatchId()
	now := time.Now().Unix()
	fallbackSubject := singleLine(c.Subject)
	if utf8.RuneCountInString(fallbackSubject) > adminEmailMaxLine {
		fallbackSubject = string([]rune(fallbackSubject)[:adminEmailMaxLine])
	}
	rows := make([]model.AdminEmailSend, 0, len(plan.results))
	for _, r := range plan.results {
		row := model.AdminEmailSend{BatchId: batch, SenderId: senderId, TemplateId: c.TemplateId, Subject: fallbackSubject,
			UserId: r.UserId, Email: r.Email, Status: r.Status, Error: r.Reason, CreatedAt: now}
		if msg, ok := msgs[r.UserId]; ok {
			row.Subject = msg.Subject
		}
		rows = append(rows, row)
	}
	if err := model.CreateAdminEmailSends(rows); err != nil {
		return "", nil, err
	}
	return batch, rows, nil
}

func ensureAdminEmailWorker() {
	adminEmailWorkerOnce.Do(func() {
		go func() {
			for job := range adminEmailQueue {
				deliverAdminEmail(job)
				time.Sleep(adminEmailSendGap)
			}
		}()
	})
}

func deliverAdminEmail(job adminEmailJob) {
	defer func() {
		if r := recover(); r != nil {
			_ = model.FinishAdminEmailSend(job.sendId, fmt.Errorf("internal error: %v", r))
		}
	}()
	err := adminEmailDeliver(adminEmailPurpose, job.email, job.msg)
	if err != nil {
		common.SysError(fmt.Sprintf("admin email: send %d: %s", job.sendId, err.Error()))
	}
	if ferr := model.FinishAdminEmailSend(job.sendId, err); ferr != nil {
		common.SysError(fmt.Sprintf("admin email: record send %d: %s", job.sendId, ferr.Error()))
	}
}

// SweepInterruptedAdminEmail runs once at boot: anything still queued from
// before this process started cannot be in this process's queue.
func SweepInterruptedAdminEmail() {
	if !common.IsMasterNode {
		return
	}
	n, err := model.InterruptStaleAdminEmailSends(time.Now().Unix())
	if err != nil {
		common.SysError("admin email: boot sweep: " + err.Error())
		return
	}
	if n > 0 {
		common.SysLog(fmt.Sprintf("admin email: %d queued message(s) from before the restart marked interrupted", n))
	}
}
