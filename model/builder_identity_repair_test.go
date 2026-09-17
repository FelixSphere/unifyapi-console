/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupRepairTest(t *testing.T) {
	t.Helper()
	setupPartnershipTestDB(t)
	require.NoError(t, DB.AutoMigrate(&BuilderIdentity{}, &Token{}, &Tenant{}))
}

func orphanedLink(t *testing.T, subject string) *BuilderIdentity {
	t.Helper()
	link := &BuilderIdentity{
		Subject: subject, UserId: 4242, ProgramId: 1, CustomerId: 2, TokenId: 3,
		GrantQuota: 5000000, GrantClaimedAt: 1789000000,
	}
	require.NoError(t, DB.Create(link).Error)
	return link
}

// The case this exists for, taken from production: a Builder subject whose
// account was deleted. Every connect answers UNIFY_ACCOUNT_REPAIR_REQUIRED and
// there was no way to clear it -- the bridge is the only Builder route and it
// has no admin surface.
func TestArchivingAnOrphanFreesTheSubjectToConnectAgain(t *testing.T) {
	setupRepairTest(t)
	link := orphanedLink(t, "3c531fe1-71d6-478f-8bbd-f9f6c80b9af5")

	_, _, err := GetBuilderIdentity(link.Subject)
	require.ErrorIs(t, err, ErrBuilderAccountRepairRequired, "the fixture must start broken")

	archived, err := ArchiveOrphanedBuilderIdentity(link.Subject)
	require.NoError(t, err)
	require.NotNil(t, archived)

	_, _, err = GetBuilderIdentity(link.Subject)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound,
		"the subject must be free, so a fresh connect creates a new account")
}

// Archived, not deleted. The row is the only record that this account was
// enrolled in a program and claimed its grant; throwing that away to fix a
// login problem would discard billing history.
func TestArchivingKeepsTheEnrollmentRecord(t *testing.T) {
	setupRepairTest(t)
	link := orphanedLink(t, "orphan-subject")

	_, err := ArchiveOrphanedBuilderIdentity(link.Subject)
	require.NoError(t, err)

	var stored BuilderIdentity
	require.NoError(t, DB.First(&stored, link.Id).Error, "the row must survive")
	assert.True(t, IsArchivedBuilderSubject(stored.Subject))
	assert.Contains(t, stored.Subject, "orphan-subject", "the original subject stays readable")
	assert.Equal(t, 1, stored.ProgramId)
	assert.Equal(t, 2, stored.CustomerId)
	assert.Equal(t, 5000000, stored.GrantQuota, "and so does what it claimed")
	assert.Equal(t, int64(1789000000), stored.GrantClaimedAt)
}

// The guard that matters: a working link must never be archived. An operator
// asking to repair one is mistaken about the subject or the problem, and
// archiving it would disconnect an account that is fine.
func TestAWorkingLinkIsNeverArchived(t *testing.T) {
	setupRepairTest(t)
	user := &User{
		Username: "builder_live", Password: "password123", DisplayName: "Live",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffCode: "lv01",
	}
	require.NoError(t, DB.Create(user).Error)
	link := &BuilderIdentity{Subject: "healthy", UserId: user.Id, ProgramId: 1, CustomerId: 2, TokenId: 3}
	require.NoError(t, DB.Create(link).Error)

	_, err := ArchiveOrphanedBuilderIdentity("healthy")
	require.ErrorIs(t, err, ErrBuilderLinkNotBroken)

	var stored BuilderIdentity
	require.NoError(t, DB.First(&stored, link.Id).Error)
	assert.Equal(t, "healthy", stored.Subject, "the working link must be untouched")
}

// The subject column is varchar(128) and the prefix alone can push a
// full-length subject past it, so an over-long archive would fail to write.
func TestAnArchivedSubjectAlwaysFitsTheColumn(t *testing.T) {
	for _, subject := range []string{"short", strings.Repeat("s", 128), strings.Repeat("投", 40)} {
		archived := ArchivedBuilderSubject(99, subject)
		assert.LessOrEqual(t, len(archived), 128, "subject of %d bytes produced %d", len(subject), len(archived))
		assert.True(t, IsArchivedBuilderSubject(archived))
	}
}

// The same subject can be archived more than once over a deployment's life,
// and Subject is unique, so two archives must not collide.
func TestTwoArchivesOfTheSameSubjectDoNotCollide(t *testing.T) {
	assert.NotEqual(t,
		ArchivedBuilderSubject(1, "same-subject"),
		ArchivedBuilderSubject(2, "same-subject"),
		"the identity id keeps archived subjects distinct")
}

// Archiving an already-archived subject is an operator mistake, not a no-op to
// absorb silently.
func TestAnAlreadyArchivedSubjectIsRefused(t *testing.T) {
	setupRepairTest(t)
	_, err := ArchiveOrphanedBuilderIdentity(BuilderArchivedSubjectPrefix + "7:old")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already archived")
}
