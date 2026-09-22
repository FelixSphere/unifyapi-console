/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package controller

// UNIFYAPI-FORK: the supplier portal -- what a credit supplier may see and do
// about their own lots. Authenticated as an ordinary console user; the login
// is mapped to a supplier by CreditSupplier.UserId, so there is no new role.
//
// Suppliers see their lots, their draw-down and the settlements issued to
// them. They may SUBMIT a lot -- which creates a disabled channel carrying
// their key -- but never activate one; approval is the operator's, because
// approval is where the right-to-transfer question gets asked.
//
// What they never see: other suppliers, operator notes, payout terms, the
// channel key they submitted (write-only), or anything priced from the
// customer side.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// supplierVendorPreset is one vendor a supplier can submit credits for. The
// channel type and base URL are fixed per vendor so a submission cannot point
// our relay at an arbitrary host; models default to the catalogue's list for
// that vendor and may be narrowed, never widened beyond what we can price.
type supplierVendorPreset struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	ChannelType int      `json:"channel_type"`
	BaseURL     string   `json:"base_url"`
	Models      []string `json:"models"`
}

var supplierVendorTable = []supplierVendorPreset{
	{Key: "openai", Label: "OpenAI", ChannelType: constant.ChannelTypeOpenAI, BaseURL: "https://api.openai.com"},
	{Key: "anthropic", Label: "Anthropic", ChannelType: constant.ChannelTypeAnthropic, BaseURL: "https://api.anthropic.com"},
	{Key: "google", Label: "Google (Gemini API)", ChannelType: constant.ChannelTypeGemini, BaseURL: "https://generativelanguage.googleapis.com"},
}

func supplierVendorPresets() []supplierVendorPreset {
	out := make([]supplierVendorPreset, 0, len(supplierVendorTable))
	for _, preset := range supplierVendorTable {
		models := make([]string, 0)
		for _, entry := range ratio_setting.Catalog() {
			if entry.Vendor == preset.Key {
				models = append(models, entry.Model)
			}
		}
		sort.Strings(models)
		preset.Models = models
		out = append(out, preset)
	}
	return out
}

func supplierVendorPreset_(key string) (supplierVendorPreset, bool) {
	for _, preset := range supplierVendorPresets() {
		if preset.Key == key {
			return preset, true
		}
	}
	return supplierVendorPreset{}, false
}

// portalSupplier resolves the caller's supplier or answers 404. A logged-in
// user who is not a supplier gets the same answer as a missing page, which is
// what the frontend uses to decide whether to show the portal at all.
func portalSupplier(c *gin.Context) (*model.CreditSupplier, bool) {
	supplier, err := model.GetCreditSupplierByUserId(c.GetInt("id"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "this account is not linked to a credit supplier"})
			return nil, false
		}
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return nil, false
	}
	return supplier, true
}

// portalSupplierView is the supplier's own record minus operator memory.
type portalSupplierView struct {
	Id           int    `json:"id"`
	Name         string `json:"name"`
	Code         string `json:"code"`
	ContactEmail string `json:"contact_email"`
	Status       string `json:"status"`
	StatusReason string `json:"status_reason"`
	Counterparty string `json:"counterparty"`
	// The seller's own payout account, in full: it is theirs.
	PayoutMethod     string `json:"payout_method"`
	PayoutHolder     string `json:"payout_holder"`
	PayoutDetails    string `json:"payout_details"`
	PayoutCurrency   string `json:"payout_currency"`
	HasPayoutAccount bool   `json:"has_payout_account"`
	// PayoutMethodLabel is the rail's name, so a screen never has to keep its
	// own copy of the list and drift from it.
	PayoutMethodLabel string `json:"payout_method_label"`
}

// UpdateSupplierPayoutAccount files where the calling seller is paid. It is
// the one thing a seller must do before they can submit a key.
func UpdateSupplierPayoutAccount(c *gin.Context) {
	var acct model.SupplierPayoutAccount
	if err := c.ShouldBindJSON(&acct); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid request body"})
		return
	}
	supplier, ok := sellerFor(c)
	if !ok {
		return
	}
	updated, err := model.SetSupplierPayoutAccount(supplier.Id, acct)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": supplierView(updated)})
}

func supplierView(s *model.CreditSupplier) portalSupplierView {
	return portalSupplierView{
		Id: s.Id, Name: s.Name, Code: s.Code, ContactEmail: s.ContactEmail,
		Status: s.Status, StatusReason: s.StatusReason, Counterparty: s.CounterpartyKey(),
		PayoutMethod: s.PayoutMethod, PayoutHolder: s.PayoutHolder, PayoutDetails: s.PayoutDetails,
		PayoutCurrency: s.PayoutCurrency, HasPayoutAccount: s.HasPayoutAccount(),
		PayoutMethodLabel: s.PayoutMethodLabel(),
	}
}

// portalLotView is a lot without the operator's note.
type portalLotView struct {
	Id               int     `json:"id"`
	Vendor           string  `json:"vendor"`
	ChannelId        int     `json:"channel_id"`
	ChannelName      string  `json:"channel_name"`
	FaceValueUSD     float64 `json:"face_value_usd"`
	ConsumedUSD      float64 `json:"consumed_usd"`
	RemainingUSD     float64 `json:"remaining_usd"`
	UnpricedRequests int64   `json:"unpriced_requests"`
	ExpiresAt        int64   `json:"expires_at"`
	Status           string  `json:"status"`
	StatusReason     string  `json:"status_reason"`
	Source           string  `json:"source"`
	RetiredAt        int64   `json:"retired_at"`
	CreatedAt        int64   `json:"created_at"`
	VerifiedAt       int64   `json:"verified_at"`
	PayoutMethod     string  `json:"payout_method"`
	// The dividend side of a contributed key. Zero on a lot bought outright.
	RevenueSharePct float64 `json:"revenue_share_pct"`
	ShareRevenueUSD float64 `json:"share_revenue_usd"`
	ShareEarnedUSD  float64 `json:"share_earned_usd"`
	SharePaidUSD    float64 `json:"share_paid_usd"`
	ShareUnpaidUSD  float64 `json:"share_unpaid_usd"`
}

func GetSupplierPortal(c *gin.Context) {
	supplier, ok := portalSupplier(c)
	if !ok {
		return
	}
	lots, err := model.GetCreditLots(model.CreditLotFilter{SupplierId: supplier.Id})
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	channelIds := make([]int, 0, len(lots))
	for _, lot := range lots {
		if lot.ChannelId != 0 {
			channelIds = append(channelIds, lot.ChannelId)
		}
	}
	channelNames := map[int]string{}
	if len(channelIds) > 0 {
		channels, err := model.GetChannelsByIds(channelIds)
		if err == nil {
			for _, channel := range channels {
				channelNames[channel.Id] = channel.Name
			}
		}
	}

	views := make([]portalLotView, 0, len(lots))
	totals := gin.H{}
	var face, consumed, remaining float64
	for _, lot := range lots {
		views = append(views, portalLotView{
			Id: lot.Id, Vendor: lot.Vendor, ChannelId: lot.ChannelId, ChannelName: channelNames[lot.ChannelId],
			FaceValueUSD: lot.FaceValueUSD, ConsumedUSD: lot.ConsumedUSD,
			RemainingUSD: lot.RemainingUSD(), UnpricedRequests: lot.UnpricedRequests,
			ExpiresAt: lot.ExpiresAt, Status: lot.Status, StatusReason: lot.StatusReason, Source: lot.Source, RetiredAt: lot.RetiredAt, CreatedAt: lot.CreatedAt,
			VerifiedAt: lot.VerifiedAt, PayoutMethod: lot.PayoutMethod,
			RevenueSharePct: lot.RevenueSharePct,
			ShareRevenueUSD: lot.ShareRevenueUSD, ShareEarnedUSD: lot.EarnedShareUSD(),
			SharePaidUSD: lot.PaidShareUSD, ShareUnpaidUSD: lot.UnpaidShareUSD(),
		})
		if lot.Status == model.CreditLotStatusRejected {
			continue
		}
		face += lot.FaceValueUSD
		consumed += lot.ConsumedUSD
		remaining += lot.RemainingUSD()
	}
	totals["face_usd"] = face
	totals["consumed_usd"] = consumed
	totals["remaining_usd"] = remaining
	share := model.SumCreditShare(lots)
	totals["share_revenue_usd"] = share.RevenueUSD
	totals["share_earned_usd"] = share.EarnedUSD
	totals["share_paid_usd"] = share.PaidUSD
	totals["share_unpaid_usd"] = share.UnpaidUSD

	payouts, err := model.ListCreditSharePayouts(model.CreditShareFilter{SupplierId: supplier.Id})
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{
		"supplier":      supplierView(supplier),
		"lots":          views,
		"totals":        totals,
		"share_payouts": payouts,
		"vendors":       supplierVendorPresets(),
		"terms":         postedTerms(model.GetCreditSupplyTerms()),
		"payout_rails":  payoutRailsFor(supplier),
	}})
}

// GetSupplierTerms is the price list a seller sees before typing anything.
func GetSupplierTerms(c *gin.Context) {
	terms := model.GetCreditSupplyTerms()
	data := postedTerms(terms)
	data["channel_priority"] = terms.ChannelPriority
	data["vendors"] = supplierVendorPresets()
	data["payout_rails"] = payoutRailsFor(nil)
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": data})
}

// payoutRailsFor is the list the seller's dialog renders. It is the rails
// Payment Settings actually has an account for, plus -- when the seller
// already filed a rail that is no longer offered -- that one, so opening the
// dialog cannot silently switch them onto something else and then save it.
func payoutRailsFor(supplier *model.CreditSupplier) []model.PayoutRail {
	rails := model.PayoutRails()
	if supplier == nil || supplier.PayoutMethod == "" {
		return rails
	}
	current := model.NormalizePayoutMethod(supplier.PayoutMethod)
	for _, rail := range rails {
		if rail.Id == current {
			return rails
		}
	}
	if rail, ok := model.PayoutRailFor(current); ok {
		return append(rails, rail)
	}
	return rails
}

// postedTerms is the offer, and only the offer: the share a seller keeps and
// the thresholds around it. Everything else is operator business.
func postedTerms(terms model.CreditSupplyTerms) gin.H {
	// Sellers are offered one deal: a share of what their credits sell for.
	// The buy rates still exist for lots an operator enters by hand, and are
	// operator business.
	return gin.H{
		"min_face_usd":         terms.MinFaceUSD,
		"revenue_share_rates":  terms.RevenueShareRates,
		"revenue_share_basis":  terms.ShareBasis(),
		"min_share_payout_usd": terms.MinSharePayoutUSD,
		"manual_review":        terms.ManualReview,
	}
}

// verifySupplierChannel makes one real request through the submitted key. It
// is a variable so tests can stand in for the vendor.
// verificationUsage is what the verification request consumed, so the lot can
// account for the seller's credits it burned before we owned them.
type verificationUsage struct {
	PromptTokens int
	// CachedTokens is the subset of PromptTokens served from the vendor's
	// cache. It must travel with the usage: the vendor charged the cache-read
	// rate for them, so the lot has to be debited at that rate too, exactly as
	// customer draw-down is. Dropping it would debit the seller more than the
	// vendor took (12.5% on a typical 40%-cached profile).
	CachedTokens     int
	CompletionTokens int
}

var verifySupplierChannel = func(channel *model.Channel, testModel string, testUserID int) (verificationUsage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	// The request runs as the seller (the relay needs a real user for group
	// resolution) but is NOT recorded as their consumption: nobody is charged
	// for a verification, so it must not appear as revenue or as customer
	// cost in reconciliation. Its vendor-side cost is booked on the lot instead.
	ctx = withChannelTestOptions(ctx, channelTestOptions{SkipConsumeLog: true})
	result := testChannel(ctx, channel, testUserID, testModel, "", false)
	if result.localErr != nil {
		return verificationUsage{}, result.localErr
	}
	if result.newAPIError != nil {
		return verificationUsage{}, result.newAPIError
	}
	return verificationUsage{PromptTokens: result.promptTokens, CachedTokens: result.cachedTokens, CompletionTokens: result.completionTokens}, nil
}

// cheapestVerificationModel picks the least expensive catalogue model among
// those the channel will serve: the verification request is paid for with the
// seller's credits, so it should cost cents, not dollars.
func cheapestVerificationModel(models []string) string {
	best := ""
	bestPrice := 0.0
	for _, name := range models {
		entry, ok := ratio_setting.CatalogEntryFor(name)
		if !ok {
			continue
		}
		price := entry.InputUSD + entry.OutputUSD
		if best == "" || price < bestPrice {
			best, bestPrice = name, price
		}
	}
	if best == "" && len(models) > 0 {
		return models[0]
	}
	return best
}

// sellerFor resolves (or creates) the supplier behind the calling login.
func sellerFor(c *gin.Context) (*model.CreditSupplier, bool) {
	userId := c.GetInt("id")
	user, err := model.GetUserById(userId, false)
	if err != nil || user == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "sign in to sell credits"})
		return nil, false
	}
	supplier, err := model.EnsureCreditSupplierForUser(user)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return nil, false
	}
	return supplier, true
}

// supplierLotSubmission is a seller handing us a key. There is one deal -- a
// share of what the credits sell for, at the posted rate -- so the seller
// names no price and chooses no deal; how they are paid is on their profile.
type supplierLotSubmission struct {
	Vendor                  string   `json:"vendor"`
	FaceValueUSD            float64  `json:"face_value_usd"`
	ExpiresAt               int64    `json:"expires_at"`
	Note                    string   `json:"note"`
	UpstreamKey             string   `json:"upstream_key"`
	Models                  []string `json:"models"`
	TransferRightsConfirmed bool     `json:"transfer_rights_confirmed"`
}

func SubmitSupplierLot(c *gin.Context) {
	var req supplierLotSubmission
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid request body"})
		return
	}
	preset, known := supplierVendorPreset_(strings.ToLower(strings.TrimSpace(req.Vendor)))
	if !known {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "choose one of the offered vendors"})
		return
	}
	terms := model.GetCreditSupplyTerms()
	sharePct, taking := terms.RevenueShareRate(preset.Key)
	if !taking {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "we are not taking " + preset.Label + " credits at the moment"})
		return
	}
	if req.FaceValueUSD < terms.MinFaceUSD {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("the smallest balance we take on is $%.0f of credit", terms.MinFaceUSD)})
		return
	}
	supplier, ok := sellerFor(c)
	if !ok {
		return
	}
	if supplier.Status != model.CreditSupplierStatusActive {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": model.ErrCreditSupplierSuspended.Error()})
		return
	}
	if !supplier.HasPayoutAccount() {
		// Filed first, on purpose: with a share deal nothing is paid up front,
		// so the account must be on record before the first dollar is owed.
		c.JSON(http.StatusOK, gin.H{"success": false, "code": "payout_account_required",
			"message": "add your payout account before submitting credits, so we know where to send your share"})
		return
	}
	req.UpstreamKey = strings.TrimSpace(req.UpstreamKey)
	if req.UpstreamKey == "" || strings.ContainsAny(req.UpstreamKey, " \n\r\t") {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "an upstream API key is required, one key only"})
		return
	}
	if !req.TransferRightsConfirmed {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "confirm that you have the right to transfer these credits"})
		return
	}
	models := preset.Models
	if len(req.Models) > 0 {
		allowed := map[string]bool{}
		for _, name := range preset.Models {
			allowed[name] = true
		}
		models = make([]string, 0, len(req.Models))
		for _, name := range req.Models {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if !allowed[name] {
				c.JSON(http.StatusOK, gin.H{"success": false, "message": "model " + name + " is not in our " + preset.Label + " catalogue"})
				return
			}
			models = append(models, name)
		}
	}
	if len(models) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "no priced models are available for this vendor yet"})
		return
	}

	baseURL := preset.BaseURL
	channel := &model.Channel{
		Type:    preset.ChannelType,
		Key:     req.UpstreamKey,
		Name:    supplier.CounterpartyKey() + " " + preset.Key,
		BaseURL: &baseURL,
		Models:  strings.Join(models, ","),
		Group:   "default",
	}
	lot := &model.CreditLot{
		Vendor:            preset.Key,
		DealType:          model.CreditLotDealRevenueShare,
		FaceValueUSD:      req.FaceValueUSD,
		RevenueSharePct:   sharePct, // the posted share, never the caller's number
		RevenueShareBasis: terms.ShareBasis(),
		ExpiresAt:         req.ExpiresAt,
		Note:              strings.TrimSpace(req.Note),
		PayoutMethod:      supplier.ExternalPayoutMethod(),
	}
	actor := optionChangeActor(c)
	if err := model.SubmitSupplierCreditLot(supplier, channel, lot, actor); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	common.SysLog("credit supply: supplier " + supplier.Code + " submitted lot #" + strconv.Itoa(lot.Id) + " on channel #" + strconv.Itoa(channel.Id))

	// Verify now, while the seller is still on the page: one real request
	// through the key. A failure is answered immediately with the vendor's
	// reason, and the key is discarded -- no operator round trip.
	testModel := cheapestVerificationModel(models)
	usage, err := verifySupplierChannel(channel, testModel, c.GetInt("id"))
	if err != nil {
		reason := "automatic verification failed: " + err.Error()
		if rejectErr := model.RejectUnverifiedSubmission(lot, reason); rejectErr != nil {
			common.SysError("credit supply: could not record failed verification for lot #" + strconv.Itoa(lot.Id) + ": " + rejectErr.Error())
		}
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "we could not make a request with this key (" + err.Error() + "). Check the key and try again.", "data": gin.H{
			"lot_id": lot.Id, "status": model.CreditLotStatusRejected,
		}})
		return
	}
	// The verification drew on the seller's vendor balance, at list price like
	// every other draw-down; the lot's remaining figure must not pretend it
	// did not happen.
	// Same pricing path as customer draw-down (RecordCreditSupplyConsumption):
	// list price with cached reads at the vendor's cache-read rate.
	// The verification call is a plain OpenAI-format probe, so its prompt count
	// already contains any cached reads; no semantic marker is needed.
	verificationUSD, _ := ratio_setting.ListPriceUSD(testModel, ratio_setting.TokenUsage{
		PromptTokens:     int64(usage.PromptTokens),
		CachedTokens:     int64(usage.CachedTokens),
		CompletionTokens: int64(usage.CompletionTokens),
	})
	verified, err := model.MarkCreditLotVerified(lot.Id, "system",
		fmt.Sprintf("key answered a %s request (%d in, %d of them cached / %d out tokens, $%.6f at list price, drawn from the lot)", testModel, usage.PromptTokens, usage.CachedTokens, usage.CompletionTokens, verificationUSD),
		verificationUSD)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	final := verified
	if !terms.ManualReview {
		// The key works, the seller attested, and we owe nothing until it
		// earns: it goes into service now. An operator can still suspend it.
		activated, err := model.ActivateVerifiedShareLot(verified.Id, "system (seller attested; auto-activated)")
		if err != nil {
			common.SysError("credit supply: verified lot #" + strconv.Itoa(verified.Id) + " could not be activated automatically: " + err.Error())
		} else {
			final = activated
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{
		"lot_id": final.Id, "channel_id": channel.Id, "status": final.Status,
		"deal_type": final.DealType, "revenue_share_pct": final.RevenueSharePct,
	}})
}

// supplierUsageWindow reads the half-open window a seller asked for, in whole
// days, defaulting to the last 30.
func supplierUsageWindow(c *gin.Context) (int64, int64) {
	const day = int64(86400)
	now := common.GetTimestamp()
	end := now + day
	start := end - 31*day
	if raw := c.Query("days"); raw != "" {
		if days, err := strconv.Atoi(raw); err == nil && days > 0 && days <= 366 {
			start = end - int64(days+1)*day
		}
	}
	return start, end
}

// sellerSharePct is the share the seller's live keys were taken on. Keys can
// in principle carry different shares; the highest is used so the figure this
// screen shows is never lower than what they are actually owed.
func sellerSharePct(lots []*model.CreditLot) float64 {
	share := 0.0
	for _, lot := range lots {
		if lot.RevenueSharePct > share {
			share = lot.RevenueSharePct
		}
	}
	return share
}

// GetSupplierUsageDetail is the seller's own audit trail: their traffic in the
// shape their vendor console reports it, so the two can be put side by side.
func GetSupplierUsageDetail(c *gin.Context) {
	supplier, ok := portalSupplier(c)
	if !ok {
		return
	}
	lots, err := model.GetCreditLots(model.CreditLotFilter{SupplierId: supplier.Id})
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	start, end := supplierUsageWindow(c)
	rows, err := model.GetSupplierUsageDetail(supplier.Id, start, end, sellerSharePct(lots))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	var listUSD, soldUSD, shareUSD float64
	var requests, unpriced int64
	for _, row := range rows {
		listUSD += row.ListUSD
		soldUSD += row.SoldUSD
		shareUSD += row.ShareUSD
		requests += row.Requests
		if !row.Priced {
			unpriced += row.Requests
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{
		"rows": rows,
		"totals": gin.H{
			"requests": requests, "list_usd": listUSD, "sold_usd": soldUSD,
			"share_usd": shareUSD, "unpriced_requests": unpriced,
		},
		"vendors": supplierVendorPresets(),
	}})
}

// ExportSupplierUsageCSV hands the same rows over as a file, so a seller can
// diff them against an export from their vendor rather than reading a screen.
func ExportSupplierUsageCSV(c *gin.Context) {
	supplier, ok := portalSupplier(c)
	if !ok {
		return
	}
	lots, err := model.GetCreditLots(model.CreditLotFilter{SupplierId: supplier.Id})
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	start, end := supplierUsageWindow(c)
	rows, err := model.GetSupplierUsageDetail(supplier.Id, start, end, sellerSharePct(lots))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	filename := fmt.Sprintf("unifyapi-usage-%s-%s.csv", supplier.Code, time.Unix(start, 0).Format("20060102"))
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	c.Data(http.StatusOK, "text/csv; charset=utf-8", []byte(model.SupplierUsageCSV(rows)))
}

func GetSupplierUsage(c *gin.Context) {
	supplier, ok := portalSupplier(c)
	if !ok {
		return
	}
	days, _ := strconv.Atoi(c.DefaultQuery("days", "30"))
	if days > 366 {
		days = 366
	}
	rows, err := model.GetSupplierDailyUsage(supplier.Id, days)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": rows})
}

// portalStatement is an issued settlement as the supplier should see it: the
// period, our figure, its status, and the per-model lines that explain it.
// Our internal note and the variance bookkeeping stay on our side.
type portalStatement struct {
	Id          int                     `json:"id"`
	PeriodStart string                  `json:"period_start"`
	PeriodEnd   string                  `json:"period_end"`
	AmountUSD   float64                 `json:"amount_usd"`
	Status      string                  `json:"status"`
	CreatedAt   int64                   `json:"created_at"`
	Lines       []service.StatementLine `json:"lines,omitempty"`
	Requests    int64                   `json:"requests"`
}

func GetSupplierStatements(c *gin.Context) {
	supplier, ok := portalSupplier(c)
	if !ok {
		return
	}
	settlements, err := model.ListSupplierSettlements(supplier)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	out := make([]portalStatement, 0, len(settlements))
	for _, settlement := range settlements {
		view := portalStatement{
			Id: settlement.Id, PeriodStart: settlement.PeriodStart, PeriodEnd: settlement.PeriodEnd,
			AmountUSD: settlement.AmountUSD, Status: settlement.Status, CreatedAt: settlement.CreatedAt,
		}
		var frozen service.Statement
		if err := common.Unmarshal([]byte(settlement.StatementJSON), &frozen); err == nil {
			view.Lines = frozen.Lines
			view.Requests = frozen.Requests
		}
		out = append(out, view)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": out})
}
