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

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// Tenant is the billing boundary: the thing that holds a balance and owns API
// keys. Upstream new-api has no such concept -- one user row is simultaneously
// an identity, a billing entity, and a key owner -- which makes it impossible
// for two people to share one balance.
//
// Every user gets a tenant at registration, so "even a single customer is a
// tenant" holds structurally from day one. That matters because retrofitting a
// billing boundary after real customers exist means migrating a live ledger.
//
// Deliberately additive: users.tenant_id is nullable-by-convention (0 means
// "none"), and every code path falls back to the user's own row when it is 0.
// An untenanted user therefore behaves exactly as upstream does, which keeps
// the merge surface small and makes the change safe to ship incrementally.
type Tenant struct {
	Id   int    `json:"id"`
	Name string `json:"name" gorm:"type:varchar(120);index" validate:"max=120"`
	// Slug is the stable external handle, so URLs and support tickets do not
	// depend on a numeric id.
	Slug      string `json:"slug" gorm:"type:varchar(64);uniqueIndex"`
	Status    int    `json:"status" gorm:"type:int;default:1"`
	OwnerId   int    `json:"owner_id" gorm:"type:int;index"`
	Quota     int    `json:"quota" gorm:"type:int;default:0"`
	UsedQuota int    `json:"used_quota" gorm:"type:int;default:0;column:used_quota"`
	// Group mirrors User.Group so pricing tiers can eventually be a tenant
	// property rather than a per-member one. Not yet consulted by the relay.
	Group     string         `json:"group" gorm:"type:varchar(64);default:'default'"`
	Remark    string         `json:"remark,omitempty" gorm:"type:varchar(255)" validate:"max=255"`
	CreatedAt int64          `json:"created_at" gorm:"autoCreateTime;column:created_at"`
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

const TenantStatusEnabled = 1
const TenantStatusDisabled = 2

// slugFromName produces a URL-safe handle. Not required to be pretty -- only
// stable and unique; uniqueness is enforced by the caller retrying with a
// suffix, because the DB has the authoritative unique index.
func slugFromName(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		default:
			// Collapse any run of non-alphanumerics (including multi-byte CJK,
			// which would otherwise vanish entirely) into a single dash.
			if !lastDash && b.Len() > 0 {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > 48 {
		slug = strings.Trim(slug[:48], "-")
	}
	// A username of only CJK or punctuation reduces to nothing, so fall back to
	// something guaranteed non-empty rather than colliding on "".
	if slug == "" {
		slug = "tenant"
	}
	return slug
}

func GetTenantById(id int) (*Tenant, error) {
	if id == 0 {
		return nil, errors.New("tenant id is empty")
	}
	tenant := &Tenant{}
	if err := DB.First(tenant, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return tenant, nil
}

func GetTenantBySlug(slug string) (*Tenant, error) {
	if slug == "" {
		return nil, errors.New("tenant slug is empty")
	}
	tenant := &Tenant{}
	if err := DB.First(tenant, "slug = ?", slug).Error; err != nil {
		return nil, err
	}
	return tenant, nil
}

// CreateTenantWithTx inserts a tenant, resolving slug collisions by suffixing.
// Runs inside the caller's transaction so registration stays atomic: a user
// must never end up committed without a billing entity.
func CreateTenantWithTx(tx *gorm.DB, tenant *Tenant) error {
	if tenant.Slug == "" {
		tenant.Slug = slugFromName(tenant.Name)
	}
	base := tenant.Slug
	for attempt := 0; attempt < 6; attempt++ {
		err := tx.Create(tenant).Error
		if err == nil {
			return nil
		}
		if !isDuplicateKeyError(err) {
			return err
		}
		// Reset the id gorm may have populated, then retry with a new slug.
		tenant.Id = 0
		tenant.Slug = fmt.Sprintf("%s-%s", base, strings.ToLower(common.GetRandomString(4)))
	}
	return errors.New("failed to allocate a unique tenant slug")
}

func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	// gorm's translated-error support is not enabled on every dialect in this
	// codebase, so fall back to matching the driver text.
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate")
}

// EnsureTenantForUserTx gives a user a tenant if they do not already have one,
// and is safe to call repeatedly -- registration, a backfill, and an admin
// action can all invoke it without creating duplicates.
//
// The new tenant takes over the user's starting quota so the balance lives in
// exactly one place. Leaving a non-zero users.quota behind would create two
// competing balances and a reconciliation problem later.
func EnsureTenantForUserTx(tx *gorm.DB, userId int) (*Tenant, error) {
	if userId == 0 {
		return nil, errors.New("user id is empty")
	}

	var user User
	if err := tx.First(&user, "id = ?", userId).Error; err != nil {
		return nil, err
	}
	if user.TenantId != 0 {
		tenant := &Tenant{}
		if err := tx.First(tenant, "id = ?", user.TenantId).Error; err != nil {
			return nil, err
		}
		return tenant, nil
	}

	name := user.DisplayName
	if name == "" {
		name = user.Username
	}

	tenant := &Tenant{
		Name:    name,
		Slug:    slugFromName(user.Username),
		Status:  TenantStatusEnabled,
		OwnerId: user.Id,
		Quota:   user.Quota,
		Group:   user.Group,
	}
	if err := CreateTenantWithTx(tx, tenant); err != nil {
		return nil, err
	}

	// Move the balance onto the tenant in the same transaction.
	if err := tx.Model(&User{}).Where("id = ?", user.Id).
		Updates(map[string]any{"tenant_id": tenant.Id, "quota": 0}).Error; err != nil {
		return nil, err
	}

	return tenant, nil
}

// EnsureTenantForUser is the non-transactional entry point used by the
// registration hook.
func EnsureTenantForUser(userId int) (*Tenant, error) {
	var tenant *Tenant
	err := DB.Transaction(func(tx *gorm.DB) error {
		created, err := EnsureTenantForUserTx(tx, userId)
		if err != nil {
			return err
		}
		tenant = created
		return nil
	})
	if err != nil {
		return nil, err
	}
	return tenant, nil
}

// AddUserToTenant moves an existing user into a tenant, folding their personal
// balance into the tenant's. This is the membership primitive an invite flow
// will call once it exists.
func AddUserToTenant(userId int, tenantId int) error {
	if userId == 0 || tenantId == 0 {
		return errors.New("user id and tenant id are required")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.First(&user, "id = ?", userId).Error; err != nil {
			return err
		}
		if user.TenantId == tenantId {
			return nil
		}
		var tenant Tenant
		if err := tx.First(&tenant, "id = ?", tenantId).Error; err != nil {
			return err
		}
		if user.Quota != 0 {
			if err := tx.Model(&Tenant{}).Where("id = ?", tenantId).
				Update("quota", gorm.Expr("quota + ?", user.Quota)).Error; err != nil {
				return err
			}
		}
		return tx.Model(&User{}).Where("id = ?", userId).
			Updates(map[string]any{"tenant_id": tenantId, "quota": 0}).Error
	})
}

func GetTenantMembers(tenantId int) ([]*User, error) {
	if tenantId == 0 {
		return nil, errors.New("tenant id is empty")
	}
	var users []*User
	err := DB.Where("tenant_id = ?", tenantId).Order("id asc").Find(&users).Error
	return users, err
}

// ---------------------------------------------------------------------------
// Tenant balance
// ---------------------------------------------------------------------------

func GetTenantQuota(tenantId int) (quota int, err error) {
	err = DB.Model(&Tenant{}).Where("id = ?", tenantId).Select("quota").Find(&quota).Error
	return quota, err
}

func increaseTenantQuota(tenantId int, quota int) error {
	return DB.Model(&Tenant{}).Where("id = ?", tenantId).
		Update("quota", gorm.Expr("quota + ?", quota)).Error
}

func decreaseTenantQuota(tenantId int, quota int) error {
	return DB.Model(&Tenant{}).Where("id = ?", tenantId).
		Update("quota", gorm.Expr("quota - ?", quota)).Error
}

func increaseTenantUsedQuota(tenantId int, quota int) error {
	return DB.Model(&Tenant{}).Where("id = ?", tenantId).
		Update("used_quota", gorm.Expr("used_quota + ?", quota)).Error
}
