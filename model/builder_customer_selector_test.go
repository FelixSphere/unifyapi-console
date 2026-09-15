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

// One program holds many customers, one per Builder Hub team. A signed team
// name selects which of them owns the connection, its pricing group and its
// invoice. Without a name the program default is used, which is what every
// caller predating the selector relies on.
func TestResolveBuilderProgramSelectsNamedCustomer(t *testing.T) {
	setupPartnershipTestDB(t)
	program := PartnershipProgram{Name: "Builder_hub_2026_Sep_Batch", Code: "builders", Group: "partner", Enabled: true}
	require.NoError(t, CreatePartnershipProgram(&program))
	require.NoError(t, CreatePartnershipCustomer(program.Id, &PartnershipCustomer{
		Name: "Acme Robotics", Code: "acme-robotics", Group: "vip", Enabled: true,
	}))

	t.Run("no name uses the program default", func(t *testing.T) {
		offer, err := ResolveBuilderProgram(DB, BuilderProgramSelector{ProgramName: program.Name}, false)
		require.NoError(t, err)
		assert.Equal(t, "partner", offer.CustomerGroup)
	})

	t.Run("a named team resolves to its own customer and group", func(t *testing.T) {
		offer, err := ResolveBuilderProgram(DB, BuilderProgramSelector{
			ProgramName: program.Name, CustomerName: "Acme Robotics",
		}, false)
		require.NoError(t, err)
		assert.Equal(t, "Acme Robotics", offer.CustomerName)
		assert.Equal(t, "vip", offer.CustomerGroup)
		assert.Equal(t, "acme-robotics", offer.CustomerCode)
	})

	// An unresolvable team must never fall back to the default customer: that
	// would bill one team's usage to another team's invoice.
	t.Run("unresolvable names are refused, never defaulted", func(t *testing.T) {
		for _, name := range []string{
			"Missing Team",   // no such customer
			"acme robotics",  // exact match only, collations may disagree
			"Acme Robotics ", // trailing space is not a different team, it is invalid
		} {
			_, err := ResolveBuilderProgram(DB, BuilderProgramSelector{
				ProgramName: program.Name, CustomerName: name,
			}, false)
			assert.ErrorIs(t, err, ErrPartnershipCustomerUnavailable, "name %q", name)
		}
	})

	t.Run("a disabled team cannot connect", func(t *testing.T) {
		require.NoError(t, DB.Model(&PartnershipCustomer{}).
			Where("program_id = ? AND code = ?", program.Id, "acme-robotics").
			Update("enabled", false).Error)
		t.Cleanup(func() {
			DB.Model(&PartnershipCustomer{}).
				Where("program_id = ? AND code = ?", program.Id, "acme-robotics").
				Update("enabled", true)
		})
		_, err := ResolveBuilderProgram(DB, BuilderProgramSelector{
			ProgramName: program.Name, CustomerName: "Acme Robotics",
		}, false)
		assert.ErrorIs(t, err, ErrPartnershipCustomerUnavailable)
	})

	// The legacy code lookup already names one customer, so accepting a second
	// selector alongside it would leave two sources of truth.
	t.Run("a team name cannot ride along with a legacy code", func(t *testing.T) {
		_, err := ResolveBuilderProgram(DB, BuilderProgramSelector{
			PartnershipCode: "builders", CustomerName: "Acme Robotics",
		}, false)
		assert.ErrorIs(t, err, ErrPartnershipCustomerUnavailable)
	})
}

// The named-customer lookup runs inside connect's locked transaction, which is
// exactly where a shared statement broke account creation before. Guard the new
// path against the same class of bug: the store stays SQLite so rows are real,
// while the database type is switched so lockForUpdate takes its locking branch.
func TestResolveBuilderProgramSelectsNamedCustomerUnderLock(t *testing.T) {
	setupPartnershipTestDB(t)
	program := PartnershipProgram{Name: "Builder_hub_2026_Sep_Batch", Code: "builders", Group: "partner", Enabled: true}
	require.NoError(t, CreatePartnershipProgram(&program))
	require.NoError(t, CreatePartnershipCustomer(program.Id, &PartnershipCustomer{
		Name: "Acme Robotics", Code: "acme-robotics", Group: "vip", Enabled: true,
	}))

	for _, databaseType := range []common.DatabaseType{common.DatabaseTypePostgreSQL, common.DatabaseTypeMySQL} {
		t.Run(string(databaseType), func(t *testing.T) {
			previousType := common.MainDatabaseType()
			common.SetMainDatabaseType(databaseType)
			t.Cleanup(func() { common.SetMainDatabaseType(previousType) })

			offer, err := ResolveBuilderProgram(DB, BuilderProgramSelector{
				ProgramName: program.Name, CustomerName: "Acme Robotics",
			}, true)
			require.NoError(t, err)
			assert.Equal(t, "vip", offer.CustomerGroup)

			// The default path must stay correct under lock too.
			offer, err = ResolveBuilderProgram(DB, BuilderProgramSelector{ProgramName: program.Name}, true)
			require.NoError(t, err)
			assert.Equal(t, "partner", offer.CustomerGroup)
		})
	}
}
