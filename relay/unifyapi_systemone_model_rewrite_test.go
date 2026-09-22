/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package relay

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The model mapping runs on every request through a channel that renames the
// model -- which is the required configuration for the FlatKey upstream, where
// we sell jev-1.13 and they answer to typesafe/jev-1.13. A decision model's
// input IS the customer's business state, so a number altered on the way is
// the customer being billed for a decision about data they never sent.
func TestTheModelMappingLeavesTheCustomersNumbersExactlyAsTheyWroteThem(t *testing.T) {
	raw := []byte(`{"model":"jev-1.13","state":{"invoice_id":9007199254740993,` +
		`"amount_cents":12345678901234567890,"ratio":1.0,"pct":0.30,"tiny":1e-7},` +
		`"questions":{"approve":{"type":"noul"}}}`)

	got, err := rewriteSystemOneModel(raw, "typesafe/jev-1.13")
	require.NoError(t, err)

	text := string(got)
	for _, exact := range []string{
		`"invoice_id":9007199254740993`,       // float64 rounds this to ...992
		`"amount_cents":12345678901234567890`, // and this to ...567000
		`"ratio":1.0`,
		`"pct":0.30`,
		`"tiny":1e-7`,
	} {
		assert.Contains(t, text, exact, "a number must survive byte for byte")
	}
	assert.Contains(t, text, `"model":"typesafe/jev-1.13"`)
	assert.NotContains(t, text, `"jev-1.13"`+`,`, "the old model name must be gone")
}

func TestTheModelMappingKeepsEveryOtherFieldIncludingOnesWeDoNotKnow(t *testing.T) {
	raw := []byte(`{"model":"jev-1.13","state":{"text":"route this"},` +
		`"questions":{"route":{"type":"choice","criteria":{"fast":"latency first"}}},` +
		`"a_field_the_vendor_adds_next_year":{"nested":[1,"two",null,true]}}`)

	got, err := rewriteSystemOneModel(raw, "typesafe/jev-1.13")
	require.NoError(t, err)

	var before, after map[string]any
	require.NoError(t, json.Unmarshal(raw, &before))
	require.NoError(t, json.Unmarshal(got, &after))

	before["model"] = "typesafe/jev-1.13"
	assert.Equal(t, before, after, "only the model name may differ")
}

func TestTheModelMappingRefusesABodyThatIsNotJSON(t *testing.T) {
	_, err := rewriteSystemOneModel([]byte(`{"model":`), "typesafe/jev-1.13")
	assert.Error(t, err, "a truncated body must fail here rather than reach the upstream half-written")
}
