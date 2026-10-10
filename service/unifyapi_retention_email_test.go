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

import (
	"bufio"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// capturingSMTP accepts every message on a plain connection and keeps
// (recipient, raw message) pairs, so a test reads what a customer would get.
type capturingSMTP struct {
	mu   sync.Mutex
	mail map[string]string
	port int
}

func newCapturingSMTP(t *testing.T) *capturingSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	s := &capturingSMTP{mail: map[string]string{}, port: ln.Addr().(*net.TCPAddr).Port}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handle(conn)
		}
	}()
	return s
}

func (s *capturingSMTP) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	say := func(l string) { _, _ = rw.WriteString(l + "\r\n"); _ = rw.Flush() }
	say("220 capture")
	var rcpt string
	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			say("250 capture")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			rcpt = strings.Trim(strings.TrimSpace(line)[len("RCPT TO:"):], "<>")
			say("250 ok")
		case cmd == "DATA":
			say("354 go")
			var b strings.Builder
			for {
				l, err := rw.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			s.mu.Lock()
			s.mail[rcpt] = b.String()
			s.mu.Unlock()
			say("250 queued")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func (s *capturingSMTP) sent() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.mail))
	for k, v := range s.mail {
		out[k] = v
	}
	return out
}

func setupRetentionTest(t *testing.T) *capturingSMTP {
	t.Helper()
	// A database of its own: the recipient query reads every user, so rows
	// other tests in this package leave behind would become recipients.
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	prevDB, prevLogDB := model.DB, model.LOG_DB
	model.DB, model.LOG_DB = db, db
	t.Cleanup(func() { model.DB, model.LOG_DB = prevDB, prevLogDB })
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Tenant{}, &model.Ability{}, &model.Log{}))
	require.NoError(t, model.MigrateRetentionEmail())

	smtp := newCapturingSMTP(t)
	prev := struct {
		server, from, account, token, secret, addr string
		port                                       int
		ssl, starttls                              bool
	}{common.SMTPServer, common.SMTPFrom, common.SMTPAccount, common.SMTPToken, common.CryptoSecret, system_setting.ServerAddress, common.SMTPPort, common.SMTPSSLEnabled, common.SMTPStartTLSEnabled}
	t.Cleanup(func() {
		common.SMTPServer, common.SMTPFrom, common.SMTPAccount, common.SMTPToken = prev.server, prev.from, prev.account, prev.token
		common.CryptoSecret, system_setting.ServerAddress, common.SMTPPort = prev.secret, prev.addr, prev.port
		common.SMTPSSLEnabled, common.SMTPStartTLSEnabled = prev.ssl, prev.starttls
	})
	common.SMTPServer, common.SMTPPort = "127.0.0.1", smtp.port
	common.SMTPFrom, common.SMTPAccount, common.SMTPToken = "noreply@unifyapi.ai", "", ""
	common.SMTPSSLEnabled, common.SMTPStartTLSEnabled = false, false
	common.CryptoSecret = "retention-test-secret"
	system_setting.ServerAddress = "https://app.unifyapi.ai"
	return smtp
}

func addRetentionUser(t *testing.T, name, email string, role, status, requests, quota int) *model.User {
	t.Helper()
	u := &model.User{Username: name, DisplayName: name, Email: email, Role: role, Status: status,
		RequestCount: requests, Quota: quota, AffCode: "aff-" + name, Password: "x"}
	require.NoError(t, model.DB.Create(u).Error)
	return u
}

func TestRetentionWeekIsDueFromTuesdayTwoUTCUntilSunday(t *testing.T) {
	cases := []struct {
		at   string
		week string
		due  bool
	}{
		{"2026-10-05T23:00:00Z", "2026-W41", false}, // Monday
		{"2026-10-06T01:59:00Z", "2026-W41", false}, // Tuesday, before 02:00
		{"2026-10-06T02:00:00Z", "2026-W41", true},
		{"2026-10-09T12:00:00Z", "2026-W41", true}, // a Friday release still sends
		{"2026-10-11T23:59:00Z", "2026-W41", true}, // Sunday
		{"2026-10-12T00:00:00Z", "2026-W42", false},
	}
	for _, c := range cases {
		at, err := time.Parse(time.RFC3339, c.at)
		require.NoError(t, err)
		week, due := retentionWeekDue(at)
		require.Equal(t, c.week, week, c.at)
		require.Equal(t, c.due, due, c.at)
	}
}

func TestOffUnlessTheEnvironmentSaysOn(t *testing.T) {
	t.Setenv(retentionEmailEnv, "")
	require.False(t, RetentionEmailEnabled())
	t.Setenv(retentionEmailEnv, "true") // only the exact word turns it on
	require.False(t, RetentionEmailEnabled())
	t.Setenv(retentionEmailEnv, "on")
	require.True(t, RetentionEmailEnabled())
}

func TestUnsubscribeTokenIsPerUserAndUnforgeable(t *testing.T) {
	setupRetentionTest(t)
	tok := RetentionUnsubscribeToken(7)
	require.True(t, VerifyRetentionUnsubscribeToken(7, tok))
	require.False(t, VerifyRetentionUnsubscribeToken(8, tok), "one user's link must not unsubscribe another")
	require.False(t, VerifyRetentionUnsubscribeToken(7, ""))
	require.False(t, VerifyRetentionUnsubscribeToken(0, RetentionUnsubscribeToken(0)))
	common.CryptoSecret = "a-different-deployment"
	require.False(t, VerifyRetentionUnsubscribeToken(7, tok))
}

func TestWeeklyUpdateGoesOnlyToCustomersWhoCanReceiveIt(t *testing.T) {
	smtp := setupRetentionTest(t)
	addRetentionUser(t, "active", "active@example.com", common.RoleCommonUser, common.UserStatusEnabled, 12, 0)
	addRetentionUser(t, "fresh", "fresh@example.com", common.RoleCommonUser, common.UserStatusEnabled, 0, 0)
	addRetentionUser(t, "disabled", "disabled@example.com", common.RoleCommonUser, common.UserStatusDisabled, 0, 0)
	addRetentionUser(t, "staff", "staff@example.com", common.RoleAdminUser, common.UserStatusEnabled, 0, 0)
	addRetentionUser(t, "noemail", "", common.RoleCommonUser, common.UserStatusEnabled, 0, 0)
	gone := addRetentionUser(t, "gone", "gone@example.com", common.RoleCommonUser, common.UserStatusEnabled, 0, 0)
	require.NoError(t, model.UnsubscribeRetentionEmail(gone.Id))

	RunRetentionEmailWeek("2026-W41")

	sent := smtp.sent()
	require.ElementsMatch(t, []string{"active@example.com", "fresh@example.com"}, keys(sent))
}

func TestARerunInTheSameWeekSendsNothingTwice(t *testing.T) {
	smtp := setupRetentionTest(t)
	addRetentionUser(t, "once", "once@example.com", common.RoleCommonUser, common.UserStatusEnabled, 0, 0)

	RunRetentionEmailWeek("2026-W41")
	require.Len(t, smtp.sent(), 1)
	smtp.mu.Lock()
	smtp.mail = map[string]string{}
	smtp.mu.Unlock()

	RunRetentionEmailWeek("2026-W41") // e.g. the container restarted on Wednesday
	require.Empty(t, smtp.sent())

	RunRetentionEmailWeek("2026-W42")
	require.Len(t, smtp.sent(), 1, "next week sends again")
}

func TestTheEmailCarriesOneClickUnsubscribeAndTheCompanyAddress(t *testing.T) {
	smtp := setupRetentionTest(t)
	u := addRetentionUser(t, "hdr", "hdr@example.com", common.RoleCommonUser, common.UserStatusEnabled, 0, 0)

	RunRetentionEmailWeek("2026-W41")

	raw := smtp.sent()["hdr@example.com"]
	link := "https://app.unifyapi.ai/api/email/unsubscribe?t=" + RetentionUnsubscribeToken(u.Id) + "&u=" + strconv.Itoa(u.Id)
	require.Contains(t, raw, "List-Unsubscribe: <"+link+">\r\n")
	require.Contains(t, raw, "List-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n")
	require.Contains(t, raw, "Unsubscribe: "+link, "the text part carries the link")
	require.Contains(t, raw, "Petaling Jaya", "CAN-SPAM: a physical postal address")
}

func TestNeverCalledCustomersGetTheQuickStart(t *testing.T) {
	setupRetentionTest(t)
	require.NoError(t, model.DB.Create(&model.Ability{Group: "default", Model: "gpt-4o", ChannelId: 1, Enabled: true}).Error)
	require.NoError(t, model.DB.Create(&model.Ability{Group: "default", Model: "claude-x", ChannelId: 1, Enabled: true}).Error)
	require.NoError(t, model.DB.Create(&model.Ability{Group: "default", Model: "retired", ChannelId: 2, Enabled: false}).Error)
	fresh := addRetentionUser(t, "fresh", "fresh@example.com", common.RoleCommonUser, common.UserStatusEnabled, 0, int(common.QuotaPerUnit*2))

	names, err := model.EnabledModelNames()
	require.NoError(t, err)
	require.Equal(t, []string{"claude-x", "gpt-4o"}, names, "disabled abilities are not advertised")

	msg, err := buildRetentionEmail(fresh, 0, len(names), nil)
	require.NoError(t, err)
	require.Equal(t, "Your Unify API account is ready", msg.Subject)
	require.Contains(t, msg.Intro, "2 models")
	require.Contains(t, msg.Details, common.EmailDetail{Label: "Base URL", Value: RetentionAPIBase})
}

func TestActiveCustomersGetTheirOwnLastSevenDays(t *testing.T) {
	setupRetentionTest(t)
	u := addRetentionUser(t, "busy", "busy@example.com", common.RoleCommonUser, common.UserStatusEnabled, 40, 0)
	other := addRetentionUser(t, "other", "other@example.com", common.RoleCommonUser, common.UserStatusEnabled, 40, 0)
	now := time.Now().Unix()
	since := now - 7*86400
	logs := []model.Log{
		{UserId: u.Id, Type: model.LogTypeConsume, Quota: 1000, CreatedAt: now - 3600},
		{UserId: u.Id, Type: model.LogTypeConsume, Quota: 500, CreatedAt: now - 86400},
		{UserId: u.Id, Type: model.LogTypeConsume, Quota: 9999, CreatedAt: since - 10}, // older than the window
		{UserId: u.Id, Type: model.LogTypeTopup, Quota: 7777, CreatedAt: now - 60},     // not a call
		{UserId: other.Id, Type: model.LogTypeConsume, Quota: 4242, CreatedAt: now - 60},
	}
	require.NoError(t, model.LOG_DB.Create(&logs).Error)

	requests, quota, err := model.RetentionActivity(u.Id, since)
	require.NoError(t, err)
	require.EqualValues(t, 2, requests)
	require.EqualValues(t, 1500, quota)

	msg, err := buildRetentionEmail(u, since, 2, nil)
	require.NoError(t, err)
	require.Equal(t, "Your week on Unify API", msg.Subject)
	require.Contains(t, msg.Details, common.EmailDetail{Label: "Requests (7 days)", Value: "2"})
	require.Contains(t, msg.Details, common.EmailDetail{Label: "Spend (7 days)", Value: retentionMoney(1500)})

	quiet, err := buildRetentionEmail(other, now+1, 2, nil) // nothing after now
	require.NoError(t, err)
	require.Equal(t, "Your Unify API account", quiet.Headline, "a quiet week is not headlined as a row of zeros")
}

func TestMoneyIsShownToTheCent(t *testing.T) {
	require.Equal(t, "$2.00", retentionMoney(int64(common.QuotaPerUnit*2)))
	require.Equal(t, "$13.40", retentionMoney(int64(common.QuotaPerUnit*13.4)))
	require.Equal(t, "$0.00", retentionMoney(0))
	require.Equal(t, "< $0.01", retentionMoney(1), "a real but tiny spend is not shown as zero")
}

func TestNewThisWeekIsADiffOfObservedCataloguesNotTheWholeList(t *testing.T) {
	setupRetentionTest(t)
	prev, err := model.SaveRetentionWeekModels("2026-W41", []string{"a", "b"})
	require.NoError(t, err)
	require.Nil(t, prev)
	require.Empty(t, newModels(prev, []string{"a", "b"}), "the first week has no baseline, so nothing is new")

	prev, err = model.SaveRetentionWeekModels("2026-W42", []string{"a", "b", "c"})
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, prev)
	require.Equal(t, []string{"c"}, newModels(prev, []string{"a", "b", "c"}))

	// A rerun later in W42 does not move W42's baseline.
	_, err = model.SaveRetentionWeekModels("2026-W42", []string{"a"})
	require.NoError(t, err)
	prev, err = model.SaveRetentionWeekModels("2026-W43", []string{"a", "b", "c", "d"})
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b", "c"}, prev)

	require.Equal(t, "m1, m2, m3, m4, m5, m6, m7, m8 and 2 more",
		modelList([]string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8", "m9", "m10"}))
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
