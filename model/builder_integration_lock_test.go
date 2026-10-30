/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ResolveBuilderProgram must give each lookup its own statement. On MySQL and
// PostgreSQL lockForUpdate returns a non-clone handle, so sharing one between
// the program and customer lookups carries the first query's resolved table
// and its name condition into the second, which then fails outright:
//
//	SELECT * FROM partnership_programs
//	WHERE name = '<program>' AND (program_id = ? AND is_default = ? AND removed_at = ?)
//	FOR UPDATE
//
// Only connect reached this, as the sole caller passing lock=true, so every
// Builder account creation answered 403 while reads kept working.
//
// The store stays SQLite so the rows are real; the database type is switched
// so lockForUpdate takes its locking branch. SQLite strips the FOR UPDATE
// text, but the shared-statement hazard does not depend on that text.
func TestResolveBuilderProgramDoesNotShareLockedStatement(t *testing.T) {
	setupPartnershipTestDB(t)
	require.NoError(t, CreatePartnershipProgram(&PartnershipProgram{
		Name: "Builder_hub_2026_Sep_Batch", Code: "builders", Group: "partner", Enabled: true,
	}))

	for _, databaseType := range []common.DatabaseType{common.DatabaseTypePostgreSQL, common.DatabaseTypeMySQL} {
		t.Run(string(databaseType), func(t *testing.T) {
			previousType := common.MainDatabaseType()
			common.SetMainDatabaseType(databaseType)
			t.Cleanup(func() { common.SetMainDatabaseType(previousType) })

			offer, err := ResolveBuilderProgram(DB, BuilderProgramSelector{
				ProgramName: "Builder_hub_2026_Sep_Batch",
			}, true)
			require.NoError(t, err)
			require.NotNil(t, offer)
			assert.Equal(t, "Builder_hub_2026_Sep_Batch", offer.Program.Name)
			assert.NotZero(t, offer.CustomerId)
		})
	}
}

// Four different refusals used to arrive as one opaque error, which Builder Hub
// then rendered as "you do not have permission". A staff account, a disabled
// account and a bad ownership proof need completely different responses from
// the person reading them.
//
// Each still satisfies the general contract, so callers that only ask "is this
// account usable" are unaffected.
func TestEachRefusalSaysWhichOneItIs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		user    *User
		want    error
		refused bool
	}{
		{"an administrator is not a self-serve billing subject",
			&User{Role: common.RoleAdminUser, Status: common.UserStatusEnabled}, ErrBuilderStaffAccount, true},
		{"root is refused for the same reason",
			&User{Role: common.RoleRootUser, Status: common.UserStatusEnabled}, ErrBuilderStaffAccount, true},
		{"a disabled account is a different problem with a different fix",
			&User{Role: common.RoleCommonUser, Status: common.UserStatusDisabled}, ErrBuilderAccountDisabled, true},
		{"an ordinary enabled account is not refused at all",
			&User{Role: common.RoleCommonUser, Status: common.UserStatusEnabled}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refusal := builderAccountRefusal(tc.user)
			if !tc.refused {
				assert.NoError(t, refusal)
				return
			}
			require.Error(t, refusal)
			assert.ErrorIs(t, refusal, tc.want, "the specific reason must be recoverable")
			assert.ErrorIs(t, refusal, ErrBuilderUnavailable,
				"and the general contract must still hold for callers that only ask whether it is usable")
		})
	}
}

// A staff refusal and a bad proof are distinguishable from each other, not just
// from success -- otherwise splitting them buys nothing.
func TestRefusalsAreDistinguishableFromEachOther(t *testing.T) {
	staff := builderAccountRefusal(&User{Role: common.RoleRootUser, Status: common.UserStatusEnabled})
	disabled := builderAccountRefusal(&User{Role: common.RoleCommonUser, Status: common.UserStatusDisabled})

	assert.NotErrorIs(t, staff, ErrBuilderAccountDisabled)
	assert.NotErrorIs(t, disabled, ErrBuilderStaffAccount)
	assert.NotErrorIs(t, staff, ErrBuilderOwnershipProof)
}
