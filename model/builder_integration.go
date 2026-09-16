/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// BuilderIdentity links a trusted Builder subject to one personal account.
// Email is used only when provisioning; it is never an authentication lookup.
type BuilderIdentity struct {
	Id             int    `gorm:"primaryKey"`
	Subject        string `gorm:"type:varchar(128);not null;uniqueIndex"`
	UserId         int    `gorm:"not null;uniqueIndex"`
	ProgramId      int    `gorm:"not null;index"`
	CustomerId     int    `gorm:"not null"`
	TokenId        int    `gorm:"not null"`
	CreatedAt      int64  `gorm:"autoCreateTime"`
	GrantQuota     int    `gorm:"not null;default:0"`
	GrantClaimedAt int64  `gorm:"not null;default:0"`
}

var ErrBuilderLinkRequired = errors.New("existing account ownership verification required")
var ErrBuilderUnavailable = errors.New("builder account unavailable")

// One opaque error used to cover a staff account, a disabled account, a
// mismatched management token and a plain lookup failure. They need completely
// different responses -- "use another address", "contact support", "your token
// is wrong" -- and the caller could not tell them apart, so every one of them
// reached the user as "you do not have permission".
//
// A staff account is refused on purpose: an administrator is not a self-serve
// billing subject, and binding one would make the same identity both the
// operator of the console and a customer inside it.
// Each wraps ErrBuilderUnavailable, so callers that only care that the account
// is unusable keep working unchanged, while callers that can act on the reason
// can now tell these apart.
// The program lookup had one error for four different situations: no program
// by that name, several, one that is disabled, and one outside its schedule.
// A caller seeing "program unavailable" could not tell a typo in configuration
// from an expired campaign, and the commonest cause by far -- the configured
// name simply not matching -- looked identical to an outage.
var ErrPartnershipProgramNotFound = fmt.Errorf("no partnership program has that name: %w", ErrPartnershipProgramUnavailable)
var ErrPartnershipProgramAmbiguous = fmt.Errorf("several partnership programs share that name: %w", ErrPartnershipProgramUnavailable)
var ErrPartnershipProgramInactive = fmt.Errorf("the partnership program is disabled or outside its schedule: %w", ErrPartnershipProgramUnavailable)

var ErrBuilderStaffAccount = fmt.Errorf("administrator accounts cannot be linked to Builder: %w", ErrBuilderUnavailable)
var ErrBuilderAccountDisabled = fmt.Errorf("the linked account is disabled: %w", ErrBuilderUnavailable)
var ErrBuilderOwnershipProof = fmt.Errorf("management token does not prove ownership of that address: %w", ErrBuilderUnavailable)

// builderAccountRefusal says which of those applies to a user record.
func builderAccountRefusal(user *User) error {
	if user == nil {
		return ErrBuilderUnavailable
	}
	if IsStaffRole(user.Role) {
		return ErrBuilderStaffAccount
	}
	if user.Status != common.UserStatusEnabled {
		return ErrBuilderAccountDisabled
	}
	return nil
}

// ErrPartnershipCustomerUnavailable separates "this named customer cannot be
// used" from "this program cannot be used", so a caller naming a team that is
// missing, ambiguous or disabled is told which of the two failed.
var ErrPartnershipCustomerUnavailable = errors.New("partnership customer unavailable")

// ErrPartnershipCustomerConflict marks an identity already enrolled with a
// different customer. Repointing it moves a person's usage to another invoice,
// so it is an administrative act rather than a side effect of a request field.
var ErrPartnershipCustomerConflict = errors.New("partnership customer conflict")

// BuilderProgramSelector keeps program names separate from legacy registration codes.
// CustomerName selects one customer within the program; without it the program's
// default customer is used, which is the behaviour every existing caller relies on.
type BuilderProgramSelector struct {
	ProgramName     string
	PartnershipCode string
	CustomerName    string
}

func ValidBuilderProgramName(name string) bool {
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 120 || strings.Trim(name, "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff") != name {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// ResolveBuilderProgram selects exactly one existing program and its default customer.
// Compare names in Go as database collations may be case/accent insensitive.
func ResolveBuilderProgram(tx *gorm.DB, selector BuilderProgramSelector, lock bool) (*PartnershipOffer, error) {
	if selector.ProgramName == "" {
		if selector.PartnershipCode == "" {
			return nil, ErrPartnershipProgramUnavailable
		}
		// A customer name selects within a program, so it has no meaning for the
		// legacy code lookup, which already resolves one specific customer.
		if selector.CustomerName != "" {
			return nil, ErrPartnershipCustomerUnavailable
		}
		return getPartnershipOfferByCode(tx, selector.PartnershipCode, lock)
	}
	if selector.PartnershipCode != "" || !ValidBuilderProgramName(selector.ProgramName) {
		return nil, ErrPartnershipProgramUnavailable
	}
	if selector.CustomerName != "" && !ValidBuilderProgramName(selector.CustomerName) {
		return nil, ErrPartnershipCustomerUnavailable
	}
	// Each lookup needs its own statement. lockForUpdate returns a non-clone
	// handle, so sharing one across both Find calls leaks the first query's
	// resolved table and conditions into the second.
	query := func() *gorm.DB {
		session := tx.Session(&gorm.Session{})
		if lock {
			return lockForUpdate(session)
		}
		return session
	}
	var candidates []PartnershipProgram
	if err := query().Where("name = ? AND removed_at = ?", selector.ProgramName, 0).
		Find(&candidates).Error; err != nil {
		return nil, err
	}
	var matches []PartnershipProgram
	for _, candidate := range candidates {
		if candidate.Name == selector.ProgramName {
			matches = append(matches, candidate)
		}
	}
	switch {
	case len(matches) == 0:
		return nil, ErrPartnershipProgramNotFound
	case len(matches) > 1:
		return nil, ErrPartnershipProgramAmbiguous
	case !partnershipProgramActive(&matches[0], time.Now().Unix()):
		return nil, ErrPartnershipProgramInactive
	}
	program := matches[0]
	customer, err := resolveProgramCustomer(query, program.Id, selector.CustomerName)
	if err != nil {
		return nil, err
	}
	return &PartnershipOffer{Program: program, CustomerId: customer.Id, CustomerName: customer.Name, CustomerCode: customer.Code, CustomerGroup: customer.Group}, nil
}

// resolveProgramCustomer selects one live customer inside a program: the one
// named, or the program default when no name is given. Names are not unique in
// the schema, so an ambiguous name is refused rather than resolved arbitrarily.
// Compare in Go, as database collations may be case or accent insensitive.
func resolveProgramCustomer(query func() *gorm.DB, programId int, name string) (*PartnershipCustomer, error) {
	if name == "" {
		var customers []PartnershipCustomer
		if err := query().Where("program_id = ? AND is_default = ? AND removed_at = ?", programId, true, 0).Find(&customers).Error; err != nil {
			return nil, err
		}
		if len(customers) != 1 || !customers[0].Enabled {
			return nil, ErrPartnershipProgramUnavailable
		}
		return &customers[0], nil
	}
	var candidates []PartnershipCustomer
	if err := query().Where("program_id = ? AND name = ? AND removed_at = ?", programId, name, 0).Find(&candidates).Error; err != nil {
		return nil, err
	}
	var matches []PartnershipCustomer
	for _, candidate := range candidates {
		if candidate.Name == name {
			matches = append(matches, candidate)
		}
	}
	if len(matches) != 1 || !matches[0].Enabled {
		return nil, ErrPartnershipCustomerUnavailable
	}
	return &matches[0], nil
}

func GetBuilderIdentity(subject string) (*BuilderIdentity, *User, error) {
	var link BuilderIdentity
	if err := DB.Where("subject = ?", subject).First(&link).Error; err != nil {
		return nil, nil, err
	}
	user, err := GetUserById(link.UserId, false)
	if err != nil {
		return nil, nil, err
	}
	if refusal := builderAccountRefusal(user); refusal != nil {
		return nil, nil, refusal
	}
	return &link, user, nil
}

// ConnectBuilderIdentity requires the caller to have verified the Builder email.
// An existing UnifyAPI account additionally requires its management credential.
func ConnectBuilderIdentity(subject, email, code, managementToken string) (*BuilderIdentity, error) {
	return ConnectBuilderIdentityWithProgram(subject, email, BuilderProgramSelector{PartnershipCode: code}, managementToken)
}

// enrolledElsewhere reports an identity bound to a different PROGRAM, which is
// a genuine dead end -- nothing here can serve it.
//
// A different CUSTOMER inside the same program is not an error. Connect is
// idempotent: it means "make sure this subject has an account", and the
// account already exists. Refusing locked out every identity that connected
// before team names started arriving, since they are all enrolled in the
// program default while the Builder side now names their team.
//
// The protection this replaces was against silently repointing an enrollment.
// That is preserved by leaving the enrollment alone, not by failing the call.
// Moving a person between customers moves their usage onto another invoice and
// stays an administrative act.
func enrolledElsewhere(link *BuilderIdentity, offer *PartnershipOffer) bool {
	return link.ProgramId != offer.Program.Id
}

// programStillExists reports whether the program an identity is bound to is
// still there.
//
// A binding that points at a deleted program is dangling, not a conflict.
// Refusing it locks the person out permanently with no self-service path:
// they cannot reach the old program, and the address they would reconnect
// with is already held by the account that binding belongs to. Only an
// administrator could rescue them, which is how this reached production.
func programStillExists(tx *gorm.DB, programId int) (bool, error) {
	var count int64
	if err := tx.Model(&PartnershipProgram{}).
		Where("id = ? AND removed_at = ?", programId, 0).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// PartnershipProgramExists reports whether a program id is still present, so
// callers outside this package can tell a dangling binding from a conflict.
func PartnershipProgramExists(programId int) (bool, error) {
	return programStillExists(DB, programId)
}

// rebindDanglingIdentity moves an identity whose program is gone onto the
// program resolving now, together with the member's pricing group.
//
// This is the one case where repointing is right. Everywhere else it is
// refused, because moving somebody between live customers moves their usage
// onto another invoice.
func rebindDanglingIdentity(tx *gorm.DB, link *BuilderIdentity, offer *PartnershipOffer) error {
	if err := tx.Model(&BuilderIdentity{}).Where("id = ?", link.Id).
		Updates(map[string]any{"program_id": offer.Program.Id, "customer_id": offer.CustomerId}).Error; err != nil {
		return err
	}
	if err := tx.Model(&User{}).Where("id = ?", link.UserId).
		Update("group", offer.CustomerGroup).Error; err != nil {
		return err
	}
	var enrollment PartnershipEnrollment
	err := tx.Where("program_id = ? AND user_id = ?", offer.Program.Id, link.UserId).First(&enrollment).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return tx.Create(&PartnershipEnrollment{
			ProgramId: offer.Program.Id, CustomerId: offer.CustomerId,
			CustomerGroup: offer.CustomerGroup, UserId: link.UserId,
		}).Error
	}
	if err != nil {
		return err
	}
	return tx.Model(&PartnershipEnrollment{}).Where("id = ?", enrollment.Id).
		Updates(map[string]any{"customer_id": offer.CustomerId, "customer_group": offer.CustomerGroup}).Error
}

func ConnectBuilderIdentityWithProgram(subject, email string, selector BuilderProgramSelector, managementToken string) (*BuilderIdentity, error) {
	offer, err := ResolveBuilderProgram(DB, selector, false)
	if err != nil {
		return nil, err
	}

	if link, _, err := GetBuilderIdentity(subject); err == nil {
		if !enrolledElsewhere(link, offer) {
			return link, nil
		}
		// Let the transaction decide: it can tell a live conflict from a
		// dangling binding, and heal the second.
		if exists, err := programStillExists(DB, link.ProgramId); err != nil {
			return nil, err
		} else if exists {
			return nil, ErrPartnershipProgramUnavailable
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	var existingID int
	if managementToken != "" {
		owner, err := ValidateAccessToken(managementToken)
		if err != nil || owner == nil || NormalizeEmail(owner.Email) != NormalizeEmail(email) {
			return nil, ErrBuilderOwnershipProof
		}
		if refusal := builderAccountRefusal(owner); refusal != nil {
			return nil, refusal
		}
		existingID = owner.Id
	}
	var link BuilderIdentity
	err = DB.Transaction(func(tx *gorm.DB) error {
		return withNormalizedEmailLock(tx, email, func(tx *gorm.DB) error {
			offer, err := ResolveBuilderProgram(tx, selector, true)
			if err != nil {
				return err
			}
			if err := tx.Where("subject = ?", subject).First(&link).Error; err == nil {
				if enrolledElsewhere(&link, offer) {
					exists, err := programStillExists(tx, link.ProgramId)
					if err != nil {
						return err
					}
					if exists {
						return ErrPartnershipProgramUnavailable
					}
					return rebindDanglingIdentity(tx, &link, offer)
				}
				return nil
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			var user User
			if existingID > 0 {
				if err := lockForUpdate(tx).First(&user, existingID).Error; err != nil {
					return err
				}
				if NormalizeEmail(user.Email) != NormalizeEmail(email) {
					return ErrBuilderOwnershipProof
				}
				if refusal := builderAccountRefusal(&user); refusal != nil {
					return refusal
				}
			} else {
				var count int64
				if err := emailQuery(tx, email).Count(&count).Error; err != nil {
					return err
				}
				if count > 0 {
					return ErrBuilderLinkRequired
				}
				user = User{Username: "builder_" + common.GetRandomString(12), Email: NormalizeEmail(email), DisplayName: "Builder", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: offer.CustomerGroup, Quota: 0, AffCode: common.GetRandomString(4)}
				if err := user.prepareForInsert(tx); err != nil {
					return err
				}
				if err := tx.Create(&user).Error; err != nil {
					return err
				}
				if _, err := EnsureTenantForUserTx(tx, user.Id); err != nil {
					return err
				}
			}
			var enrollment PartnershipEnrollment
			if err := tx.Where("program_id = ? AND user_id = ?", offer.Program.Id, user.Id).First(&enrollment).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				if err := tx.Create(&PartnershipEnrollment{ProgramId: offer.Program.Id, CustomerId: offer.CustomerId, CustomerGroup: offer.CustomerGroup, UserId: user.Id}).Error; err != nil {
					return err
				}
			} else if err != nil {
				return err
			}
			key, err := common.GenerateKey()
			if err != nil {
				return err
			}
			token := Token{UserId: user.Id, Key: key, Status: common.TokenStatusEnabled, Name: "Builder API", CreatedTime: time.Now().Unix(), AccessedTime: time.Now().Unix(), ExpiredTime: -1, UnlimitedQuota: true}
			if err := tx.Create(&token).Error; err != nil {
				return err
			}
			link = BuilderIdentity{Subject: subject, UserId: user.Id, ProgramId: offer.Program.Id, CustomerId: offer.CustomerId, TokenId: token.Id}
			return tx.Create(&link).Error
		})
	})
	return &link, err
}

// ReadBuilderWorkspace returns a deliberately small projection; provider logs,
// management credentials and payment identifiers never cross this boundary.
func ReadBuilderWorkspace(link *BuilderIdentity, user *User, period string) (map[string]any, error) {
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -30).Unix()
	if period == "7d" {
		since = now.AddDate(0, 0, -7).Unix()
	}
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Unix()
	if period == "current_month" {
		since = month
	}
	quota, err := GetUserQuota(user.Id, true)
	if err != nil {
		return nil, err
	}
	token, err := GetTokenByIds(link.TokenId, user.Id)
	if err != nil {
		return nil, err
	}
	var rows []Log
	if err := LOG_DB.Select("id", "created_at", "type", "model_name", "quota", "prompt_tokens", "completion_tokens", "use_time").Where("user_id = ? AND type IN ? AND created_at >= ?", user.Id, []int{LogTypeConsume, LogTypeError}, since).Order("id desc").Limit(100).Find(&rows).Error; err != nil {
		return nil, err
	}
	var totals struct {
		Requests     int64 `json:"requests"`
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	}
	if err := LOG_DB.Model(&Log{}).Select("count(*) AS requests, coalesce(sum(prompt_tokens),0) AS input_tokens, coalesce(sum(completion_tokens),0) AS output_tokens").Where("user_id = ? AND type IN ? AND created_at >= ?", user.Id, []int{LogTypeConsume, LogTypeError}, since).Scan(&totals).Error; err != nil {
		return nil, err
	}
	var monthly int64
	if err := LOG_DB.Model(&Log{}).Select("coalesce(sum(quota),0)").Where("user_id = ? AND type = ? AND created_at >= ?", user.Id, LogTypeConsume, month).Scan(&monthly).Error; err != nil {
		return nil, err
	}
	activity := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		outcome := "success"
		if row.Type == LogTypeError {
			outcome = "error"
		}
		activity = append(activity, map[string]any{"id": row.Id, "timestamp": time.Unix(row.CreatedAt, 0).UTC().Format(time.RFC3339), "model": row.ModelName, "outcome": outcome, "duration_ms": row.UseTime * 1000, "cost": float64(row.Quota) / common.QuotaPerUnit})
	}
	var topups []TopUp
	if err := DB.Where("user_id = ? AND status = ? AND payment_method = ?", user.Id, common.TopUpStatusSuccess, PaymentMethodStripe).Order("id desc").Limit(100).Find(&topups).Error; err != nil {
		return nil, err
	}
	credits := make([]map[string]any, 0, len(topups))
	for _, topup := range topups {
		credits = append(credits, map[string]any{"id": topup.Id, "amount": topup.Money, "timestamp": time.Unix(topup.CompleteTime, 0).UTC().Format(time.RFC3339), "description": "Account top-up"})
	}
	// Negative IDs distinguish launch receipts from positive Stripe top-up IDs.
	if link.GrantClaimedAt > 0 {
		credits = append(credits, map[string]any{"id": -link.Id, "amount": float64(link.GrantQuota) / common.QuotaPerUnit, "timestamp": time.Unix(link.GrantClaimedAt, 0).UTC().Format(time.RFC3339), "description": "Team launch credit"})
	}
	models := []string{}
	if err := DB.Table("abilities").Where(commonGroupCol+" = ? AND enabled = ?", user.Group, true).Distinct("model").Order("model").Pluck("model", &models).Error; err != nil {
		return nil, err
	}
	if models == nil {
		models = []string{}
	}
	return map[string]any{
		"connected": true, "account": map[string]any{"id": user.Id, "email": user.Email, "name": user.DisplayName, "group": user.Group},
		"balance": float64(quota) / common.QuotaPerUnit, "monthly_usage": float64(monthly) / common.QuotaPerUnit,
		"key":    map[string]any{"id": token.Id, "masked": token.GetMaskedKey(), "active": token.Status == common.TokenStatusEnabled && (token.ExpiredTime == -1 || token.ExpiredTime > now.Unix()), "created_at": time.Unix(token.CreatedTime, 0).UTC().Format(time.RFC3339)},
		"models": models, "activity": activity, "totals": totals, "credits": credits,
	}, nil
}

// BuilderGrantStatus includes an earlier partnership signup grant so linking
// an already-funded account cannot stack another launch grant.
func BuilderGrantStatus(link *BuilderIdentity) (bool, error) {
	if link.GrantClaimedAt > 0 {
		return true, nil
	}
	var enrollment PartnershipEnrollment
	err := DB.Where("program_id = ? AND user_id = ?", link.ProgramId, link.UserId).First(&enrollment).Error
	return enrollment.GrantedQuota > 0, err
}

// ClaimBuilderTeamGrant treats the immutable, unique Builder subject as the
// team owner. No product identifier participates in grant uniqueness.
func ClaimBuilderTeamGrant(subject, code string) error {
	return ClaimBuilderTeamGrantWithProgram(subject, BuilderProgramSelector{PartnershipCode: code})
}

func ClaimBuilderTeamGrantWithProgram(subject string, selector BuilderProgramSelector) error {
	var userID int
	err := DB.Transaction(func(tx *gorm.DB) error {
		offer, err := ResolveBuilderProgram(tx, selector, true)
		if err != nil {
			return err
		}
		var link BuilderIdentity
		if err := lockForUpdate(tx).Where("subject = ?", subject).First(&link).Error; err != nil {
			return err
		}
		userID = link.UserId
		if offer.Program.Id != link.ProgramId || offer.CustomerId != link.CustomerId {
			return ErrBuilderUnavailable
		}
		if link.GrantClaimedAt > 0 {
			return nil
		}
		var user User
		if err := lockForUpdate(tx).First(&user, link.UserId).Error; err != nil {
			return err
		}
		if user.Status != common.UserStatusEnabled || IsStaffRole(user.Role) {
			return ErrBuilderUnavailable
		}
		var enrollment PartnershipEnrollment
		if err := lockForUpdate(tx).Where("program_id = ? AND user_id = ?", link.ProgramId, link.UserId).First(&enrollment).Error; err != nil {
			return err
		}
		if enrollment.GrantedQuota > 0 {
			return nil
		}
		quota, err := common.QuotaFromFloatStrict(10 * common.QuotaPerUnit)
		if err != nil || quota <= 0 || offer.Program.GrantQuota != quota {
			return ErrBuilderUnavailable
		}
		result := tx.Model(&PartnershipProgram{}).Where("id = ? AND claimed_count < grant_limit", link.ProgramId).UpdateColumn("claimed_count", gorm.Expr("claimed_count + 1"))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrBuilderUnavailable
		}
		if _, err := IncreaseUserQuotaWithTx(tx, link.UserId, quota); err != nil {
			return err
		}
		if err := tx.Model(&enrollment).Update("granted_quota", quota).Error; err != nil {
			return err
		}
		return tx.Model(&link).Updates(map[string]any{"grant_quota": quota, "grant_claimed_at": time.Now().Unix()}).Error
	})
	if err == nil {
		_ = InvalidateBillingQuotaCacheForUser(userID)
	}
	return err
}
