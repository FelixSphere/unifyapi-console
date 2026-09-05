/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

// UNIFYAPI-FORK: the supply side of the credit supply.
//
// A supplier is a third party who holds vendor credits (OpenAI, Anthropic, ...)
// and sells us the right to consume them. We route customer traffic through a
// channel that carries the supplier's upstream key, draw the lot down at the
// vendor's LIST price -- because that is the denomination the vendor's own
// credit balance decrements in -- and owe the supplier the face value consumed
// multiplied by the agreed acquisition rate.
//
// That last number is exactly what ChannelCostRatio already means (see
// setting/ratio_setting/unifyapi_channel_cost.go), so activating a lot writes
// the rate into the channel's cost ratio and the existing reconciliation,
// profit and vendor-settlement screens become correct for supplier traffic
// without a second cost model.
//
// Nothing here moves money. A supplier is paid outside the system; the vendor
// settlement row (model/settlement.go, kind "vendor", counterparty
// "supplier:<code>") is the record that it happened.

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

var (
	creditSupplierCodePattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{2,63}$`)
	ErrCreditSupplierUserTaken = errors.New("that login already manages another supplier")
)

const (
	// A supplier applies (pending), is approved (active) or turned down
	// (rejected); an active supplier can be suspended and reinstated.
	CreditSupplierStatusPending   = "pending"
	CreditSupplierStatusActive    = "active"
	CreditSupplierStatusSuspended = "suspended"
	CreditSupplierStatusRejected  = "rejected"

	// creditSupplierCounterpartyPrefix namespaces supplier settlement keys so
	// they can never collide with the host-derived vendor keys in
	// service.UpstreamVendor ("anthropic", "openrouter", ...).
	creditSupplierCounterpartyPrefix = "supplier:"
)

// CreditSupplier is one counterparty we buy credits from.
type CreditSupplier struct {
	Id           int    `json:"id" gorm:"primaryKey"`
	Name         string `json:"name" gorm:"type:varchar(120);not null"`
	Code         string `json:"code" gorm:"type:varchar(64);not null;uniqueIndex"`
	ContactEmail string `json:"contact_email" gorm:"type:varchar(120)"`
	// UserId is the console login allowed to use the supplier portal for this
	// supplier. 0 means the supplier is admin-managed only. At most one supplier
	// per login, enforced in code because a unique index would reject the many
	// zeros.
	UserId int    `json:"user_id" gorm:"not null;default:0;index"`
	Status string `json:"status" gorm:"type:varchar(16);not null;default:'active';index"`
	// StatusReason is what the applicant reads when rejected or suspended.
	StatusReason string `json:"status_reason" gorm:"type:varchar(500)"`
	// Attestation made by the applicant in the portal: they own or control the
	// vendor accounts they intend to offer. Empty on operator-created suppliers.
	AttestationVersion string `json:"attestation_version" gorm:"type:varchar(64)"`
	AttestedAt         int64  `json:"attested_at" gorm:"not null;default:0"`
	// PayoutTerms is how this supplier gets paid, in words ("monthly wire, net
	// 15", "USDC on request"). It is operator memory, not payment credentials:
	// account numbers do not belong here and the portal never shows it.
	PayoutTerms string `json:"payout_terms" gorm:"type:text"`
	Note        string `json:"note" gorm:"type:text"`
	CreatedAt   int64  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt   int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

// CounterpartyKey is the stable id this supplier is settled under.
func (s *CreditSupplier) CounterpartyKey() string {
	return creditSupplierCounterpartyPrefix + s.Code
}

// IsSupplierCounterparty reports whether a settlement counterparty belongs to a
// credit-pool supplier rather than a host-derived vendor.
func IsSupplierCounterparty(counterparty string) bool {
	return strings.HasPrefix(counterparty, creditSupplierCounterpartyPrefix)
}

func ValidateCreditSupplier(s *CreditSupplier) error {
	s.Name = strings.TrimSpace(s.Name)
	s.Code = strings.ToLower(strings.TrimSpace(s.Code))
	s.ContactEmail = strings.TrimSpace(s.ContactEmail)
	s.PayoutTerms = strings.TrimSpace(s.PayoutTerms)
	s.Note = strings.TrimSpace(s.Note)
	if s.Status == "" {
		s.Status = CreditSupplierStatusActive
	}
	if s.Name == "" || !creditSupplierCodePattern.MatchString(s.Code) {
		return errors.New("name and a valid code (lowercase letters, digits, - or _) are required")
	}
	if s.ContactEmail != "" && !strings.Contains(s.ContactEmail, "@") {
		return errors.New("contact email is not an email address")
	}
	if s.UserId < 0 {
		return errors.New("user id cannot be negative")
	}
	switch s.Status {
	case CreditSupplierStatusPending, CreditSupplierStatusActive, CreditSupplierStatusSuspended, CreditSupplierStatusRejected:
	default:
		return errors.New("status must be pending, active, suspended or rejected")
	}
	s.StatusReason = strings.TrimSpace(s.StatusReason)
	if (s.Status == CreditSupplierStatusRejected || s.Status == CreditSupplierStatusSuspended) && s.StatusReason == "" {
		return errors.New("a reason is required when rejecting or suspending a supplier")
	}
	if textLooksLikeProviderSecret(s.PayoutTerms, s.Note, s.StatusReason) {
		return ErrCreditLotSecretInText
	}
	return nil
}

// CreditSupplierApplication is what an ordinary login submits to become a
// supplier. No credentials: those come later, per lot, once approved.
type CreditSupplierApplication struct {
	Name         string `json:"name"`
	ContactEmail string `json:"contact_email"`
	// Note is the applicant's own description: which vendors, roughly how much,
	// how the credits were obtained.
	Note     string `json:"note"`
	Attested bool   `json:"attested"`
}

// supplierCodeFromName derives a slug; the caller uniquifies it.
func supplierCodeFromName(name string) string {
	slug := strings.ToLower(strings.TrimSpace(name))
	slug = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-_")
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-_")
	}
	if len(slug) < 3 {
		slug = "supplier-" + slug
	}
	return strings.Trim(slug, "-_")
}

// ApplyForCreditSupplier records a pending supplier for the calling login. One
// application per login; the operator approves it from Billing -> Credit
// Supply, after which the login can submit lots.
func ApplyForCreditSupplier(userId int, input CreditSupplierApplication) (*CreditSupplier, error) {
	if userId <= 0 {
		return nil, errors.New("sign in to apply")
	}
	if !input.Attested {
		return nil, errors.New("confirm that you own or control the vendor accounts you intend to offer")
	}
	supplier := &CreditSupplier{
		Name:               strings.TrimSpace(input.Name),
		ContactEmail:       strings.TrimSpace(input.ContactEmail),
		Note:               strings.TrimSpace(input.Note),
		UserId:             userId,
		Status:             CreditSupplierStatusPending,
		AttestationVersion: CreditLotAttestationVersion,
		AttestedAt:         common.GetTimestamp(),
	}
	if supplier.Name == "" || supplier.ContactEmail == "" {
		return nil, errors.New("a company or team name and a contact email are required")
	}
	base := supplierCodeFromName(supplier.Name)
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := ensureSupplierUserFree(tx, userId, 0); err != nil {
			return errors.New("this login already has a supplier application or account")
		}
		supplier.Code = base
		for attempt := 2; attempt < 50; attempt++ {
			var count int64
			if err := tx.Model(&CreditSupplier{}).Where("code = ?", supplier.Code).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				break
			}
			supplier.Code = fmt.Sprintf("%s-%d", base, attempt)
		}
		if err := ValidateCreditSupplier(supplier); err != nil {
			return err
		}
		return tx.Create(supplier).Error
	})
	if err != nil {
		return nil, err
	}
	invalidateChannelSupplierIndex()
	return supplier, nil
}

func ensureSupplierUserFree(tx *gorm.DB, userId int, exceptSupplierId int) error {
	if userId == 0 {
		return nil
	}
	var count int64
	err := tx.Model(&CreditSupplier{}).
		Where("user_id = ? AND id <> ?", userId, exceptSupplierId).
		Count(&count).Error
	if err != nil {
		return err
	}
	if count > 0 {
		return ErrCreditSupplierUserTaken
	}
	return nil
}

func CreateCreditSupplier(s *CreditSupplier) error {
	if err := ValidateCreditSupplier(s); err != nil {
		return err
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := ensureSupplierUserFree(tx, s.UserId, 0); err != nil {
			return err
		}
		return tx.Create(s).Error
	})
	if err != nil {
		return err
	}
	invalidateChannelSupplierIndex()
	return nil
}

// UpdateCreditSupplier replaces the editable fields. The id, timestamps and any
// lots are untouched; renaming a supplier does not rename issued settlements,
// which carry their own frozen label.
func UpdateCreditSupplier(id int, patch *CreditSupplier) error {
	if err := ValidateCreditSupplier(patch); err != nil {
		return err
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		var existing CreditSupplier
		if err := tx.First(&existing, "id = ?", id).Error; err != nil {
			return err
		}
		if err := ensureSupplierUserFree(tx, patch.UserId, id); err != nil {
			return err
		}
		existing.Name = patch.Name
		existing.Code = patch.Code
		existing.ContactEmail = patch.ContactEmail
		existing.UserId = patch.UserId
		existing.Status = patch.Status
		existing.StatusReason = patch.StatusReason
		if existing.Status == CreditSupplierStatusActive {
			existing.StatusReason = ""
		}
		existing.PayoutTerms = patch.PayoutTerms
		existing.Note = patch.Note
		return tx.Save(&existing).Error
	})
	if err != nil {
		return err
	}
	invalidateChannelSupplierIndex()
	return nil
}

func GetCreditSuppliers() ([]*CreditSupplier, error) {
	var suppliers []*CreditSupplier
	err := DB.Order("id asc").Find(&suppliers).Error
	return suppliers, err
}

func GetCreditSupplierById(id int) (*CreditSupplier, error) {
	var supplier CreditSupplier
	if err := DB.First(&supplier, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &supplier, nil
}

// GetCreditSupplierByUserId is the portal's identity check: the supplier a
// login is allowed to see, or gorm.ErrRecordNotFound.
func GetCreditSupplierByUserId(userId int) (*CreditSupplier, error) {
	if userId <= 0 {
		return nil, gorm.ErrRecordNotFound
	}
	var supplier CreditSupplier
	if err := DB.First(&supplier, "user_id = ?", userId).Error; err != nil {
		return nil, err
	}
	return &supplier, nil
}
