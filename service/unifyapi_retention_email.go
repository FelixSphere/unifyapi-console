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

// UNIFYAPI-FORK: a weekly email to every registered customer (operator
// decision 2026-10-09: "不时和已注册的客户保持联系，比如邮件，保持他们对我们的
// 平台的awareness", weekly, fully automated).
//
// Off unless UNIFYAPI_RETENTION_EMAIL=on. It is an environment variable and
// not an option on purpose: staging runs on a copy of production's options
// AND production's customer emails, so an option switched on in production
// would also be on in staging, and real customers would get two emails.
//
// Every number in the email is read from the database at send time -- the
// customer's own usage and balance, and models that appeared in the
// catalogue since last week's run. Nothing in it is a marketing claim a
// person wrote, so nothing in it can go stale or be wrong the way the
// homepage's "300+ models" was.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

const (
	retentionEmailEnv = "UNIFYAPI_RETENTION_EMAIL"
	// Tuesday 02:00 UTC = 10:00 in Kuala Lumpur, mid-morning for Asia and the
	// evening before for the Americas.
	retentionSendWeekday = time.Tuesday
	retentionSendHour    = 2
	retentionPurpose     = "weekly-update"
	// SES allows 14/s; one every 250ms leaves room for verification codes.
	retentionSendGap   = 250 * time.Millisecond
	retentionNewModels = 8 // listed by name; the rest are counted
	// RetentionAPIBase is the OpenAI-compatible base URL customers point an
	// SDK at. api.unifyapi.ai is the relay host (app. serves the console).
	RetentionAPIBase = "https://api.unifyapi.ai/v1"
)

func RetentionEmailEnabled() bool {
	return os.Getenv(retentionEmailEnv) == "on"
}

// StartRetentionEmailWorker checks every 30 minutes whether this week's email
// is due and sends it. A no-op unless enabled, and only on the master node.
func StartRetentionEmailWorker() {
	if !RetentionEmailEnabled() || !common.IsMasterNode {
		return
	}
	if err := model.MigrateRetentionEmail(); err != nil {
		common.SysError("weekly update: migrate failed, not starting: " + err.Error())
		return
	}
	common.SysLog("weekly update: enabled")
	go func() {
		time.Sleep(2 * time.Minute) // let the boot finish and options load
		for {
			if week, due := retentionWeekDue(time.Now().UTC()); due {
				RunRetentionEmailWeek(week)
			}
			time.Sleep(30 * time.Minute)
		}
	}()
}

// retentionWeekDue names the ISO week of now (e.g. "2026-W41") and says
// whether that week's send time has passed. Weeks run Monday to Sunday, so
// a release that lands after Tuesday still sends that week.
func retentionWeekDue(now time.Time) (string, bool) {
	year, wk := now.ISOWeek()
	week := fmt.Sprintf("%d-W%02d", year, wk)
	daysSinceMonday := (int(now.Weekday()) + 6) % 7
	monday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -daysSinceMonday)
	sendAt := monday.AddDate(0, 0, int(retentionSendWeekday)-int(time.Monday)).Add(retentionSendHour * time.Hour)
	return week, !now.Before(sendAt)
}

// RunRetentionEmailWeek sends week's email to everyone not yet claimed for
// it. Safe to call repeatedly: each (week, user) is claimed once.
func RunRetentionEmailWeek(week string) {
	if common.SMTPServer == "" {
		return
	}
	current, err := model.EnabledModelNames()
	if err != nil {
		common.SysError("weekly update: read catalogue: " + err.Error())
		return
	}
	previous, err := model.SaveRetentionWeekModels(week, current)
	if err != nil {
		common.SysError("weekly update: save catalogue: " + err.Error())
		return
	}
	added := newModels(previous, current)

	users, err := model.RetentionRecipients(week)
	if err != nil {
		common.SysError("weekly update: recipients: " + err.Error())
		return
	}
	sent, failed := 0, 0
	since := time.Now().Add(-7 * 24 * time.Hour).Unix()
	for i := range users {
		u := &users[i]
		claimed, err := model.ClaimRetentionSend(week, u.Id)
		if err != nil || !claimed {
			continue
		}
		msg, err := buildRetentionEmail(u, since, len(current), added)
		if err == nil {
			err = SendBrandedEmail(retentionPurpose, u.Email, msg)
		}
		if err != nil {
			failed++
			model.RecordRetentionSendError(week, u.Id, err)
			common.SysError(fmt.Sprintf("weekly update: user %d: %s", u.Id, err.Error()))
		} else {
			sent++
		}
		time.Sleep(retentionSendGap)
	}
	if sent+failed > 0 {
		common.SysLog(fmt.Sprintf("weekly update %s: sent %d, failed %d, new models %d", week, sent, failed, len(added)))
	}
}

// newModels is current minus previous. With no previous week there is no
// baseline, so nothing is "new" -- the first email does not announce the
// whole catalogue as this week's news.
func newModels(previous, current []string) []string {
	if previous == nil {
		return nil
	}
	seen := make(map[string]bool, len(previous))
	for _, m := range previous {
		seen[m] = true
	}
	var added []string
	for _, m := range current {
		if !seen[m] {
			added = append(added, m)
		}
	}
	return added
}

func buildRetentionEmail(u *model.User, since int64, catalogueSize int, added []string) (common.EmailMessage, error) {
	balance, err := model.GetUserQuota(u.Id, true) // the tenant wallet when there is one
	if err != nil {
		return common.EmailMessage{}, err
	}
	requests, spent, err := model.RetentionActivity(u.Id, since)
	if err != nil {
		return common.EmailMessage{}, err
	}
	console := strings.TrimRight(system_setting.ServerAddress, "/")
	name := u.DisplayName
	if name == "" {
		name = u.Username
	}

	msg := common.EmailMessage{
		Eyebrow:        "Weekly update",
		CTALabel:       "Open your console",
		CTAURL:         console,
		FooterReason:   "You get this weekly update because you have a Unify API account.",
		UnsubscribeURL: RetentionUnsubscribeURL(u.Id),
	}
	if u.RequestCount == 0 {
		// Never called the API: the one thing worth saying is how to start.
		msg.Subject = "Your Unify API account is ready"
		msg.Preheader = fmt.Sprintf("%d models behind one OpenAI-compatible endpoint.", catalogueSize)
		msg.Headline = "Make your first call in two minutes"
		msg.Intro = fmt.Sprintf("Hi %s, any OpenAI SDK works with Unify API: create a key in your console, set the base URL below, and pick any of the %d models.", name, catalogueSize)
		msg.Details = []common.EmailDetail{
			{Label: "Base URL", Value: RetentionAPIBase},
			{Label: "Balance", Value: retentionMoney(int64(balance))},
		}
	} else {
		msg.Subject = "Your week on Unify API"
		msg.Preheader = fmt.Sprintf("%d requests in the last 7 days.", requests)
		msg.Headline = "Your week on Unify API"
		msg.Intro = fmt.Sprintf("Hi %s, here is your account over the last 7 days.", name)
		if requests == 0 {
			// A quiet week: say so plainly rather than headline a row of zeros.
			msg.Preheader = "Your key and balance are ready when you are."
			msg.Headline = "Your Unify API account"
			msg.Intro = fmt.Sprintf("Hi %s, no requests in the last 7 days. Your key and balance are ready when you are.", name)
		}
		msg.Details = []common.EmailDetail{
			{Label: "Requests (7 days)", Value: strconv.FormatInt(requests, 10)},
			{Label: "Spend (7 days)", Value: retentionMoney(spent)},
			{Label: "Balance", Value: retentionMoney(int64(balance))},
		}
	}
	if len(added) > 0 {
		msg.Details = append(msg.Details, common.EmailDetail{Label: "New this week", Value: modelList(added)})
		if u.RequestCount > 0 {
			msg.Preheader = fmt.Sprintf("%d new models this week.", len(added))
		}
	}
	return msg, nil
}

// retentionMoney is USD to the cent. The console's own formatter prints six
// decimals ("$2.000000"), which reads as a bug in an email.
func retentionMoney(quota int64) string {
	usd := float64(quota) / common.QuotaPerUnit
	if usd > 0 && usd < 0.01 {
		return "< $0.01"
	}
	return fmt.Sprintf("$%.2f", usd)
}

func modelList(models []string) string {
	if len(models) <= retentionNewModels {
		return strings.Join(models, ", ")
	}
	return strings.Join(models[:retentionNewModels], ", ") + fmt.Sprintf(" and %d more", len(models)-retentionNewModels)
}

// RetentionUnsubscribeToken signs a user id so the link works without signing
// in and cannot be forged for someone else. Keyed by CryptoSecret (falls back
// to SESSION_SECRET, which production sets), so links survive restarts.
func RetentionUnsubscribeToken(userId int) string {
	h := hmac.New(sha256.New, []byte(common.CryptoSecret))
	h.Write([]byte("retention-unsubscribe:" + strconv.Itoa(userId)))
	return hex.EncodeToString(h.Sum(nil))[:32]
}

func VerifyRetentionUnsubscribeToken(userId int, token string) bool {
	return userId > 0 && hmac.Equal([]byte(RetentionUnsubscribeToken(userId)), []byte(token))
}

func RetentionUnsubscribeURL(userId int) string {
	base := strings.TrimRight(system_setting.ServerAddress, "/")
	q := url.Values{"u": {strconv.Itoa(userId)}, "t": {RetentionUnsubscribeToken(userId)}}
	return base + "/api/email/unsubscribe?" + q.Encode()
}
