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
	// The team name decides which customer an account is ENROLLED in, so it is
	// only consulted where enrollment is established. A read must never fail
	// because the caller named a team: the account already exists and the
	// customer that owns it is recorded on the link, which is the authority.
	//
	// Without this, turning the selector on from the Builder side locks out
	// every account that connected before teams existed -- they are enrolled in
	// the program default, the named team resolves to something else or to
	// nothing at all, and even reading a workspace answers 409.
	enrollmentName := ""
	switch c.Param("action") {
	case "connect", "claim":
		enrollmentName = request.CustomerName
	}
	selector := model.BuilderProgramSelector{ProgramName: request.ProgramName, PartnershipCode: request.PartnershipCode, CustomerName: enrollmentName}
	var offer *model.PartnershipOffer
	if request.ProgramName != "" {
		offer, err = model.ResolveBuilderProgram(model.DB, selector, false)
		// A team the Builder side names but that does not exist here is created
		// rather than refused. Only on enrollment: a read never provisions, so
		// a typo on a read cannot leave a stray customer and an empty invoice
		// behind.
		if enrollmentName != "" && errors.Is(err, model.ErrPartnershipCustomerUnavailable) {
			if provisionErr := model.ProvisionBuilderCustomer(request.ProgramName, enrollmentName); provisionErr != nil {
				c.JSON(http.StatusConflict, gin.H{"code": builderProgramConflictCode(provisionErr)})
				return
			}
			offer, err = model.ResolveBuilderProgram(model.DB, selector, false)
		}
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
		// Say which refusal this is. All three used to arrive as one generic
		// 403, which reached the user as "you do not have permission" and told
		// nobody what to do next.
		if errors.Is(err, model.ErrBuilderStaffAccount) {
			c.JSON(http.StatusConflict, gin.H{"code": "UNIFY_ACCOUNT_NOT_ELIGIBLE"})
			return
		}
		if errors.Is(err, model.ErrBuilderAccountDisabled) {
			c.JSON(http.StatusConflict, gin.H{"code": "UNIFY_ACCOUNT_DISABLED"})
			return
		}
		if errors.Is(err, model.ErrBuilderOwnershipProof) {
			c.JSON(http.StatusConflict, gin.H{"code": "UNIFY_OWNERSHIP_UNPROVEN"})
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
		code := "UNIFY_ACCOUNT_UNAVAILABLE"
		switch {
		case errors.Is(err, model.ErrBuilderStaffAccount):
			code = "UNIFY_ACCOUNT_NOT_ELIGIBLE"
		case errors.Is(err, model.ErrBuilderAccountDisabled):
			code = "UNIFY_ACCOUNT_DISABLED"
		}
		c.JSON(http.StatusForbidden, gin.H{"code": code})
		return
	}
	// connect validates the email while provisioning. Every other action only
	// identified the subject, so a signed address was never checked against the
	// account it resolves to. Verify it when supplied, so the caller's claim
	// about who this is has to agree with the linked account on every call.
	if request.Email != "" {
		if !request.EmailVerified {
			c.JSON(http.StatusForbidden, gin.H{"code": "UNIFY_EMAIL_UNVERIFIED"})
			return
		}
		if model.NormalizeEmail(request.Email) != model.NormalizeEmail(user.Email) {
			c.JSON(http.StatusConflict, gin.H{"code": "UNIFY_EMAIL_MISMATCH"})
			return
		}
	}
	// A binding to a program that no longer exists is dangling, not a conflict,
	// and it must not make an account unreadable. connect heals it; every other
	// action answers from the binding on record, which is the only authority
	// left once the old program is gone.
	if offer != nil && link.ProgramId != offer.Program.Id {
		stillThere, existsErr := model.PartnershipProgramExists(link.ProgramId)
		if existsErr != nil {
			c.JSON(http.StatusBadGateway, gin.H{"code": "UNIFY_READ_FAILED"})
			return
		}
		if stillThere {
			c.JSON(http.StatusConflict, gin.H{"code": "UNIFY_PROGRAM_UNAVAILABLE"})
			return
		}
	}
	// Only refuse on the customer where the caller is actually asking to be
	// enrolled in one. Comparing a read against the program default would
	// reject every account that legitimately belongs to a team.
	if offer != nil && enrollmentName != "" && link.CustomerId != offer.CustomerId {
		c.JSON(http.StatusConflict, gin.H{"code": "UNIFY_CUSTOMER_CONFLICT"})
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
