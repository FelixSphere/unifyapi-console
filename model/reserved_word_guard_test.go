/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// "group" is a reserved word. MySQL and SQLite accept backticks; PostgreSQL
// does not, and answers a backticked identifier with
//
//	ERROR: syntax error at or near "group" (SQLSTATE 42601)
//
// Every test in this package runs on SQLite, so a backtick in a WHERE clause
// passes the whole suite and then fails on the only database that matters.
// That is not hypothetical: CreatePartnershipCustomer carried one from
// 2026-09-03 to 2026-09-17, which meant adding a customer group to a
// partnership program was broken in production for two weeks while CI stayed
// green the entire time.
//
// commonGroupCol holds the portable form. This scans for the mistake instead
// of relying on a test that cannot reach it.
func TestNoReservedWordIsQuotedForOneDialectOnly(t *testing.T) {
	// Any clause that reaches the database. An earlier version scanned only
	// WHERE-shaped clauses on the theory that a Select with a retry was safe;
	// it was not -- the retry meant every call on PostgreSQL failed, logged a
	// syntax error, and ran twice. Guessing wrong and recovering is still
	// getting it wrong.
	clauses := []string{"Where(", "Joins(", "Having(", "Order(", "Select(", "Pluck(", "Group("}

	var offences []string
	for _, dir := range []string{".", "../controller", "../service", "../relay", "../middleware"} {
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		require.NoError(t, err)
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(dir, name)
			body, err := os.ReadFile(path)
			require.NoError(t, err)
			for number, line := range strings.Split(string(body), "\n") {
				if !strings.Contains(line, "`group`") && !strings.Contains(line, "`key`") {
					continue
				}
				for _, clause := range clauses {
					if strings.Contains(line, clause) {
						offences = append(offences,
							filepath.Clean(path)+":"+strconv.Itoa(number+1)+"  "+strings.TrimSpace(line))
						break
					}
				}
			}
		}
	}

	assert.Empty(t, offences,
		"a reserved word is backtick-quoted in a query clause, which PostgreSQL rejects; "+
			"use commonGroupCol / commonKeyCol instead:\n%s", strings.Join(offences, "\n"))
}

// The accessor exists because commonGroupCol is set by initCol, which a
// running server calls and a unit test does not. Building a WHERE clause from
// an empty column name yields "AND  = ?", which is broken more quietly than
// the reserved word it replaced -- and that is exactly what happened when this
// fix first landed: two controller tests failed on malformed SQL.
func TestTheGroupColumnIsNeverEmpty(t *testing.T) {
	previous := commonGroupCol
	t.Cleanup(func() { commonGroupCol = previous })

	commonGroupCol = ""
	assert.NotEmpty(t, groupColumn(),
		"an unset column name must still produce a usable identifier")
	assert.Contains(t, groupColumn(), "group")

	commonGroupCol = `"group"`
	assert.Equal(t, `"group"`, groupColumn(), "a configured column name wins")
}
