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
}

// portalLotView is a lot without the operator's note.
type portalLotView struct {
	Id               int     `json:"id"`
	Vendor           string  `json:"vendor"`
	ChannelId        int     `json:"channel_id"`
	ChannelName      string  `json:"channel_name"`
	FaceValueUSD     float64 `json:"face_value_usd"`
	AcquisitionRate  float64 `json:"acquisition_rate"`
	ConsumedUSD      float64 `json:"consumed_usd"`
	RemainingUSD     float64 `json:"remaining_usd"`
	PayableUSD       float64 `json:"payable_usd"`
	UnpricedRequests int64   `json:"unpriced_requests"`
	ExpiresAt        int64   `json:"expires_at"`
	Status           string  `json:"status"`
	StatusReason     string  `json:"status_reason"`
	Source           string  `json:"source"`
	RetiredAt        int64   `json:"retired_at"`
	CreatedAt        int64   `json:"created_at"`
	VerifiedAt       int64   `json:"verified_at"`
	PayoutMethod     string  `json:"payout_method"`
	PayoutUSD        float64 `json:"payout_usd"`
	PaidUSD          float64 `json:"paid_usd"`
	PaidAt           int64   `json:"paid_at"`
	PayoutReference  string  `json:"payout_reference"`
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
	terms := model.GetCreditSupplyTerms()
	var face, consumed, remaining, awaiting, paid float64
	for _, lot := range lots {
		views = append(views, portalLotView{
			Id: lot.Id, Vendor: lot.Vendor, ChannelId: lot.ChannelId, ChannelName: channelNames[lot.ChannelId],
			FaceValueUSD: lot.FaceValueUSD, AcquisitionRate: lot.AcquisitionRate, ConsumedUSD: lot.ConsumedUSD,
			RemainingUSD: lot.RemainingUSD(), PayableUSD: lot.PayableUSD(), UnpricedRequests: lot.UnpricedRequests,
			ExpiresAt: lot.ExpiresAt, Status: lot.Status, StatusReason: lot.StatusReason, Source: lot.Source, RetiredAt: lot.RetiredAt, CreatedAt: lot.CreatedAt,
			VerifiedAt: lot.VerifiedAt, PayoutMethod: lot.PayoutMethod,
			PayoutUSD: terms.PayoutUSD(lot.FaceValueUSD, lot.AcquisitionRate, lot.PayoutMethod),
			PaidUSD:   lot.PaidUSD, PaidAt: lot.PaidAt, PayoutReference: lot.PayoutReference,
		})
		if lot.Status == model.CreditLotStatusRejected {
			continue
		}
		face += lot.FaceValueUSD
		consumed += lot.ConsumedUSD
		remaining += lot.RemainingUSD()
		if lot.Status == model.CreditLotStatusVerified {
			awaiting += terms.PayoutUSD(lot.FaceValueUSD, lot.AcquisitionRate, lot.PayoutMethod)
		}
		paid += lot.PaidUSD
	}
	totals["face_usd"] = face
	totals["consumed_usd"] = consumed
	totals["remaining_usd"] = remaining
	totals["awaiting_payment_usd"] = awaiting
	totals["paid_usd"] = paid

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{
		"supplier": portalSupplierView{
			Id: supplier.Id, Name: supplier.Name, Code: supplier.Code, ContactEmail: supplier.ContactEmail,
			Status: supplier.Status, StatusReason: supplier.StatusReason, Counterparty: supplier.CounterpartyKey(),
		},
		"lots":    views,
		"totals":  totals,
		"vendors": supplierVendorPresets(),
		"terms": gin.H{
			"buy_rates":             terms.BuyRates,
			"min_face_usd":          terms.MinFaceUSD,
			"platform_credit_bonus": terms.PlatformCreditBonus,
		},
	}})
}

// supplierLotSubmission is what a supplier may propose. The rate is their
// asking price; the operator may edit it before approving.
// GetSupplierTerms is the price list a seller sees before typing anything.
func GetSupplierTerms(c *gin.Context) {
	terms := model.GetCreditSupplyTerms()
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{
		"buy_rates":             terms.BuyRates,
		"min_face_usd":          terms.MinFaceUSD,
		"platform_credit_bonus": terms.PlatformCreditBonus,
		"channel_priority":      terms.ChannelPriority,
		"vendors":               supplierVendorPresets(),
	}})
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

type supplierLotSubmission struct {
	Vendor          string   `json:"vendor"`
	FaceValueUSD    float64  `json:"face_value_usd"`
	AcquisitionRate float64  `json:"acquisition_rate"`
	ExpiresAt       int64    `json:"expires_at"`
	Note            string   `json:"note"`
	UpstreamKey     string   `json:"upstream_key"`
	Models          []string `json:"models"`
	// PayoutMethod is platform_credit (instant on settlement) or external;
	// PayoutAccount is where an external payment should go, in the seller's words.
	PayoutMethod  string `json:"payout_method"`
	PayoutAccount string `json:"payout_account"`
	// TransferRightsConfirmed is the supplier's own attestation. It does not
	// replace the operator's check at approval; it puts the claim on record.
	TransferRightsConfirmed bool `json:"transfer_rights_confirmed"`
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
	rate, buying := terms.BuyRate(preset.Key)
	if !buying {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "we are not buying " + preset.Label + " credits at the moment"})
		return
	}
	if req.FaceValueUSD < terms.MinFaceUSD {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("the smallest sale we accept is $%.0f of credit", terms.MinFaceUSD)})
		return
	}
	req.PayoutMethod = strings.TrimSpace(req.PayoutMethod)
	req.PayoutAccount = strings.TrimSpace(req.PayoutAccount)
	switch req.PayoutMethod {
	case model.CreditLotPayoutPlatformCredit:
	case model.CreditLotPayoutExternal:
		if req.PayoutAccount == "" {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "tell us where to send the payment (bank, PayPal, or wallet details)"})
			return
		}
	default:
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "choose how you want to be paid"})
		return
	}
	supplier, ok := sellerFor(c)
	if !ok {
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
		Vendor:          preset.Key,
		FaceValueUSD:    req.FaceValueUSD,
		AcquisitionRate: rate, // the posted rate, never the caller's number
		ExpiresAt:       req.ExpiresAt,
		Note:            strings.TrimSpace(req.Note),
		PayoutMethod:    req.PayoutMethod,
		PayoutAccount:   req.PayoutAccount,
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
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{
		"lot_id": verified.Id, "channel_id": channel.Id, "status": verified.Status,
		"payout_usd": terms.PayoutUSD(verified.FaceValueUSD, verified.AcquisitionRate, verified.PayoutMethod),
	}})
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
