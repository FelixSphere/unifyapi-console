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
