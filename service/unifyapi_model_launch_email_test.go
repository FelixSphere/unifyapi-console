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
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

// The GPT-6.2 Sol expression exactly as production carries it (2026-11-18):
// $2 / $10 / $0.10 / $2.50 per 1M, doubled input above 272K.
const gpt62SolExpr = `len <= 272000 ? tier("standard", p*2 + c*10 + cr*0.1 + cc*2.5) : tier("long_context", p*4 + c*15 + cr*0.2 + cc*5)`

func setupModelLaunchTest(t *testing.T) *capturingSMTP {
	t.Helper()
	smtp := setupRetentionTest(t)
	require.NoError(t, model.MigrateModelLaunchEmail())
	t.Setenv(retentionEmailEnv, "on")
	return smtp
}

func enableModel(t *testing.T, name string) {
	t.Helper()
	require.NoError(t, model.DB.Create(&model.Ability{Group: "default", Model: name, ChannelId: 1, Enabled: true}).Error)
}

func waitForLaunch(t *testing.T, id int) model.ModelLaunchEmail {
	t.Helper()
	var l model.ModelLaunchEmail
	require.Eventually(t, func() bool {
		require.NoError(t, model.DB.First(&l, id).Error)
		return l.FinishedAt > 0
	}, 10*time.Second, 20*time.Millisecond)
	return l
}

func TestLaunchPriceComesFromTheCatalogue(t *testing.T) {
	// Operator-decided price, 2026-11-16: deepseek-v4-flash at $0.30 / $1.20 / $0.006.
	p, ok := LookupModelLaunchPrice("deepseek-v4-flash")
	require.True(t, ok)
	require.InDelta(t, 0.30, p.InputUSD, 1e-9)
	require.InDelta(t, 1.20, p.OutputUSD, 1e-9)
	require.InDelta(t, 0.006, p.CacheReadUSD, 1e-9)
	require.Equal(t, "$0.30 input · $1.20 output · $0.006 cached input per 1M tokens (list price)", launchPriceLine(p))
}

func TestLaunchPriceComesFromAnExpressionWithItsTier(t *testing.T) {
	billing_setting.SetBillingExprForTest("test-gpt-6.2-sol", gpt62SolExpr)
	p, ok := LookupModelLaunchPrice("test-gpt-6.2-sol")
	require.True(t, ok)
	require.InDelta(t, 2, p.InputUSD, 1e-9)
	require.InDelta(t, 10, p.OutputUSD, 1e-9)
	require.InDelta(t, 0.10, p.CacheReadUSD, 1e-9)
	require.Equal(t, 272000, p.LongContextAbove, "the tier boundary is found from the expression itself")
	require.Equal(t, "$2 input · $10 output · $0.10 cached input per 1M tokens (list price); prompts over 272K tokens bill at a higher tier", launchPriceLine(p))
}

func TestAnUnpricedModelHasNoLaunchPrice(t *testing.T) {
	_, ok := LookupModelLaunchPrice("no-such-model-anywhere")
	require.False(t, ok)
}

func TestNothingIsSentUnlessTheProductionSwitchIsOn(t *testing.T) {
	smtp := setupModelLaunchTest(t)
	t.Setenv(retentionEmailEnv, "") // staging: real customer addresses, switch off
	enableModel(t, "deepseek-v4-flash")
	addRetentionUser(t, "c", "c@example.com", common.RoleCommonUser, common.UserStatusEnabled, 0, 0)

	_, err := StartModelLaunchEmail([]string{"deepseek-v4-flash"}, "", 1)
	require.ErrorIs(t, err, ErrModelLaunchDisabled)
	time.Sleep(100 * time.Millisecond)
	require.Empty(t, smtp.sent())
	var n int64
	model.DB.Model(&model.ModelLaunchEmail{}).Count(&n)
	require.Zero(t, n, "a refused send leaves no record")
}

func TestTheAnnouncementGoesOnlyToCustomersWhoCanReceiveIt(t *testing.T) {
	smtp := setupModelLaunchTest(t)
	enableModel(t, "deepseek-v4-flash")
	c := addRetentionUser(t, "cust", "cust@example.com", common.RoleCommonUser, common.UserStatusEnabled, 3, 0)
	addRetentionUser(t, "disabled", "disabled@example.com", common.RoleCommonUser, common.UserStatusDisabled, 0, 0)
	addRetentionUser(t, "staff", "staff@example.com", common.RoleAdminUser, common.UserStatusEnabled, 0, 0)
	addRetentionUser(t, "noemail", "", common.RoleCommonUser, common.UserStatusEnabled, 0, 0)
	gone := addRetentionUser(t, "gone", "gone@example.com", common.RoleCommonUser, common.UserStatusEnabled, 0, 0)
	require.NoError(t, model.UnsubscribeRetentionEmail(gone.Id), "the weekly update's opt-out covers this email too")

	n, err := model.CountMarketingRecipients()
	require.NoError(t, err)
	require.EqualValues(t, 1, n, "the preview's count is the audience that will be mailed")

	launch, err := StartModelLaunchEmail([]string{"deepseek-v4-flash"}, "", 1)
	require.NoError(t, err)
	done := waitForLaunch(t, launch.Id)
	require.Equal(t, 1, done.Sent)
	require.Equal(t, 0, done.Failed)

	sent := smtp.sent()
	require.Equal(t, []string{"cust@example.com"}, keys(sent))
	raw := sent["cust@example.com"]
	require.Contains(t, raw, "New on Unify API: deepseek-v4-flash")
	link := "https://app.unifyapi.ai/api/email/unsubscribe?t=" + RetentionUnsubscribeToken(c.Id) + "&u=" + strconv.Itoa(c.Id)
	require.Contains(t, raw, "List-Unsubscribe: <"+link+">\r\n", "one-click opt-out, the recipient's own link")
	require.Contains(t, raw, "6 Karen Ct, CA 94010, United States", "CAN-SPAM: a physical postal address")
}

func TestAModelIsAnnouncedOnce(t *testing.T) {
	smtp := setupModelLaunchTest(t)
	enableModel(t, "deepseek-v4-flash")
	addRetentionUser(t, "cust", "cust@example.com", common.RoleCommonUser, common.UserStatusEnabled, 0, 0)

	first, err := StartModelLaunchEmail([]string{"deepseek-v4-flash"}, "", 1)
	require.NoError(t, err)
	waitForLaunch(t, first.Id)
	require.Len(t, smtp.sent(), 1)

	_, err = StartModelLaunchEmail([]string{"deepseek-v4-flash"}, "", 1) // a double click
	require.ErrorContains(t, err, "already announced")
	_, err = PreviewModelLaunchEmail([]string{"deepseek-v4-flash"}, "")
	require.ErrorContains(t, err, "already announced")
}

func TestOnlyCallableAndPricedModelsCanBeAnnounced(t *testing.T) {
	setupModelLaunchTest(t)
	require.NoError(t, model.DB.Create(&model.Ability{Group: "default", Model: "deepseek-v3", ChannelId: 1, Enabled: false}).Error)
	enableModel(t, "unpriced-model")

	_, err := PreviewModelLaunchEmail([]string{"deepseek-v3"}, "")
	require.ErrorContains(t, err, "not enabled", "a delisted model must not be advertised")
	_, err = PreviewModelLaunchEmail([]string{"unpriced-model"}, "")
	require.ErrorContains(t, err, "no price", "the relay refuses an unpriced model")
	_, err = PreviewModelLaunchEmail(nil, "")
	require.Error(t, err)
}

func TestThePreviewIsTheEmailThatWillBeSent(t *testing.T) {
	setupModelLaunchTest(t)
	enableModel(t, "deepseek-v4-flash")
	addRetentionUser(t, "cust", "cust@example.com", common.RoleCommonUser, common.UserStatusEnabled, 0, 0)

	p, err := PreviewModelLaunchEmail([]string{"deepseek-v4-flash"}, "Our cheapest DeepSeek is now cheaper.")
	require.NoError(t, err)
	require.True(t, p.Enabled)
	require.EqualValues(t, 1, p.Recipients)
	require.Equal(t, "New on Unify API: deepseek-v4-flash", p.Subject)
	require.Contains(t, p.Text, "Our cheapest DeepSeek is now cheaper.", "the operator's note replaces the default intro")
	require.Contains(t, p.Text, "$0.30 input · $1.20 output")
	require.Contains(t, p.Text, RetentionAPIBase)
}

func TestCandidatesAreModelsTheWeeklyUpdateHasNotToldAnyone(t *testing.T) {
	setupModelLaunchTest(t)
	enableModel(t, "deepseek-v4-flash")
	enableModel(t, "old-model")

	got, err := ModelLaunchCandidates()
	require.NoError(t, err)
	require.Empty(t, got, "no weekly baseline yet: nothing is called new")

	_, err = model.SaveRetentionWeekModels("2026-W47", []string{"old-model"})
	require.NoError(t, err)
	got, err = ModelLaunchCandidates()
	require.NoError(t, err)
	require.Equal(t, []string{"deepseek-v4-flash"}, got)

	launch, err := model.CreateModelLaunchEmail([]string{"deepseek-v4-flash"}, "", 1)
	require.NoError(t, err)
	got, err = ModelLaunchCandidates()
	require.NoError(t, err)
	require.Equal(t, []string{"deepseek-v4-flash"}, got, "a launch that reached nobody announced nothing")

	claimed, err := model.ClaimModelLaunchSend(launch.Id, 42)
	require.NoError(t, err)
	require.True(t, claimed)
	got, err = ModelLaunchCandidates()
	require.NoError(t, err)
	require.Empty(t, got, "an announced model is no longer a candidate")
}

// Found on staging during the v0.1.182 release: with SMTP failing, the send
// recorded 0 sent / 1 failed and the model could never be announced again.
func TestAFailedAnnouncementCanBeSentAgain(t *testing.T) {
	smtp := setupModelLaunchTest(t)
	enableModel(t, "deepseek-v4-flash")
	addRetentionUser(t, "cust", "cust@example.com", common.RoleCommonUser, common.UserStatusEnabled, 0, 0)

	goodPort := common.SMTPPort
	common.SMTPPort = 1 // nothing listens: every send fails
	failed, err := StartModelLaunchEmail([]string{"deepseek-v4-flash"}, "", 1)
	require.NoError(t, err)
	done := waitForLaunch(t, failed.Id)
	require.Equal(t, 0, done.Sent)
	require.Equal(t, 1, done.Failed)
	require.Empty(t, smtp.sent())

	common.SMTPPort = goodPort // mail is back
	retry, err := StartModelLaunchEmail([]string{"deepseek-v4-flash"}, "", 1)
	require.NoError(t, err, "nobody was told, so the model is not announced yet")
	done = waitForLaunch(t, retry.Id)
	require.Equal(t, 1, done.Sent)
	require.Equal(t, []string{"cust@example.com"}, keys(smtp.sent()))

	_, err = StartModelLaunchEmail([]string{"deepseek-v4-flash"}, "", 1)
	require.ErrorContains(t, err, "already announced", "once delivered, it is announced")
}

// The catalogue automation queues a launch by inserting the row; the worker
// sends it once the cancel window has passed (operator decision 2026-11-19).
func queueLaunch(t *testing.T, models string, age time.Duration) model.ModelLaunchEmail {
	t.Helper()
	l := model.ModelLaunchEmail{Models: models, Queued: true, CreatedAt: time.Now().Add(-age).Unix()}
	require.NoError(t, model.DB.Create(&l).Error)
	return l
}

func TestAQueuedLaunchIsSentAfterTheCancelWindow(t *testing.T) {
	smtp := setupModelLaunchTest(t)
	enableModel(t, "deepseek-v4-flash")
	addRetentionUser(t, "cust", "cust@example.com", common.RoleCommonUser, common.UserStatusEnabled, 0, 0)

	fresh := queueLaunch(t, "deepseek-v4-flash", time.Minute) // younger than the window
	RunQueuedModelLaunches()
	require.Empty(t, smtp.sent(), "inside the cancel window nothing is sent")
	var row model.ModelLaunchEmail
	require.NoError(t, model.DB.First(&row, fresh.Id).Error)
	require.True(t, row.Queued)

	require.NoError(t, model.DB.Model(&row).Update("created_at", time.Now().Add(-ModelLaunchQueueDelay-time.Second).Unix()).Error)
	RunQueuedModelLaunches()
	require.Equal(t, []string{"cust@example.com"}, keys(smtp.sent()))
	require.NoError(t, model.DB.First(&row, fresh.Id).Error)
	require.False(t, row.Queued)
	require.Equal(t, 1, row.Sent)
	require.NotZero(t, row.FinishedAt)

	smtp.mu.Lock()
	smtp.mail = map[string]string{}
	smtp.mu.Unlock()
	RunQueuedModelLaunches() // a later tick
	require.Empty(t, smtp.sent(), "a sent launch is not sent again")
}

func TestADeletedQueuedLaunchIsNeverSent(t *testing.T) {
	smtp := setupModelLaunchTest(t)
	enableModel(t, "deepseek-v4-flash")
	addRetentionUser(t, "cust", "cust@example.com", common.RoleCommonUser, common.UserStatusEnabled, 0, 0)

	l := queueLaunch(t, "deepseek-v4-flash", time.Minute)
	require.NoError(t, model.DB.Delete(&model.ModelLaunchEmail{}, l.Id).Error) // the cancel
	RunQueuedModelLaunches()
	require.Empty(t, smtp.sent())
}

func TestAQueuedLaunchIsValidatedLikeAnOperatorSend(t *testing.T) {
	smtp := setupModelLaunchTest(t)
	enableModel(t, "unpriced-model")
	require.NoError(t, model.DB.Create(&model.Ability{Group: "default", Model: "deepseek-v3", ChannelId: 1, Enabled: false}).Error)
	addRetentionUser(t, "cust", "cust@example.com", common.RoleCommonUser, common.UserStatusEnabled, 0, 0)

	unpriced := queueLaunch(t, "unpriced-model", time.Hour)
	delisted := queueLaunch(t, "deepseek-v3", time.Hour)
	RunQueuedModelLaunches()

	require.Empty(t, smtp.sent(), "neither may be advertised")
	var row model.ModelLaunchEmail
	require.NoError(t, model.DB.First(&row, unpriced.Id).Error)
	require.Contains(t, row.Error, "no price")
	require.False(t, row.Queued)
	require.NotZero(t, row.FinishedAt)
	var second model.ModelLaunchEmail
	require.NoError(t, model.DB.First(&second, delisted.Id).Error)
	require.Contains(t, second.Error, "not enabled")
}

func TestQueuedLaunchesWaitWhileTheSwitchIsOff(t *testing.T) {
	smtp := setupModelLaunchTest(t)
	t.Setenv(retentionEmailEnv, "") // staging
	enableModel(t, "deepseek-v4-flash")
	addRetentionUser(t, "cust", "cust@example.com", common.RoleCommonUser, common.UserStatusEnabled, 0, 0)

	l := queueLaunch(t, "deepseek-v4-flash", time.Hour)
	RunQueuedModelLaunches()
	require.Empty(t, smtp.sent())
	var row model.ModelLaunchEmail
	require.NoError(t, model.DB.First(&row, l.Id).Error)
	require.True(t, row.Queued, "left untouched, not refused")
}

// The announcement shows each customer what THEY pay. Operator 2026-11-21:
// CVP editions sell at 1.10x and Mythos at 1.15x of list in every group, so a
// card quoting list price alone would announce a price nobody is charged.
func TestTheCardQuotesTheRecipientsOwnPrice(t *testing.T) {
	setupModelLaunchTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Model{}))
	prevGR, prevGMD := ratio_setting.GroupRatio2JSONString(), ratio_setting.GroupModelDiscount2JSONString()
	t.Cleanup(func() {
		_ = ratio_setting.UpdateGroupRatioByJSONString(prevGR)
		_ = ratio_setting.UpdateGroupModelDiscountByJSONString(prevGMD)
	})
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":0.85,"Kingdee":1}`))
	require.NoError(t, ratio_setting.UpdateGroupModelDiscountByJSONString(`{"Kingdee":{"deepseek-v4-flash":1.1}}`))
	p, ok := LookupModelLaunchPrice("deepseek-v4-flash") // $0.30 / $1.20 / $0.006
	require.True(t, ok)

	marked := BuildModelLaunchEmail([]ModelLaunchPrice{p}, "", 7, "Kingdee")
	require.Len(t, marked.Cards, 1)
	require.Contains(t, marked.Cards[0].Rows, common.EmailDetail{Label: "Your price / 1M tokens", Value: "$0.33 input · $1.32 output"}, "list x the group's per-model 1.1")
	require.Contains(t, marked.Cards[0].Rows, common.EmailDetail{Label: "List price / 1M", Value: "$0.30 input · $1.20 output"})

	discounted := BuildModelLaunchEmail([]ModelLaunchPrice{p}, "", 7, "default")
	require.Contains(t, discounted.Cards[0].Rows, common.EmailDetail{Label: "Your price / 1M tokens", Value: "$0.255 input · $1.02 output"}, "list x the group ratio when there is no per-model ratio")
}

func TestCardsCarryTheTagsModelSquareShows(t *testing.T) {
	setupModelLaunchTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Model{}))
	require.NoError(t, model.DB.Create(&model.Model{ModelName: "-cvp", Tags: "CVP", NameRule: model.NameRuleSuffix, Status: 1}).Error)
	require.NoError(t, model.DB.Create(&model.Model{ModelName: "claude-mythos-", Tags: "CVP,Mythos", NameRule: model.NameRulePrefix, Status: 1}).Error)
	tags := launchModelTags()
	require.Equal(t, "CVP", tags("claude-opus-5-5-cvp"))
	require.Equal(t, "CVP · Mythos", tags("claude-mythos-5-2"))
	require.Equal(t, "", tags("deepseek-v4-flash"))
	require.Equal(t, "Claude Mythos and CVP editions of Claude, for verified defensive security work.",
		launchHeroLine([]ModelLaunchPrice{{Model: "claude-mythos-5-2"}, {Model: "claude-opus-5-5-cvp"}}))
}

func TestTheAnnouncementRisesInAndAccountMailDoesNot(t *testing.T) {
	setupModelLaunchTest(t)
	p, ok := LookupModelLaunchPrice("deepseek-v4-flash")
	require.True(t, ok)
	html, text, err := common.RenderEmail(UnifyAPIEmailBrand(), BuildModelLaunchEmail([]ModelLaunchPrice{p, p}, "", 0, "default"))
	require.NoError(t, err)
	require.Contains(t, html, "@keyframes u-rise")
	require.Contains(t, html, "prefers-reduced-motion")
	require.Contains(t, html, "animation-delay:0.18s")
	require.Contains(t, html, "animation-delay:0.29s", "cards are staggered")
	require.Contains(t, text, "NEW ON UNIFY API")

	account, _, err := common.RenderEmail(UnifyAPIEmailBrand(), common.EmailMessage{Subject: "Reset", Headline: "Reset your password", Code: "123456"})
	require.NoError(t, err)
	require.NotContains(t, account, `class="u-rise"`, "account mail renders exactly as before")
	require.Contains(t, account, "Reset your password")
}
