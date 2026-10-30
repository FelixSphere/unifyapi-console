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

	// Where the share is paid. Required before a seller can submit a key:
	// with revenue share nothing is paid up front, so the account has to be
	// on file before the first dollar is owed, not chased afterwards.
	// PayoutMethod is a rail id from PayoutRails() -- the same ids the wallet
	// uses for taking money in, so the seller reads one set of names across
	// the platform. Holder is the name on the account; Details is free text
	// for a wire and a canonical "<NETWORK>:<address>" on an on-chain rail.
	// Root reads them in full; nobody else does.
	PayoutMethod    string `json:"payout_method" gorm:"type:varchar(24)"`
	PayoutHolder    string `json:"payout_holder" gorm:"type:varchar(120)"`
	PayoutDetails   string `json:"payout_details" gorm:"type:varchar(500)"`
	PayoutCurrency  string `json:"payout_currency" gorm:"type:varchar(8)"`
	PayoutUpdatedAt int64  `json:"payout_updated_at" gorm:"not null;default:0"`
	Note            string `json:"note" gorm:"type:text"`
	CreatedAt       int64  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt       int64  `json:"updated_at" gorm:"autoUpdateTime"`

	// PayoutRailLabel is not stored: it is the rail's name, attached on read
	// so the operator's table shows "Binance.US" rather than the raw id, and
	// shows it from the same list the seller chose from.
	PayoutRailLabel string `json:"payout_rail_label" gorm:"-"`
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

// SupplierPayoutAccount is what a seller files before they can sell.
type SupplierPayoutAccount struct {
	Method   string `json:"method"`
	Holder   string `json:"holder"`
	Details  string `json:"details"`
	Currency string `json:"currency"`
}

// HasPayoutAccount reports whether we know where to send this seller's money.
// A rail that has since been switched off in Payment Settings still counts:
// the account is on file, and an operator can still send to it by hand. What
// must not happen is a seller being blocked from selling because somebody
// toggled a gateway.
func (s *CreditSupplier) HasPayoutAccount() bool {
	rail, ok := PayoutRailFor(s.PayoutMethod)
	if !ok {
		return false
	}
	if !rail.NeedsAccount {
		// Platform credit: the login is the account, so there must be one.
		return s.UserId > 0
	}
	return strings.TrimSpace(s.PayoutHolder) != "" && strings.TrimSpace(s.PayoutDetails) != ""
}

// PayoutMethodLabel names this supplier's rail for a screen.
func (s *CreditSupplier) PayoutMethodLabel() string { return PayoutMethodLabel(s.PayoutMethod) }

// ExternalPayoutMethod folds the seller's choice into the two ways a payout is
// booked: platform credit lands in the wallet, everything else is a transfer
// the operator makes and records.
func (s *CreditSupplier) ExternalPayoutMethod() string {
	if s.PayoutMethod == CreditLotPayoutPlatformCredit {
		return CreditLotPayoutPlatformCredit
	}
	return CreditLotPayoutExternal
}

// MaskedPayoutDetails keeps the tail so an operator can recognise the account
// in a list without the whole number on screen.
func (s *CreditSupplier) MaskedPayoutDetails() string {
	d := strings.TrimSpace(s.PayoutDetails)
	if len(d) <= 4 {
		return strings.Repeat("•", len(d))
	}
	return strings.Repeat("•", 6) + d[len(d)-4:]
}

// validatePayoutAccount checks the account against the rail it names and
// returns the account as it should be stored, so the caller never has to
// re-derive the canonical form.
func validatePayoutAccount(acct SupplierPayoutAccount) (SupplierPayoutAccount, error) {
	rail, ok := PayoutRailFor(acct.Method)
	if !ok {
		return acct, fmt.Errorf("%q is not a way we can pay you", acct.Method)
	}
	acct.Method = rail.Id
	if !rail.NeedsAccount {
		acct.Holder, acct.Details = "", ""
	} else if strings.TrimSpace(acct.Holder) == "" || strings.TrimSpace(acct.Details) == "" {
		return acct, errors.New("an account holder and the account details are required for that payout method")
	}
	if textLooksLikeProviderSecret(acct.Holder, acct.Details) {
		return acct, ErrCreditLotSecretInText
	}
	if rail.OnChain() {
		details, err := normalizeOnChainPayoutDetails(rail, acct.Details)
		if err != nil {
			return acct, err
		}
		acct.Details = details
	}
	if rail.Currency != "" {
		// The rail settles in one asset; letting the seller type another one
		// would only promise a currency the transfer cannot be made in.
		acct.Currency = rail.Currency
	}
	if acct.Currency != "" && !payoutCurrencyPattern.MatchString(acct.Currency) {
		return acct, errors.New("currency must be a code such as USD or EUR")
	}
	return acct, nil
}

var payoutCurrencyPattern = regexp.MustCompile(`^[A-Z]{3,5}$`)

// SetSupplierPayoutAccount files or replaces where a seller is paid.
//
// A rail that is switched off in Payment Settings cannot be newly chosen --
// there would be no account to send from. It can still be re-saved by whoever
// already filed it: refusing that would mean a seller who edits their address
// loses the rail they were being paid on because an operator toggled a gateway
// in the meantime.
func SetSupplierPayoutAccount(supplierId int, acct SupplierPayoutAccount) (*CreditSupplier, error) {
	acct.Method = NormalizePayoutMethod(acct.Method)
	acct.Holder = strings.TrimSpace(acct.Holder)
	acct.Details = strings.TrimSpace(acct.Details)
	acct.Currency = strings.ToUpper(strings.TrimSpace(acct.Currency))
	if acct.Method == "" {
		return nil, errors.New("choose how you want to be paid")
	}
	if acct.Currency == "" {
		acct.Currency = "USD"
	}
	acct, err := validatePayoutAccount(acct)
	if err != nil {
		return nil, err
	}
	rail, _ := PayoutRailFor(acct.Method)
	var supplier CreditSupplier
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).First(&supplier, "id = ?", supplierId).Error; err != nil {
			return err
		}
		if !rail.Available && NormalizePayoutMethod(supplier.PayoutMethod) != rail.Id {
			if rail.Reason != "" {
				return errors.New(rail.Reason)
			}
			return fmt.Errorf("%s is not available as a payout method right now", rail.Label)
		}
		if !rail.NeedsAccount && supplier.UserId <= 0 {
			return errors.New("platform credit needs a login to credit; this supplier has none")
		}
		supplier.PayoutMethod = acct.Method
		supplier.PayoutHolder = acct.Holder
		supplier.PayoutDetails = acct.Details
		supplier.PayoutCurrency = acct.Currency
		supplier.PayoutUpdatedAt = common.GetTimestamp()
		return tx.Save(&supplier).Error
	})
	if err != nil {
		return nil, err
	}
	return &supplier, nil
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
	s.PayoutMethod = NormalizePayoutMethod(s.PayoutMethod)
	s.PayoutCurrency = strings.ToUpper(strings.TrimSpace(s.PayoutCurrency))
	if s.PayoutMethod != "" {
		// An operator editing a supplier is not choosing a rail on the
		// seller's behalf, so availability is not checked here -- only that
		// whatever is on the row is still a coherent account.
		acct, err := validatePayoutAccount(SupplierPayoutAccount{
			Method: s.PayoutMethod, Holder: s.PayoutHolder,
			Details: s.PayoutDetails, Currency: s.PayoutCurrency,
		})
		if err != nil {
			return err
		}
		s.PayoutMethod, s.PayoutHolder = acct.Method, acct.Holder
		s.PayoutDetails, s.PayoutCurrency = acct.Details, acct.Currency
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

// EnsureCreditSupplierForUser returns the supplier record behind a login,
// creating an active one on first sale. There is no application step: the
// terms are posted, the key is verified automatically, and the operator's only
// decision is to pay.
func EnsureCreditSupplierForUser(user *User) (*CreditSupplier, error) {
	if user == nil || user.Id <= 0 {
		return nil, errors.New("sign in to sell credits")
	}
	if existing, err := GetCreditSupplierByUserId(user.Id); err == nil && existing != nil {
		return existing, nil
	}
	name := strings.TrimSpace(user.DisplayName)
	if name == "" {
		name = user.Username
	}
	supplier := &CreditSupplier{
		Name:         name,
		ContactEmail: strings.TrimSpace(user.Email),
		UserId:       user.Id,
		Status:       CreditSupplierStatusActive,
	}
	base := supplierCodeFromName(user.Username)
	err := DB.Transaction(func(tx *gorm.DB) error {
		// Two first sales from the same login at once must not create two
		// supplier records: lock the user row for the transaction.
		var owner User
		if err := lockForUpdate(tx).Select("id").First(&owner, "id = ?", user.Id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("sign in to sell credits")
			}
			return err
		}
		if err := ensureSupplierUserFree(tx, user.Id, 0); err != nil {
			return err
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
// UpdateCreditSupplier is a PATCH, not a replace. A caller that sends only a
// status must not silently unlink the supplier from their login (user_id 0)
// or blank their identity: omitted identity fields keep their value, and the
// merged record -- not the raw patch -- is what gets validated. To unlink a
// login deliberately, send user_id -1.
func UpdateCreditSupplier(id int, patch *CreditSupplier) error {
	err := DB.Transaction(func(tx *gorm.DB) error {
		var existing CreditSupplier
		if err := tx.First(&existing, "id = ?", id).Error; err != nil {
			return err
		}
		merged := existing
		if name := strings.TrimSpace(patch.Name); name != "" {
			merged.Name = name
		}
		if code := strings.TrimSpace(patch.Code); code != "" {
			merged.Code = code
		}
		if email := strings.TrimSpace(patch.ContactEmail); email != "" {
			merged.ContactEmail = email
		}
		switch {
		case patch.UserId < 0:
			merged.UserId = 0
		case patch.UserId > 0:
			merged.UserId = patch.UserId
		}
		if status := strings.TrimSpace(patch.Status); status != "" {
			merged.Status = status
		}
		merged.StatusReason = patch.StatusReason
		if merged.Status == CreditSupplierStatusActive {
			merged.StatusReason = ""
		}
		merged.PayoutTerms = patch.PayoutTerms
		merged.Note = patch.Note
		if patch.PayoutMethod != "" {
			merged.PayoutMethod = patch.PayoutMethod
			merged.PayoutHolder = patch.PayoutHolder
			merged.PayoutDetails = patch.PayoutDetails
			merged.PayoutCurrency = patch.PayoutCurrency
			merged.PayoutUpdatedAt = common.GetTimestamp()
		}
		if err := ValidateCreditSupplier(&merged); err != nil {
			return err
		}
		if err := ensureSupplierUserFree(tx, merged.UserId, id); err != nil {
			return err
		}
		return tx.Save(&merged).Error
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
	for _, supplier := range suppliers {
		supplier.PayoutRailLabel = supplier.PayoutMethodLabel()
	}
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
