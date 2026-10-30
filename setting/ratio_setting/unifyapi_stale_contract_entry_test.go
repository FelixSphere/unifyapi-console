/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package ratio_setting

// Delisting one model used to wipe every customer's negotiated price.
//
// Found on staging, 2026-09-28, releasing a catalogue chore that removed
// `gemini-flash-lite-latest`. Six groups still had per-model prices naming it.
// The option is re-read every 60 seconds, validation is all-or-nothing, so the
// whole table was rejected: default_group_model_ratio went from 52 of 52
// models to 0 and stayed there.
//
// It was not a display problem. The relay reads this same map through
// GetGroupModelDiscount, so an empty map drops every customer to their group
// ratio -- and a customer whose contract sits BELOW their group ratio is then
// overcharged, one minute after a deploy that had nothing to do with them.
//
// The fix is asymmetric on purpose, and these tests pin both halves: SAVING a
// price for an uncatalogued model is still refused, because the operator is
// doing something they should be told about; READING the stored table drops
// only the stale rows, because throwing away forty good contracts to punish
// one dead reference is the worse failure.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadingTheTableKeepsEveryPriceExceptTheStaleOne(t *testing.T) {
	InitRatioSettings()
	// The shape production was in: several groups priced on a live model, and
	// the same groups still naming one that had just been delisted.
	stored := `{
		"GenAI":   {"claude-opus-5": 0.7, "a-model-we-delisted": 0.9},
		"Chinhin": {"claude-opus-5": 0.8, "a-model-we-delisted": 0.9},
		"default": {"claude-opus-5": 0.85}
	}`

	cleaned, dropped, err := SanitizeGroupModelDiscountJSON(stored)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"Chinhin / a-model-we-delisted",
		"GenAI / a-model-we-delisted",
	}, dropped, "the operator must be told exactly what was ignored")

	// The cleaned table must now load, which is the whole point.
	require.NoError(t, UpdateGroupModelDiscountByJSONString(cleaned))
	t.Cleanup(func() { _ = UpdateGroupModelDiscountByJSONString(`{}`) })

	for group, want := range map[string]float64{"GenAI": 0.7, "Chinhin": 0.8, "default": 0.85} {
		got, ok := GetGroupModelDiscount(group, "claude-opus-5")
		require.True(t, ok, "%s lost its negotiated price because another row was stale", group)
		assert.InDelta(t, want, got, 1e-9)
	}
	_, ok := GetGroupModelDiscount("GenAI", "a-model-we-delisted")
	assert.False(t, ok)
}

// Without the sanitiser the same stored table is refused outright -- the
// behaviour that emptied the map on staging.
func TestTheStoredTableIsRefusedWithoutSanitising(t *testing.T) {
	InitRatioSettings()
	err := UpdateGroupModelDiscountByJSONString(
		`{"GenAI":{"claude-opus-5":0.7,"a-model-we-delisted":0.9}}`)
	require.Error(t, err, "this is the rejection that took the whole table down")
	assert.Contains(t, err.Error(), "a-model-we-delisted")
}

// A group left with nothing but stale rows disappears rather than lingering as
// an empty object.
func TestAGroupWithOnlyStaleEntriesDropsOut(t *testing.T) {
	cleaned, dropped, err := SanitizeGroupModelDiscountJSON(
		`{"GenAI":{"a-model-we-delisted":0.9},"default":{"claude-opus-5":0.85}}`)
	require.NoError(t, err)
	assert.Equal(t, []string{"GenAI / a-model-we-delisted"}, dropped)
	assert.NotContains(t, cleaned, "GenAI")
	assert.Contains(t, cleaned, "claude-opus-5")
}

// Nothing stale means nothing touched -- the common case must not re-encode the
// table and must not log.
func TestACleanTableIsReturnedUnchanged(t *testing.T) {
	stored := `{"GenAI":{"claude-opus-5":0.7}}`
	cleaned, dropped, err := SanitizeGroupModelDiscountJSON(stored)
	require.NoError(t, err)
	assert.Empty(t, dropped)
	assert.Equal(t, stored, cleaned, "an untouched table must come back byte-identical")
}

// Tolerating a stale MODEL must not tolerate a broken PRICE, and the sanitiser
// must not be a way around the save-side validator.
func TestSanitisingDoesNotExcuseAnInvalidPrice(t *testing.T) {
	InitRatioSettings()
	for _, bad := range []string{
		`{"GenAI":{"claude-opus-5":0}}`,
		`{"GenAI":{"claude-opus-5":-0.5}}`,
		`{"GenAI":{"claude-opus-5":999}}`,
	} {
		cleaned, _, err := SanitizeGroupModelDiscountJSON(bad)
		require.NoError(t, err, "the sanitiser only removes stale models")
		assert.Error(t, UpdateGroupModelDiscountByJSONString(cleaned),
			"%s must still be refused after sanitising", bad)
	}
}
