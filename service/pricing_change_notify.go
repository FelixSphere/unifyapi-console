/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package service

// UNIFYAPI-FORK: tell customers when a list price they pay has changed.
//
// The pricing baseline lives in code (setting/ratio_setting/unifyapi_catalog.go)
// and changes by deploy. A deploy is silent from the customer's side: the
// pricing page shows the new number and the next invoice reflects it, and
// nothing in between says so. Operator rule (2026-09-22): when the baseline of
// a model on sale changes, the customers using it are emailed.
//
// This runs on the system-task scheduler and compares the live catalog with
// the last baseline that was announced, stored in an options row. Anything
// different on a model that is enabled on a channel is a change customers can
// feel, and it is sent to everyone who called that model in the last thirty
// days plus every admin. The announced baseline is then advanced, so a change
// is told once.
//
// The very first run records the current baseline and sends nothing: there is
// no "before" to compare against, and mailing every customer a table of sixty
// unchanged prices would teach them to delete the next one unread.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// pricingChangeLookback is how far back a customer must have called a model
// to be told its price changed.
const pricingChangeLookback = 30 * 24 * time.Hour

// AnnouncedPrice is one model's list price as customers were last told it.
// USD per 1M tokens, or per unit for per-call models -- the same numbers the
// catalog row carries, so the comparison is exact rather than a ratio
// round-trip.
type AnnouncedPrice struct {
	InputUSD      float64 `json:"input_usd,omitempty"`
	OutputUSD     float64 `json:"output_usd,omitempty"`
	CacheReadUSD  float64 `json:"cache_read_usd,omitempty"`
	CacheWriteUSD float64 `json:"cache_write_usd,omitempty"`
	PerCallUSD    float64 `json:"per_call_usd,omitempty"`
	PriceUnit     string  `json:"price_unit,omitempty"`
}

func announcedPriceOf(entry ratio_setting.CatalogEntry) AnnouncedPrice {
	return AnnouncedPrice{
		InputUSD:      entry.InputUSD,
		OutputUSD:     entry.OutputUSD,
		CacheReadUSD:  entry.CacheReadUSD,
		CacheWriteUSD: entry.CacheWriteUSD,
		PerCallUSD:    entry.PerCallUSD,
		PriceUnit:     entry.PriceUnit,
	}
}

// CurrentPricingBaseline is the catalog -- compiled rows plus admin-added
// extras -- as a baseline map. Official list prices only: the customer's own
// discount or contract is applied per request and is not what changed.
func CurrentPricingBaseline() map[string]AnnouncedPrice {
	out := map[string]AnnouncedPrice{}
	for _, entry := range ratio_setting.Catalog() {
		out[entry.Model] = announcedPriceOf(entry)
	}
	return out
}

// PriceChangeKind says what happened to a model between two baselines.
type PriceChangeKind string

const (
	PriceChangeAdded   PriceChangeKind = "added"
	PriceChangeChanged PriceChangeKind = "changed"
	PriceChangeRemoved PriceChangeKind = "removed"
)

// PriceChange is one model whose baseline differs from the announced one.
type PriceChange struct {
	Model  string          `json:"model"`
	Kind   PriceChangeKind `json:"kind"`
	Before AnnouncedPrice  `json:"before"`
	After  AnnouncedPrice  `json:"after"`
}

// DiffPricingBaseline lists the changes customers can feel: models on sale
// (enabled on a channel) whose price moved or that are newly priced, and models
// still on a channel whose price was withdrawn, since the relay now refuses
// them. A change on a model nobody is routed to is not announced; the stored
// baseline still advances, so the customer is never told about a "change" from
// a price they were never charged.
func DiffPricingBaseline(previous, current map[string]AnnouncedPrice, served map[string]bool) []PriceChange {
	var changes []PriceChange
	for name, after := range current {
		if !served[name] {
			continue
		}
		before, known := previous[name]
		switch {
		case !known:
			changes = append(changes, PriceChange{Model: name, Kind: PriceChangeAdded, After: after})
		case before != after:
			changes = append(changes, PriceChange{Model: name, Kind: PriceChangeChanged, Before: before, After: after})
		}
	}
	for name, before := range previous {
		if _, still := current[name]; still || !served[name] {
			continue
		}
		changes = append(changes, PriceChange{Model: name, Kind: PriceChangeRemoved, Before: before})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Model < changes[j].Model })
	return changes
}

// PricingChangeNoticeResult is what one run did, recorded on the task row.
type PricingChangeNoticeResult struct {
	Seeded    bool     `json:"seeded,omitempty"`
	Changes   int      `json:"changes"`
	Models    []string `json:"models,omitempty"`
	Notified  int      `json:"notified"`
	Failed    int      `json:"failed,omitempty"`
	Skipped   int      `json:"skipped,omitempty"`
	Announced bool     `json:"announced"`
}

// sendPricingChangeNotify is the delivery seam; tests replace it.
var sendPricingChangeNotify = NotifyUser

// RunPricingChangeNotice compares the live catalog with the announced baseline
// and, when a model on sale moved, notifies the customers who use it. Returns
// what it did; an error means the announced baseline was NOT advanced and the
// next run will try again.
func RunPricingChangeNotice(now time.Time) (*PricingChangeNoticeResult, error) {
	current := CurrentPricingBaseline()
	currentJSON, err := common.Marshal(current)
	if err != nil {
		return nil, err
	}

	stored := model.GetAnnouncedPricingBaseline()
	if strings.TrimSpace(stored) == "" {
		if err := model.SaveAnnouncedPricingBaseline(string(currentJSON)); err != nil {
			return nil, fmt.Errorf("recording the initial baseline: %w", err)
		}
		return &PricingChangeNoticeResult{Seeded: true, Announced: true}, nil
	}

	previous := map[string]AnnouncedPrice{}
	if err := common.Unmarshal([]byte(stored), &previous); err != nil {
		return nil, fmt.Errorf("the stored baseline does not parse; refusing to guess what customers were told: %w", err)
	}

	served := map[string]bool{}
	for _, name := range model.GetEnabledModels() {
		served[name] = true
	}
	changes := DiffPricingBaseline(previous, current, served)
	result := &PricingChangeNoticeResult{Changes: len(changes)}
	if len(changes) == 0 {
		if stored != string(currentJSON) {
			// Only unserved rows moved. Advance quietly so a later listing of
			// that model is not announced as a change from a price nobody paid.
			if err := model.SaveAnnouncedPricingBaseline(string(currentJSON)); err != nil {
				return nil, err
			}
			result.Announced = true
		}
		return result, nil
	}
	for _, change := range changes {
		result.Models = append(result.Models, change.Model)
	}

	recipients, err := pricingChangeRecipients(changes, now.Add(-pricingChangeLookback).Unix())
	if err != nil {
		return nil, err
	}
	for _, recipient := range recipients {
		if err := notifyPricingChange(recipient.user, recipient.changes, now); err != nil {
			result.Failed++
			common.SysError(fmt.Sprintf("pricing change notice: user %d: %s", recipient.user.Id, err.Error()))
			continue
		}
		result.Notified++
	}

	// Advance the announced baseline only after the notices went out. A failed
	// save means the next run re-sends, which beats a change nobody hears of.
	if err := model.SaveAnnouncedPricingBaseline(string(currentJSON)); err != nil {
		return result, fmt.Errorf("notices sent but the announced baseline could not be recorded: %w", err)
	}
	result.Announced = true
	common.SysLog(fmt.Sprintf("pricing change notice: %d model(s) changed, %d user(s) notified, %d failed",
		len(changes), result.Notified, result.Failed))
	return result, nil
}

type pricingChangeRecipient struct {
	user    model.User
	changes []PriceChange
}

// pricingChangeRecipients pairs each recipient with the changes that concern
// them: the models they called in the lookback window, or every change for an
// admin. Ordered by user id so a run's log reads the same twice.
func pricingChangeRecipients(changes []PriceChange, since int64) ([]pricingChangeRecipient, error) {
	byModel := map[string]PriceChange{}
	models := make([]string, 0, len(changes))
	for _, change := range changes {
		byModel[change.Model] = change
		models = append(models, change.Model)
	}

	usedBy, err := model.ModelUsersSince(models, since)
	if err != nil {
		return nil, fmt.Errorf("listing who used the repriced models: %w", err)
	}
	ids := make([]int, 0, len(usedBy))
	for id := range usedBy {
		ids = append(ids, id)
	}
	users, err := model.EnabledUsersByIds(ids)
	if err != nil {
		return nil, err
	}
	admins, err := model.EnabledAdminUsers()
	if err != nil {
		return nil, err
	}

	recipients := map[int]*pricingChangeRecipient{}
	for _, user := range users {
		var mine []PriceChange
		for _, name := range usedBy[user.Id] {
			mine = append(mine, byModel[name])
		}
		sort.Slice(mine, func(i, j int) bool { return mine[i].Model < mine[j].Model })
		recipients[user.Id] = &pricingChangeRecipient{user: user, changes: mine}
	}
	for _, admin := range admins {
		recipients[admin.Id] = &pricingChangeRecipient{user: admin, changes: changes}
	}

	out := make([]pricingChangeRecipient, 0, len(recipients))
	for _, recipient := range recipients {
		out = append(out, *recipient)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].user.Id < out[j].user.Id })
	return out, nil
}

func notifyPricingChange(user model.User, changes []PriceChange, now time.Time) error {
	setting := user.GetSetting()
	subject := fmt.Sprintf("%s pricing update: %s", common.SystemName, pricingChangeHeadline(changes))
	var content string
	switch setting.NotifyType {
	case dto.NotifyTypeBark, dto.NotifyTypeGotify:
		content = pricingChangePlainText(changes, now)
	default:
		content = pricingChangeHTML(changes, now)
	}
	return sendPricingChangeNotify(user.Id, user.Email, setting, dto.NewNotify(dto.NotifyTypePricingChange, subject, content, nil))
}

func pricingChangeHeadline(changes []PriceChange) string {
	if len(changes) == 1 {
		return changes[0].Model
	}
	return fmt.Sprintf("%d models", len(changes))
}

func pricingPageLink() string {
	return strings.TrimRight(system_setting.ServerAddress, "/") + "/pricing"
}

// usd renders a list price the way the pricing page does: shortest exact
// decimal, so $0.075 does not become $0.08 in a notice about a 7.5 cent rate.
func usd(value float64) string {
	return "$" + strconv.FormatFloat(value, 'f', -1, 64)
}

// priceLine renders one price as the customer reads it on the pricing page.
func priceLine(price AnnouncedPrice) string {
	if price.PerCallUSD > 0 {
		unit := "per request"
		if price.PriceUnit == "second" {
			unit = "per second of output"
		}
		return usd(price.PerCallUSD) + " " + unit
	}
	parts := []string{usd(price.InputUSD) + " in", usd(price.OutputUSD) + " out"}
	if price.CacheReadUSD > 0 {
		parts = append(parts, usd(price.CacheReadUSD)+" cached read")
	}
	if price.CacheWriteUSD > 0 {
		parts = append(parts, usd(price.CacheWriteUSD)+" cache write")
	}
	return strings.Join(parts, " / ") + " per 1M tokens"
}

func pricingChangeHTML(changes []PriceChange, now time.Time) string {
	var b strings.Builder
	b.WriteString("<p>Hello,</p>")
	fmt.Fprintf(&b, "<p>The official list price of a model you have used on %s changed on %s. Your account is billed from these list prices; any discount or contract rate on your account still applies on top of them.</p>",
		common.SystemName, now.UTC().Format("2 January 2006"))
	b.WriteString("<table cellpadding='6' style='border-collapse:collapse'>")
	b.WriteString("<tr><th align='left'>Model</th><th align='left'>Previous list price</th><th align='left'>New list price</th></tr>")
	for _, change := range changes {
		before, after := priceLine(change.Before), priceLine(change.After)
		switch change.Kind {
		case PriceChangeAdded:
			before = "&mdash; (newly listed)"
		case PriceChangeRemoved:
			after = "withdrawn &mdash; requests to this model are now refused"
		}
		fmt.Fprintf(&b, "<tr><td><code>%s</code></td><td>%s</td><td><b>%s</b></td></tr>", change.Model, before, after)
	}
	b.WriteString("</table>")
	link := pricingPageLink()
	fmt.Fprintf(&b, "<p>The full price list is at <a href='%s'>%s</a>. Reply to this email if anything about your invoice does not add up.</p>", link, link)
	return b.String()
}

func pricingChangePlainText(changes []PriceChange, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "List prices changed on %s:\n", now.UTC().Format("2006-01-02"))
	for _, change := range changes {
		switch change.Kind {
		case PriceChangeAdded:
			fmt.Fprintf(&b, "%s: newly listed at %s\n", change.Model, priceLine(change.After))
		case PriceChangeRemoved:
			fmt.Fprintf(&b, "%s: withdrawn, requests are now refused\n", change.Model)
		default:
			fmt.Fprintf(&b, "%s: %s -> %s\n", change.Model, priceLine(change.Before), priceLine(change.After))
		}
	}
	b.WriteString(pricingPageLink())
	return b.String()
}
