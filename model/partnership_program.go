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

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrPartnershipProgramUnavailable = errors.New("partnership program is unavailable")
	partnershipCodePattern           = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{2,63}$`)
)

// PartnershipProgram is an operator-managed registration offer. It only
// controls account creation: normal top-up and request billing remain unchanged.
type PartnershipProgram struct {
	Id           int    `json:"id" gorm:"primaryKey"`
	Name         string `json:"name" gorm:"type:varchar(120);not null"`
	Code         string `json:"code" gorm:"type:varchar(64);not null;uniqueIndex"`
	Group        string `json:"group" gorm:"type:varchar(64);not null"`
	GrantQuota   int    `json:"grant_quota" gorm:"type:int;not null;default:0"`
	GrantLimit   int    `json:"grant_limit" gorm:"type:int;not null;default:0"`
	ClaimedCount int    `json:"claimed_count" gorm:"type:int;not null;default:0"`
	Enabled      bool   `json:"enabled" gorm:"not null;default:false;index"`
	StartsAt     int64  `json:"starts_at" gorm:"not null;default:0"`
	EndsAt       int64  `json:"ends_at" gorm:"not null;default:0"`
	CreatedAt    int64  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt    int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

type PartnershipEnrollment struct {
	Id           int   `json:"id" gorm:"primaryKey"`
	ProgramId    int   `json:"program_id" gorm:"not null;index;uniqueIndex:idx_partnership_user"`
	UserId       int   `json:"user_id" gorm:"not null;index;uniqueIndex:idx_partnership_user"`
	GrantedQuota int   `json:"granted_quota" gorm:"type:int;not null;default:0"`
	CreatedAt    int64 `json:"created_at" gorm:"autoCreateTime"`
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
	err := DB.Order("created_at DESC, id DESC").Find(&programs).Error
	return programs, err
}

func GetPartnershipProgramByCode(code string) (*PartnershipProgram, error) {
	var program PartnershipProgram
	err := DB.Where("code = ?", NormalizePartnershipCode(code)).First(&program).Error
	return &program, err
}

func CreatePartnershipProgram(program *PartnershipProgram) error {
	if err := ValidatePartnershipProgram(program); err != nil {
		return err
	}
	program.Id = 0
	program.ClaimedCount = 0
	return DB.Create(program).Error
}

func UpdatePartnershipProgram(id int, input *PartnershipProgram) error {
	if id <= 0 {
		return errors.New("invalid partnership program id")
	}
	if err := ValidatePartnershipProgram(input); err != nil {
		return err
	}
	var current PartnershipProgram
	if err := DB.First(&current, id).Error; err != nil {
		return err
	}
	if input.GrantLimit < current.ClaimedCount {
		return fmt.Errorf("grant limit cannot be lower than claimed count %d", current.ClaimedCount)
	}
	return DB.Model(&current).Updates(map[string]any{
		"name":        input.Name,
		"code":        input.Code,
		"group":       input.Group,
		"grant_quota": input.GrantQuota,
		"grant_limit": input.GrantLimit,
		"enabled":     input.Enabled,
		"starts_at":   input.StartsAt,
		"ends_at":     input.EndsAt,
	}).Error
}

// InsertForPartnership applies the program identity and optional registration
// grant atomically with user creation. Capacity exhaustion is not an error: the
// user keeps the configured group and starts with zero quota, then uses the
// ordinary payment flow.
func (user *User) InsertForPartnership(code string, inviterId int) (int, error) {
	grantedQuota := 0
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		grantedQuota, err = user.InsertForPartnershipWithTx(tx, code, inviterId)
		return err
	})
	if err != nil {
		return 0, err
	}
	user.finishInsertWithInitialQuota(inviterId, grantedQuota, "Partnership registration grant")
	return grantedQuota, nil
}

func (user *User) InsertForPartnershipWithTx(tx *gorm.DB, code string, inviterId int) (int, error) {
	var grantedQuota int
	err := withNormalizedEmailLock(tx, user.Email, func(tx *gorm.DB) error {
		var program PartnershipProgram
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("code = ?", NormalizePartnershipCode(code)).First(&program).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPartnershipProgramUnavailable
			}
			return err
		}
		if !partnershipProgramActive(&program, time.Now().Unix()) {
			return ErrPartnershipProgramUnavailable
		}
		if err := user.prepareForInsert(tx); err != nil {
			return err
		}
		user.Group = program.Group
		user.Quota = 0
		user.AffCode = common.GetRandomString(4)
		if user.Setting == "" {
			user.SetSetting(dto.UserSetting{})
		}

		if program.GrantQuota > 0 && program.ClaimedCount < program.GrantLimit {
			result := tx.Model(&PartnershipProgram{}).
				Where("id = ? AND claimed_count < grant_limit", program.Id).
				UpdateColumn("claimed_count", gorm.Expr("claimed_count + 1"))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 1 {
				grantedQuota = program.GrantQuota
				user.Quota = grantedQuota
			}
		}
		if err := tx.Create(user).Error; err != nil {
			return err
		}
		return tx.Create(&PartnershipEnrollment{
			ProgramId: program.Id, UserId: user.Id, GrantedQuota: grantedQuota,
		}).Error
	})
	return grantedQuota, err
}
