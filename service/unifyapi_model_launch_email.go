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

// UNIFYAPI-FORK: the new-model announcement email (operator decision
// 2026-11-18). When a model is listed, an operator previews one email naming
// it and its price and presses Send in /ops; every customer who gets the
// weekly update gets it. Nothing goes out without that press.
//
// Gated on the SAME switch as the weekly update, UNIFYAPI_RETENTION_EMAIL=on,
// for the same reason: staging runs on a copy of production's customer
// addresses, and that variable is set on production only.
//
// The prices in the email are read from what the relay bills at send time --
// the compiled catalogue, or the model's billing expression -- so the email
// cannot quote a price the bill does not charge. The operator's own words go
// in an optional note, which the operator sees in the preview before sending.

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

const (
	modelLaunchPurpose   = "model-launch"
	modelLaunchMaxModels = 8
	modelLaunchMaxNote   = 600
)

var (
	ErrModelLaunchDisabled = errors.New("announcement email is off on this deployment (UNIFYAPI_RETENTION_EMAIL is not on, or SMTP is not configured)")
	ErrModelLaunchRunning  = errors.New("an announcement is still sending; wait for it to finish")

	modelLaunchMu      sync.Mutex
	modelLaunchRunning bool
)

// ModelLaunchEmailEnabled reports whether this deployment may send it.
func ModelLaunchEmailEnabled() bool {
	return RetentionEmailEnabled() && common.SMTPServer != ""
}

// ModelLaunchPrice is one model's price as the relay bills it, in USD.
type ModelLaunchPrice struct {
	Model        string  `json:"model"`
	InputUSD     float64 `json:"input_usd"`      // per 1M tokens
	OutputUSD    float64 `json:"output_usd"`     // per 1M tokens
	CacheReadUSD float64 `json:"cache_read_usd"` // per 1M tokens; 0 = not offered
	PerCallUSD   float64 `json:"per_call_usd"`
	PriceUnit    string  `json:"price_unit"` // "second" or "" (per request), with PerCallUSD
	// LongContextAbove is the prompt length above which a dearer tier
	// applies, or 0 when there is none.
	LongContextAbove int `json:"long_context_above"`
}

// LookupModelLaunchPrice reads a model's list price from the catalogue (which
// includes ExtraModelPricing) or, for a model priced only by a billing
// expression, by evaluating that expression on one million tokens of each
// kind. False when the model has no price, which also means the relay
// refuses to serve it -- so it must not be announced.
func LookupModelLaunchPrice(name string) (ModelLaunchPrice, bool) {
	if e, ok := ratio_setting.CatalogEntryFor(name); ok {
		p := ModelLaunchPrice{Model: name, InputUSD: e.InputUSD, OutputUSD: e.OutputUSD,
			CacheReadUSD: e.CacheReadUSD, PerCallUSD: e.PerCallUSD, PriceUnit: e.PriceUnit}
		if e.ContextTier != nil {
			p.LongContextAbove = e.ContextTier.ThresholdTokens
		}
		return p, p.InputUSD > 0 || p.OutputUSD > 0 || p.PerCallUSD > 0
	}
	exprStr, ok := billing_setting.GetBillingExpr(name)
	if !ok || strings.TrimSpace(exprStr) == "" {
		return ModelLaunchPrice{}, false
	}
	perMillion := func(params billingexpr.TokenParams) (float64, bool) {
		v, _, err := billingexpr.RunExpr(exprStr, params)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return 0, false
		}
		// An expression is quota-per-token x 1e6 / QuotaPerUnit, i.e. its value
		// on N tokens is N x (USD per 1M); on 1M tokens it is USD x 1e6.
		return math.Round(v) / 1e6, true
	}
	const m = 1_000_000.0
	in, ok1 := perMillion(billingexpr.TokenParams{P: m, Len: 1})
	out, ok2 := perMillion(billingexpr.TokenParams{C: m, Len: 1})
	cr, ok3 := perMillion(billingexpr.TokenParams{CR: m, Len: 1})
	if !ok1 || !ok2 || !ok3 || (in <= 0 && out <= 0) {
		return ModelLaunchPrice{}, false
	}
	p := ModelLaunchPrice{Model: name, InputUSD: in, OutputUSD: out, CacheReadUSD: cr}
	p.LongContextAbove = exprTierThreshold(exprStr, in)
	return p, true
}

// exprTierThreshold finds the largest prompt length still billed at the
// standard input price, by bisection over 1..10M tokens. 0 = no tier.
func exprTierThreshold(exprStr string, standardIn float64) int {
	inputAt := func(n int) float64 {
		v, _, err := billingexpr.RunExpr(exprStr, billingexpr.TokenParams{P: 1_000_000, Len: float64(n)})
		if err != nil {
			return standardIn
		}
		return math.Round(v) / 1e6
	}
	const top = 10_000_000
	if inputAt(top) == standardIn {
		return 0
	}
	lo, hi := 1, top // inputAt(lo) == standard, inputAt(hi) != standard
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		if inputAt(mid) == standardIn {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo
}

// ModelLaunchCandidates is every enabled model the latest weekly update did
// not see and no announcement has named: the news nobody has been told yet.
// Empty until the weekly update has run once.
func ModelLaunchCandidates() ([]string, error) {
	current, err := model.EnabledModelNames()
	if err != nil {
		return nil, err
	}
	seenWeekly, err := model.LatestRetentionWeekModels()
	if err != nil || seenWeekly == nil {
		return []string{}, err
	}
	announced, err := model.AnnouncedModels()
	if err != nil {
		return nil, err
	}
	weekly := map[string]bool{}
	for _, m := range seenWeekly {
		weekly[m] = true
	}
	out := []string{}
	for _, m := range current {
		if !weekly[m] && !announced[m] {
			out = append(out, m)
		}
	}
	return out, nil
}

// validateModelLaunch checks a request before anything is previewed or sent:
// every model is enabled, priced, and not announced before.
func validateModelLaunch(models []string, note string) ([]ModelLaunchPrice, error) {
	if len(models) == 0 {
		return nil, errors.New("name at least one model")
	}
	if len(models) > modelLaunchMaxModels {
		return nil, fmt.Errorf("at most %d models per announcement", modelLaunchMaxModels)
	}
	if len([]rune(note)) > modelLaunchMaxNote {
		return nil, fmt.Errorf("the note is limited to %d characters", modelLaunchMaxNote)
	}
	enabled, err := model.EnabledModelNames()
	if err != nil {
		return nil, err
	}
	live := map[string]bool{}
	for _, m := range enabled {
		live[m] = true
	}
	announced, err := model.AnnouncedModels()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	prices := make([]ModelLaunchPrice, 0, len(models))
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		if !live[m] {
			return nil, fmt.Errorf("%s is not enabled on any channel, so customers cannot call it", m)
		}
		if announced[m] {
			return nil, fmt.Errorf("%s was already announced", m)
		}
		p, ok := LookupModelLaunchPrice(m)
		if !ok {
			return nil, fmt.Errorf("%s has no price, so the relay refuses it", m)
		}
		prices = append(prices, p)
	}
	if len(prices) == 0 {
		return nil, errors.New("name at least one model")
	}
	sort.Slice(prices, func(i, j int) bool { return prices[i].Model < prices[j].Model })
	return prices, nil
}

// BuildModelLaunchEmail renders the announcement for one recipient (userId
// 0 = a preview, whose unsubscribe link is not real).
func BuildModelLaunchEmail(prices []ModelLaunchPrice, note string, userId int) common.EmailMessage {
	names := make([]string, len(prices))
	for i, p := range prices {
		names[i] = p.Model
	}
	console := strings.TrimRight(system_setting.ServerAddress, "/")
	intro := strings.TrimSpace(note)
	if intro == "" {
		intro = "Now available on Unify API. Call it with any OpenAI SDK at the base URL below; your account discount applies."
		if len(prices) > 1 {
			intro = "Now available on Unify API. Call them with any OpenAI SDK at the base URL below; your account discount applies."
		}
	}
	msg := common.EmailMessage{
		Subject:      "New on Unify API: " + joinNames(names),
		Preheader:    "List prices and how to call it.",
		Eyebrow:      "New models",
		Headline:     "Now available: " + joinNames(names),
		Intro:        intro,
		CTALabel:     "See pricing",
		CTAURL:       console + "/pricing",
		FooterReason: "You get product updates because you have a Unify API account.",
	}
	if len(prices) == 1 {
		msg.Eyebrow = "New model"
	} else {
		msg.Preheader = "List prices and how to call them."
	}
	for _, p := range prices {
		msg.Details = append(msg.Details, common.EmailDetail{Label: p.Model, Value: launchPriceLine(p)})
	}
	msg.Details = append(msg.Details, common.EmailDetail{Label: "Base URL", Value: RetentionAPIBase})
	if userId > 0 {
		msg.UnsubscribeURL = RetentionUnsubscribeURL(userId)
	} else {
		msg.UnsubscribeURL = console + "/api/email/unsubscribe?u=0&t=preview"
	}
	return msg
}

func joinNames(names []string) string {
	switch len(names) {
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}

func launchUSD(v float64) string {
	s := fmt.Sprintf("%.4f", v)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if i := strings.Index(s, "."); i >= 0 && len(s)-i-1 == 1 {
		s += "0" // $0.1 reads as a typo; $0.10 does not
	}
	return "$" + s
}

func launchPriceLine(p ModelLaunchPrice) string {
	if p.PerCallUSD > 0 {
		unit := "request"
		if p.PriceUnit != "" {
			unit = p.PriceUnit
		}
		return launchUSD(p.PerCallUSD) + " per " + unit + " (list price)"
	}
	line := launchUSD(p.InputUSD) + " input · " + launchUSD(p.OutputUSD) + " output"
	if p.CacheReadUSD > 0 && p.CacheReadUSD != p.InputUSD {
		line += " · " + launchUSD(p.CacheReadUSD) + " cached input"
	}
	line += " per 1M tokens (list price)"
	if p.LongContextAbove > 0 {
		line += fmt.Sprintf("; prompts over %s tokens bill at a higher tier", tokensShort(p.LongContextAbove))
	}
	return line
}

func tokensShort(n int) string {
	if n%1000 == 0 {
		return fmt.Sprintf("%dK", n/1000)
	}
	return fmt.Sprintf("%d", n)
}

// ModelLaunchPreview is what the operator sees before pressing Send.
type ModelLaunchPreview struct {
	Prices     []ModelLaunchPrice `json:"prices"`
	Subject    string             `json:"subject"`
	HTML       string             `json:"html"`
	Text       string             `json:"text"`
	Recipients int64              `json:"recipients"`
	Enabled    bool               `json:"enabled"`
}

func PreviewModelLaunchEmail(models []string, note string) (*ModelLaunchPreview, error) {
	prices, err := validateModelLaunch(models, note)
	if err != nil {
		return nil, err
	}
	msg := BuildModelLaunchEmail(prices, note, 0)
	html, text, err := common.RenderEmail(UnifyAPIEmailBrand(), msg)
	if err != nil {
		return nil, err
	}
	n, err := model.CountMarketingRecipients()
	if err != nil {
		return nil, err
	}
	return &ModelLaunchPreview{Prices: prices, Subject: msg.Subject, HTML: html, Text: text,
		Recipients: n, Enabled: ModelLaunchEmailEnabled()}, nil
}

// StartModelLaunchEmail records the operator's approval and sends in the
// background. One announcement at a time per process.
func StartModelLaunchEmail(models []string, note string, actorId int) (*model.ModelLaunchEmail, error) {
	if !ModelLaunchEmailEnabled() {
		return nil, ErrModelLaunchDisabled
	}
	modelLaunchMu.Lock()
	defer modelLaunchMu.Unlock()
	if modelLaunchRunning {
		return nil, ErrModelLaunchRunning
	}
	prices, err := validateModelLaunch(models, note)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(prices))
	for i, p := range prices {
		names[i] = p.Model
	}
	launch, err := model.CreateModelLaunchEmail(names, strings.TrimSpace(note), actorId)
	if err != nil {
		return nil, err
	}
	modelLaunchRunning = true
	go func() {
		defer func() {
			modelLaunchMu.Lock()
			modelLaunchRunning = false
			modelLaunchMu.Unlock()
		}()
		runModelLaunchEmail(launch.Id, prices, launch.Note)
	}()
	return launch, nil
}

// ModelLaunchQueueDelay is how long a queued launch waits before it is sent.
// It is the cancel window: deleting the row inside it stops the email.
var ModelLaunchQueueDelay = 15 * time.Minute

// StartModelLaunchQueueWorker sends launches queued by the catalogue
// automation. A no-op unless announcement email is enabled here (production
// only) and this is the master node.
func StartModelLaunchQueueWorker() {
	if !RetentionEmailEnabled() || !common.IsMasterNode {
		return
	}
	common.SysLog("model launch email: queue worker enabled")
	go func() {
		time.Sleep(2 * time.Minute) // let the boot finish and options load
		for {
			RunQueuedModelLaunches()
			time.Sleep(time.Minute)
		}
	}()
}

// RunQueuedModelLaunches sends every queued launch that is old enough. Each
// is re-validated at send time exactly as an operator's Send is: a model that
// was disabled, lost its price, or was announced meanwhile is refused and the
// reason recorded on the row.
func RunQueuedModelLaunches() {
	if !ModelLaunchEmailEnabled() {
		return
	}
	due, err := model.DueQueuedModelLaunches(time.Now().Add(-ModelLaunchQueueDelay).Unix())
	if err != nil {
		common.SysError("model launch email: queue: " + err.Error())
		return
	}
	for i := range due {
		l := &due[i]
		modelLaunchMu.Lock()
		busy := modelLaunchRunning
		if !busy {
			modelLaunchRunning = true
		}
		modelLaunchMu.Unlock()
		if busy {
			return // an operator's send is in flight; next tick
		}
		func() {
			defer func() {
				modelLaunchMu.Lock()
				modelLaunchRunning = false
				modelLaunchMu.Unlock()
			}()
			taken, err := model.TakeQueuedModelLaunch(l.Id)
			if err != nil || !taken {
				return
			}
			prices, err := validateModelLaunch(l.ModelList(), l.Note)
			if err != nil {
				common.SysLog(fmt.Sprintf("model launch email %d: queued launch refused: %s", l.Id, err.Error()))
				_ = model.RefuseModelLaunch(l.Id, err.Error())
				return
			}
			runModelLaunchEmail(l.Id, prices, strings.TrimSpace(l.Note))
		}()
	}
}

func runModelLaunchEmail(launchId int, prices []ModelLaunchPrice, note string) {
	users, err := model.ModelLaunchRecipients(launchId)
	if err != nil {
		common.SysError("model launch email: recipients: " + err.Error())
		_ = model.FinishModelLaunchEmail(launchId, 0, 0, 0)
		return
	}
	sent, failed := 0, 0
	for i := range users {
		u := &users[i]
		claimed, err := model.ClaimModelLaunchSend(launchId, u.Id)
		if err != nil || !claimed {
			continue
		}
		if err := SendBrandedEmail(modelLaunchPurpose, u.Email, BuildModelLaunchEmail(prices, note, u.Id)); err != nil {
			failed++
			model.RecordModelLaunchSendError(launchId, u.Id, err)
			common.SysError(fmt.Sprintf("model launch email %d: user %d: %s", launchId, u.Id, err.Error()))
		} else {
			sent++
		}
		time.Sleep(retentionSendGap)
	}
	if err := model.FinishModelLaunchEmail(launchId, len(users), sent, failed); err != nil {
		common.SysError("model launch email: finish: " + err.Error())
	}
	common.SysLog(fmt.Sprintf("model launch email %d: sent %d, failed %d", launchId, sent, failed))
}
