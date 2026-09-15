/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package controller

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/mail"
	"os"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type builderRequest struct {
	Subject         string `json:"subject"`
	PartnershipCode string `json:"partnership_code"`
	ProgramName     string `json:"program_name"`
	CustomerName    string `json:"customer_name"`
	Email           string `json:"email"`
	EmailVerified   bool   `json:"email_verified"`
	ManagementToken string `json:"management_token"`
	Range           string `json:"range"`
	OwnerEligible   bool   `json:"owner_eligible"`
	Amount          int64  `json:"amount"`
}

func readBuilderRequest(c *gin.Context) (*builderRequest, error) {
	secret := os.Getenv("BUILDER_INTEGRATION_SECRET")
	if len(secret) < 32 {
		return nil, errors.New("integration disabled")
	}
	timestamp := c.GetHeader("X-Builder-Timestamp")
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	now := time.Now().Unix()
	if err != nil || seconds < now-30 || seconds > now+30 {
		return nil, errors.New("expired assertion")
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 32769))
	if err != nil || len(body) > 32768 {
		return nil, errors.New("invalid body")
	}
	signature, err := hex.DecodeString(c.GetHeader("X-Builder-Signature"))
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(c.Request.Method + "\n" + c.Request.URL.Path + "\n" + timestamp + "\n"))
	mac.Write(body)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return nil, errors.New("invalid assertion")
	}
	var request builderRequest
	if err := common.Unmarshal(body, &request); err != nil {
		return nil, err
	}
	if request.Subject == "" || len(request.Subject) > 128 || (request.PartnershipCode == "") == (request.ProgramName == "") {
		return nil, errors.New("invalid identity or program")
	}
	if request.ProgramName != "" {
		// The signed name selects business data, not deployment configuration.
		// BuilderIntegration resolves it against the database before any action.
		if !model.ValidBuilderProgramName(request.ProgramName) {
			return nil, errors.New("invalid program name")
		}
	} else if request.PartnershipCode != os.Getenv("BUILDER_INTEGRATION_PARTNERSHIP_CODE") {
		// Preserve the existing pinned customer-code contract for legacy callers.
		return nil, errors.New("invalid partnership code")
	}
	return &request, nil
}

// builderProgramConflictCode keeps "the team cannot be used" distinct from "the
// program cannot be used", so a caller can tell which selector failed.
func builderProgramConflictCode(err error) string {
	switch {
	case errors.Is(err, model.ErrPartnershipCustomerConflict):
		return "UNIFY_CUSTOMER_CONFLICT"
	case errors.Is(err, model.ErrPartnershipCustomerUnavailable):
		return "UNIFY_CUSTOMER_UNAVAILABLE"
	default:
		return "UNIFY_PROGRAM_UNAVAILABLE"
	}
}

// BuilderIntegration accepts only server-signed assertions, never a browser's
// claimed email or user id. The integration is disabled without its secret.
func BuilderIntegration(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	request, err := readBuilderRequest(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"code": "UNIFY_UNAUTHORIZED"})
		return
	}
	// A malformed team name is the caller's mistake, not a missing customer, so
	// it is reported as a bad request rather than a conflict.
	if request.CustomerName != "" && !model.ValidBuilderProgramName(request.CustomerName) {
		c.JSON(http.StatusBadRequest, gin.H{"code": "UNIFY_CUSTOMER_INVALID"})
		return
	}
	selector := model.BuilderProgramSelector{ProgramName: request.ProgramName, PartnershipCode: request.PartnershipCode, CustomerName: request.CustomerName}
	var offer *model.PartnershipOffer
	if request.ProgramName != "" {
		offer, err = model.ResolveBuilderProgram(model.DB, selector, false)
		if err != nil {
			c.JSON(http.StatusConflict, gin.H{"code": builderProgramConflictCode(err)})
			return
		}
	}
	if c.Param("action") == "connect" {
		_, emailErr := mail.ParseAddress(request.Email)
		if !request.EmailVerified || request.Email == "" || len(request.Email) > 254 || emailErr != nil {
			c.JSON(http.StatusForbidden, gin.H{"code": "UNIFY_EMAIL_UNVERIFIED"})
			return
		}
		_, err := model.ConnectBuilderIdentityWithProgram(request.Subject, request.Email, selector, request.ManagementToken)
		if request.ProgramName != "" && (errors.Is(err, model.ErrPartnershipProgramUnavailable) ||
			errors.Is(err, model.ErrPartnershipCustomerUnavailable) ||
			errors.Is(err, model.ErrPartnershipCustomerConflict)) {
			c.JSON(http.StatusConflict, gin.H{"code": builderProgramConflictCode(err)})
			return
		}
		if errors.Is(err, model.ErrBuilderLinkRequired) {
			c.JSON(http.StatusConflict, gin.H{"code": "UNIFY_LINK_REQUIRED"})
			return
		}
		if err != nil {
			c.JSON(http.StatusForbidden, gin.H{"code": "UNIFY_CONNECT_FAILED"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"connected": true})
		return
	}
	if c.Param("action") == "checkout" && !isStripeTopUpEnabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "UNIFY_STRIPE_UNAVAILABLE"})
		return
	}
	link, user, err := model.GetBuilderIdentity(request.Subject)
	if errors.Is(err, gorm.ErrRecordNotFound) && c.Param("action") == "workspace" {
		c.JSON(http.StatusOK, gin.H{"connected": false})
		return
	}
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"code": "UNIFY_ACCOUNT_UNAVAILABLE"})
		return
	}
	if offer != nil && (link.ProgramId != offer.Program.Id || link.CustomerId != offer.CustomerId) {
		// Naming a team this identity is not enrolled in is a customer conflict,
		// not a missing program. Reads must not silently answer for another team.
		code := "UNIFY_PROGRAM_UNAVAILABLE"
		if request.CustomerName != "" {
			code = "UNIFY_CUSTOMER_CONFLICT"
		}
		c.JSON(http.StatusConflict, gin.H{"code": code})
		return
	}
	switch c.Param("action") {
	case "claim":
		if !request.OwnerEligible {
			c.JSON(http.StatusForbidden, gin.H{"code": "UNIFY_OWNER_REQUIRED"})
			return
		}
		if err := model.ClaimBuilderTeamGrantWithProgram(request.Subject, selector); err != nil {
			c.JSON(http.StatusConflict, gin.H{"code": "UNIFY_GRANT_UNAVAILABLE"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"claimed": true})
	case "credit-status":
		claimed, err := model.BuilderGrantStatus(link)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"code": "UNIFY_READ_FAILED"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"claimed": claimed})
	case "workspace":
		data, err := model.ReadBuilderWorkspace(link, user, request.Range)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"code": "UNIFY_READ_FAILED"})
			return
		}
		data["stripe_available"] = isStripeTopUpEnabled()
		c.JSON(http.StatusOK, data)
	case "checkout":
		if request.Amount < 1 || request.Amount > 10000 {
			c.JSON(http.StatusBadRequest, gin.H{"code": "UNIFY_AMOUNT_INVALID"})
			return
		}
		c.Set("id", user.Id)
		stripeAdaptor.RequestPay(c, &StripePayRequest{Amount: request.Amount, Currency: "USD", PaymentMethod: model.PaymentMethodStripe, SuccessURL: os.Getenv("BUILDER_INTEGRATION_RETURN_URL"), CancelURL: os.Getenv("BUILDER_INTEGRATION_RETURN_URL")})

	case "key":
		token, err := model.GetTokenByIds(link.TokenId, user.Id)
		if err != nil || token.Status != common.TokenStatusEnabled || (token.ExpiredTime != -1 && token.ExpiredTime < time.Now().Unix()) {
			c.JSON(http.StatusForbidden, gin.H{"code": "UNIFY_KEY_UNAVAILABLE"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"credential": "sk-" + token.Key})
	default:
		c.JSON(http.StatusNotFound, gin.H{"code": "UNIFY_ACTION_UNAVAILABLE"})
	}
}
