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
	"regexp"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrPartnershipProgramUnavailable = errors.New("partnership program is unavailable")
	ErrPartnershipCustomerMismatch   = errors.New("account belongs to a different customer group")
	partnershipCodePattern           = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{2,63}$`)
)

const partnershipGroupIntegrityLockKey = "unifyapi-partnership-group-integrity"

// PartnershipProgram is an operator-managed registration offer. It only
// controls account creation: normal top-up and request billing remain unchanged.
type PartnershipProgram struct {
	Id           int                   `json:"id" gorm:"primaryKey"`
	Name         string                `json:"name" gorm:"type:varchar(120);not null"`
	Code         string                `json:"code" gorm:"type:varchar(64);not null;uniqueIndex"`
	Group        string                `json:"group" gorm:"type:varchar(64);not null"`
	GrantQuota   int                   `json:"grant_quota" gorm:"type:int;not null;default:0"`
	GrantLimit   int                   `json:"grant_limit" gorm:"type:int;not null;default:0"`
	ClaimedCount int                   `json:"claimed_count" gorm:"type:int;not null;default:0"`
	Enabled      bool                  `json:"enabled" gorm:"not null;default:false;index"`
	StartsAt     int64                 `json:"starts_at" gorm:"not null;default:0"`
	EndsAt       int64                 `json:"ends_at" gorm:"not null;default:0"`
	CreatedAt    int64                 `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt    int64                 `json:"updated_at" gorm:"autoUpdateTime"`
	Customers    []PartnershipCustomer `json:"customers,omitempty" gorm:"foreignKey:ProgramId"`
}

// PartnershipCustomer is one billable customer inside a Program. Group is
// deliberately an existing Pricing/User Group: settlement owns that group as
// one customer, while every User assigned to it is only a member.
type PartnershipCustomer struct {
	Id        int    `json:"id" gorm:"primaryKey"`
	ProgramId int    `json:"program_id" gorm:"not null;index;uniqueIndex:idx_partnership_customer_group"`
	Name      string `json:"name" gorm:"type:varchar(120);not null"`
	Code      string `json:"code" gorm:"type:varchar(64);not null;uniqueIndex"`
	Group     string `json:"group" gorm:"type:varchar(64);not null;uniqueIndex:idx_partnership_customer_group"`
	IsDefault bool   `json:"is_default" gorm:"not null;default:false;index"`
	Enabled   bool   `json:"enabled" gorm:"not null;default:true;index"`
	CreatedAt int64  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

type PartnershipEnrollment struct {
	Id            int    `json:"id" gorm:"primaryKey"`
	ProgramId     int    `json:"program_id" gorm:"not null;index;uniqueIndex:idx_partnership_user"`
	CustomerId    int    `json:"customer_id" gorm:"not null;default:0;index"`
	UserId        int    `json:"user_id" gorm:"not null;index;uniqueIndex:idx_partnership_user"`
	CustomerGroup string `json:"customer_group" gorm:"type:varchar(64);not null;default:'';index"`
	GrantedQuota  int    `json:"granted_quota" gorm:"type:int;not null;default:0"`
	CreatedAt     int64  `json:"created_at" gorm:"autoCreateTime"`
}

const PartnershipStatusConnectedExisting = "connected_existing"

type PartnershipConnectionResult struct {
	Status       string `json:"status"`
	ProgramCode  string `json:"program_code"`
	ProgramGroup string `json:"program_group"`
	UserGroup    string `json:"user_group"`
}

type PartnershipOffer struct {
	Program       PartnershipProgram
	CustomerId    int
	CustomerName  string
	CustomerCode  string
	CustomerGroup string
}

func NormalizePartnershipCode(code string) string {
	return strings.ToLower(strings.TrimSpace(code))
}

func ValidatePartnershipProgram(program *PartnershipProgram) error {
	program.Name = strings.TrimSpace(program.Name)
	program.Code = NormalizePartnershipCode(program.Code)
	program.Group = strings.TrimSpace(program.Group)
	if program.Name == "" || !partnershipCodePattern.MatchString(program.Code) || program.Group == "" {
		return errors.New("name, valid code, and group are required")
	}
	if program.GrantQuota < 0 || program.GrantLimit < 0 {
		return errors.New("grant quota and limit cannot be negative")
	}
	if program.EndsAt != 0 && program.StartsAt != 0 && program.EndsAt <= program.StartsAt {
		return errors.New("end time must be after start time")
	}
	return nil
}

func ValidatePartnershipCustomer(customer *PartnershipCustomer) error {
	customer.Name = strings.TrimSpace(customer.Name)
	customer.Code = NormalizePartnershipCode(customer.Code)
	customer.Group = strings.TrimSpace(customer.Group)
	if customer.Name == "" || !partnershipCodePattern.MatchString(customer.Code) || customer.Group == "" {
		return errors.New("customer name, valid registration code, and group are required")
	}
	return nil
}

func partnershipProgramActive(program *PartnershipProgram, now int64) bool {
	return program.Enabled &&
		(program.StartsAt == 0 || program.StartsAt <= now) &&
		(program.EndsAt == 0 || program.EndsAt > now)
}

func IsPartnershipProgramActive(program *PartnershipProgram, now int64) bool {
	return partnershipProgramActive(program, now)
}

func GetPartnershipPrograms() ([]PartnershipProgram, error) {
	var programs []PartnershipProgram
	err := DB.Preload("Customers", func(db *gorm.DB) *gorm.DB {
		return db.Order("is_default DESC, created_at ASC, id ASC")
	}).Order("created_at DESC, id DESC").Find(&programs).Error
	return programs, err
}

func GetPartnershipProgramByCode(code string) (*PartnershipProgram, error) {
	var program PartnershipProgram
	err := DB.Where("code = ?", NormalizePartnershipCode(code)).First(&program).Error
	return &program, err
}

// initializePartnershipCustomers upgrades single-customer Program rows created
// before PartnershipCustomer existed. It also snapshots the customer mapping
// onto historical enrollments so later Program edits cannot rewrite accounting
// attribution for already-connected users.
func initializePartnershipCustomers() error {
	if !DB.Migrator().HasTable(&PartnershipProgram{}) ||
		!DB.Migrator().HasTable(&PartnershipCustomer{}) ||
		!DB.Migrator().HasTable(&PartnershipEnrollment{}) {
		return nil
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var programs []PartnershipProgram
		if err := tx.Find(&programs).Error; err != nil {
			return err
		}
		for _, program := range programs {
			var customer PartnershipCustomer
			err := tx.Where("program_id = ? AND is_default = ?", program.Id, true).
				First(&customer).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				customer = PartnershipCustomer{
					ProgramId: program.Id, Name: program.Name, Code: program.Code,
					Group: program.Group, IsDefault: true, Enabled: true,
				}
				if err := tx.Create(&customer).Error; err != nil {
					return fmt.Errorf("backfill default customer for partnership program %d: %w", program.Id, err)
				}
			} else if err != nil {
				return err
			}
			if err := tx.Model(&PartnershipEnrollment{}).
				Where("program_id = ? AND customer_id = ?", program.Id, 0).
				Updates(map[string]any{
					"customer_id": customer.Id, "customer_group": customer.Group,
				}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func CreatePartnershipProgram(program *PartnershipProgram) error {
	if err := ValidatePartnershipProgram(program); err != nil {
		return err
	}
	program.Id = 0
	program.ClaimedCount = 0
	return DB.Transaction(func(tx *gorm.DB) error {
		return withPartnershipGroupIntegrityLock(tx, func(tx *gorm.DB) error {
			if err := validatePartnershipProgramGroup(tx, program.Group); err != nil {
				return err
			}
			if err := validatePartnershipCodeAvailable(tx, program.Code, 0, 0); err != nil {
				return err
			}
			if err := tx.Create(program).Error; err != nil {
				return err
			}
			return tx.Create(&PartnershipCustomer{
				ProgramId: program.Id, Name: program.Name, Code: program.Code,
				Group: program.Group, IsDefault: true, Enabled: true,
			}).Error
		})
	})
}

func UpdatePartnershipProgram(id int, input *PartnershipProgram) error {
	if id <= 0 {
		return errors.New("invalid partnership program id")
	}
	if err := ValidatePartnershipProgram(input); err != nil {
		return err
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		return withPartnershipGroupIntegrityLock(tx, func(tx *gorm.DB) error {
			if err := validatePartnershipProgramGroup(tx, input.Group); err != nil {
				return err
			}
			var current PartnershipProgram
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, id).Error; err != nil {
				return err
			}
			if input.GrantLimit < current.ClaimedCount {
				return fmt.Errorf("grant limit cannot be lower than claimed count %d", current.ClaimedCount)
			}
			var defaultCustomer PartnershipCustomer
			err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("program_id = ? AND is_default = ?", current.Id, true).
				First(&defaultCustomer).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err := validatePartnershipCodeAvailable(tx, input.Code, current.Id, defaultCustomer.Id); err != nil {
				return err
			}
			if err := tx.Model(&current).Updates(map[string]any{
				"name":        input.Name,
				"code":        input.Code,
				"group":       input.Group,
				"grant_quota": input.GrantQuota,
				"grant_limit": input.GrantLimit,
				"enabled":     input.Enabled,
				"starts_at":   input.StartsAt,
				"ends_at":     input.EndsAt,
			}).Error; err != nil {
				return err
			}
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return tx.Create(&PartnershipCustomer{
					ProgramId: current.Id, Name: input.Name, Code: input.Code,
					Group: input.Group, IsDefault: true, Enabled: true,
				}).Error
			}
			if err != nil {
				return err
			}
			return tx.Model(&defaultCustomer).Updates(map[string]any{
				"name": input.Name, "code": input.Code, "group": input.Group,
			}).Error
		})
	})
}

func CreatePartnershipCustomer(programId int, customer *PartnershipCustomer) error {
	if programId <= 0 {
		return errors.New("invalid partnership program id")
	}
	if err := ValidatePartnershipCustomer(customer); err != nil {
		return err
	}
	customer.Id = 0
	customer.ProgramId = programId
	customer.IsDefault = false
	return DB.Transaction(func(tx *gorm.DB) error {
		return withPartnershipGroupIntegrityLock(tx, func(tx *gorm.DB) error {
			if err := validatePartnershipProgramGroup(tx, customer.Group); err != nil {
				return err
			}
			var program PartnershipProgram
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&program, programId).Error; err != nil {
				return err
			}
			if err := validatePartnershipCodeAvailable(tx, customer.Code, 0, 0); err != nil {
				return err
			}
			return tx.Create(customer).Error
		})
	})
}

func UpdatePartnershipCustomer(programId, customerId int, input *PartnershipCustomer) error {
	if programId <= 0 || customerId <= 0 {
		return errors.New("invalid partnership customer id")
	}
	if err := ValidatePartnershipCustomer(input); err != nil {
		return err
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		return withPartnershipGroupIntegrityLock(tx, func(tx *gorm.DB) error {
			if err := validatePartnershipProgramGroup(tx, input.Group); err != nil {
				return err
			}
			var program PartnershipProgram
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&program, programId).Error; err != nil {
				return err
			}
			var current PartnershipCustomer
			if err := tx.Where("id = ? AND program_id = ?", customerId, programId).First(&current).Error; err != nil {
				return err
			}
			if current.IsDefault {
				return errors.New("edit the default customer through the Program settings")
			}
			if err := validatePartnershipCodeAvailable(tx, input.Code, 0, current.Id); err != nil {
				return err
			}
			return tx.Model(&current).Updates(map[string]any{
				"name": input.Name, "code": input.Code, "group": input.Group, "enabled": input.Enabled,
			}).Error
		})
	})
}

// Program codes and customer codes share one public URL namespace. The two
// tables intentionally duplicate a Program's code for its default customer,
// so callers may exclude that exact pair while still rejecting every other
// cross-table collision.
func validatePartnershipCodeAvailable(tx *gorm.DB, code string, programId, customerId int) error {
	var programCount int64
	programQuery := tx.Model(&PartnershipProgram{}).Where("code = ?", code)
	if programId > 0 {
		programQuery = programQuery.Where("id <> ?", programId)
	}
	if err := programQuery.Count(&programCount).Error; err != nil {
		return err
	}
	if programCount > 0 {
		return fmt.Errorf("registration code %q is already in use", code)
	}

	var customerCount int64
	customerQuery := tx.Model(&PartnershipCustomer{}).Where("code = ?", code)
	if customerId > 0 {
		customerQuery = customerQuery.Where("id <> ?", customerId)
	}
	if err := customerQuery.Count(&customerCount).Error; err != nil {
		return err
	}
	if customerCount > 0 {
		return fmt.Errorf("registration code %q is already in use", code)
	}
	return nil
}

// withPartnershipGroupIntegrityLock serializes GroupRatio replacement with
// program creation, enabling, and group moves. Both paths validate while the
// lock is held, so two concurrent root requests cannot commit a dangling group
// reference. SQLite's single-writer transaction model supplies the equivalent
// serialization for local development and tests.
func withPartnershipGroupIntegrityLock(tx *gorm.DB, fn func(tx *gorm.DB) error) error {
	switch {
	case common.UsingMainDatabase(common.DatabaseTypePostgreSQL):
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", partnershipGroupIntegrityLockKey).Error; err != nil {
			return err
		}
	case common.UsingMainDatabase(common.DatabaseTypeMySQL):
		var option Option
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("key = ?", "GroupRatio").Find(&option).Error; err != nil {
			return err
		}
	}
	return fn(tx)
}

func validatePartnershipProgramGroup(tx *gorm.DB, group string) error {
	var option Option
	err := tx.Where("key = ?", "GroupRatio").First(&option).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Fresh installations may still be using the built-in in-memory
		// GroupRatio without having saved that option. Materialize it while the
		// integrity lock is held so Program writes have one durable source.
		option = Option{Key: "GroupRatio", Value: ratio_setting.GroupRatio2JSONString()}
		err = tx.Create(&option).Error
	}
	if err != nil {
		return fmt.Errorf("read Group Pricing: %w", err)
	}
	var groups map[string]float64
	if err := common.Unmarshal([]byte(option.Value), &groups); err != nil {
		return fmt.Errorf("parse Group Pricing: %w", err)
	}
	if _, ok := groups[group]; !ok {
		return fmt.Errorf("group %q does not exist in Group Pricing", group)
	}
	return nil
}

func getPartnershipOfferByCode(tx *gorm.DB, code string, lock bool) (*PartnershipOffer, error) {
	normalized := NormalizePartnershipCode(code)
	var customer PartnershipCustomer
	err := tx.Where("code = ?", normalized).First(&customer).Error
	if err == nil {
		var program PartnershipProgram
		programQuery := tx
		if lock {
			programQuery = programQuery.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		if err := programQuery.First(&program, customer.ProgramId).Error; err != nil {
			return nil, err
		}
		if lock {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&customer, customer.Id).Error; err != nil {
				return nil, err
			}
			if customer.Code != normalized || customer.ProgramId != program.Id {
				return nil, ErrPartnershipProgramUnavailable
			}
		}
		if !customer.Enabled || !partnershipProgramActive(&program, time.Now().Unix()) {
			return nil, ErrPartnershipProgramUnavailable
		}
		return &PartnershipOffer{
			Program: program, CustomerId: customer.Id, CustomerName: customer.Name,
			CustomerCode: customer.Code, CustomerGroup: customer.Group,
		}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	// Compatibility for rows created before Partnership customers existed.
	// Fresh writes always create a default customer, but an older Program link
	// can still be used safely as its original single-customer group.
	var program PartnershipProgram
	programQuery := tx
	if lock {
		programQuery = programQuery.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := programQuery.Where("code = ?", normalized).First(&program).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrPartnershipProgramUnavailable
		}
		return nil, err
	}
	if !partnershipProgramActive(&program, time.Now().Unix()) {
		return nil, ErrPartnershipProgramUnavailable
	}
	return &PartnershipOffer{
		Program: program, CustomerName: program.Name, CustomerCode: program.Code,
		CustomerGroup: program.Group,
	}, nil
}

func GetActivePartnershipOfferByCode(code string) (*PartnershipOffer, error) {
	return getPartnershipOfferByCode(DB, code, false)
}

// ConnectExistingUserToPartnership records attribution without granting signup
// credit. It refuses a customer-link mismatch: silently associating an account
// from another group would make the Program attribution disagree with the
// customer invoice owner.
func ConnectExistingUserToPartnership(userId int, code string) (*PartnershipConnectionResult, error) {
	var connection PartnershipConnectionResult
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, userId).Error; err != nil {
			return err
		}
		offer, err := getPartnershipOfferByCode(tx, code, true)
		if err != nil {
			return err
		}
		if user.Group != offer.CustomerGroup {
			return ErrPartnershipCustomerMismatch
		}
		var enrollment PartnershipEnrollment
		err = tx.Where("program_id = ? AND user_id = ?", offer.Program.Id, user.Id).First(&enrollment).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = tx.Create(&PartnershipEnrollment{
				ProgramId: offer.Program.Id, CustomerId: offer.CustomerId,
				CustomerGroup: offer.CustomerGroup, UserId: user.Id, GrantedQuota: 0,
			}).Error
		}
		if err != nil {
			return err
		}
		connection = PartnershipConnectionResult{
			Status: PartnershipStatusConnectedExisting, ProgramCode: offer.Program.Code,
			ProgramGroup: offer.CustomerGroup, UserGroup: user.Group,
		}
		return nil
	})
	return &connection, err
}

// ValidateActivePartnershipGroups prevents Group Pricing updates from
// removing a group referenced by an enabled program. Groups remain the single
// source of truth; programs hold only the identifier.
func ValidateActivePartnershipGroups(groups map[string]struct{}) error {
	if DB == nil || !DB.Migrator().HasTable(&PartnershipProgram{}) {
		return nil
	}
	return validateActivePartnershipGroups(DB, groups)
}

func validateActivePartnershipGroups(db *gorm.DB, groups map[string]struct{}) error {
	var programs []PartnershipProgram
	if err := db.Where("enabled = ?", true).Find(&programs).Error; err != nil {
		return err
	}
	for _, program := range programs {
		if _, ok := groups[program.Group]; !ok {
			return fmt.Errorf("group %q is used by an enabled partnership program; disable or move the program first", program.Group)
		}
	}
	var customers []PartnershipCustomer
	if db.Migrator().HasTable(&PartnershipCustomer{}) {
		if err := db.Table("partnership_customers AS pc").
			Select("pc.*").
			Joins("JOIN partnership_programs AS pp ON pp.id = pc.program_id").
			Where("pc.enabled = ? AND pp.enabled = ?", true, true).
			Find(&customers).Error; err != nil {
			return err
		}
	}
	for _, customer := range customers {
		if _, ok := groups[customer.Group]; !ok {
			return fmt.Errorf("group %q is used by an enabled partnership customer; disable or move the customer first", customer.Group)
		}
	}
	return nil
}

// InsertForPartnership applies the program identity and optional registration
// grant atomically with user creation. Capacity exhaustion is not an error: the
// user keeps the configured group and starts with zero quota, then uses the
// ordinary payment flow.
func (user *User) InsertForPartnership(code string) (int, error) {
	grantedQuota := 0
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		grantedQuota, err = user.InsertForPartnershipWithTx(tx, code)
		return err
	})
	if err != nil {
		return 0, err
	}
	// Partnership grants are the only signup credit on this path. Keep the
	// ordinary affiliate relationship for attribution, but do not stack the
	// generic invitee/inviter rewards on top of a capped Program offer.
	user.finishInsertWithInitialQuota(0, grantedQuota, "Partnership registration grant")
	return grantedQuota, nil
}

func (user *User) InsertForPartnershipWithTx(tx *gorm.DB, code string) (int, error) {
	var grantedQuota int
	err := withNormalizedEmailLock(tx, user.Email, func(tx *gorm.DB) error {
		offer, err := getPartnershipOfferByCode(tx, code, true)
		if err != nil {
			return err
		}
		if err := user.prepareForInsert(tx); err != nil {
			return err
		}
		user.Group = offer.CustomerGroup
		user.Quota = 0
		user.AffCode = common.GetRandomString(4)
		if user.Setting == "" {
			user.SetSetting(dto.UserSetting{})
		}

		if offer.Program.GrantQuota > 0 && offer.Program.ClaimedCount < offer.Program.GrantLimit {
			result := tx.Model(&PartnershipProgram{}).
				Where("id = ? AND claimed_count < grant_limit", offer.Program.Id).
				UpdateColumn("claimed_count", gorm.Expr("claimed_count + 1"))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 1 {
				grantedQuota = offer.Program.GrantQuota
				user.Quota = grantedQuota
			}
		}
		if err := tx.Create(user).Error; err != nil {
			return err
		}
		return tx.Create(&PartnershipEnrollment{
			ProgramId: offer.Program.Id, CustomerId: offer.CustomerId,
			CustomerGroup: offer.CustomerGroup, UserId: user.Id, GrantedQuota: grantedQuota,
		}).Error
	})
	return grantedQuota, err
}
