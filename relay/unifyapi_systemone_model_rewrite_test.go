/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package relay

import (
	"strings"
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

	// The whole body, byte for byte, with only the model name different.
	want := `{"model":"typesafe/jev-1.13","state":{"invoice_id":9007199254740993,` +
		`"amount_cents":12345678901234567890,"ratio":1.0,"pct":0.30,"tiny":1e-7},` +
		`"questions":{"approve":{"type":"noul"}}}`
	assert.Equal(t, want, string(got),
		"a decode-and-re-encode would round 9007199254740993 to ...992 and trim "+
			"12345678901234567890 to ...567000, and would reorder the keys")
}

// Whatever the vendor adds after this code is written has to survive, in place.
func TestTheModelMappingKeepsEveryOtherFieldIncludingOnesWeDoNotKnow(t *testing.T) {
	raw := []byte(`{"model":"jev-1.13","state":{"text":"route this"},` +
		`"questions":{"route":{"type":"choice","criteria":{"fast":"latency first"}}},` +
		`"a_field_the_vendor_adds_next_year":{"nested":[1,"two",null,true]}}`)

	got, err := rewriteSystemOneModel(raw, "typesafe/jev-1.13")
	require.NoError(t, err)

	assert.Equal(t, strings.Replace(string(raw), `"jev-1.13"`, `"typesafe/jev-1.13"`, 1), string(got))
}

// A state can legitimately contain a key called "model"; only the top-level
// one names the model being billed.
func TestTheModelMappingTouchesOnlyTheTopLevelModelField(t *testing.T) {
	raw := []byte(`{"model":"jev-1.13","state":{"model":"the customer's own field"},` +
		`"questions":{"q":{"type":"noul"}}}`)

	got, err := rewriteSystemOneModel(raw, "typesafe/jev-1.13")
	require.NoError(t, err)

	assert.Contains(t, string(got), `"model":"typesafe/jev-1.13"`)
	assert.Contains(t, string(got), `"state":{"model":"the customer's own field"}`,
		"a nested field that happens to be called model is the customer's data")
}

func TestTheModelMappingRefusesABodyThatIsNotJSON(t *testing.T) {
	_, err := rewriteSystemOneModel([]byte(`{"model":`), "typesafe/jev-1.13")
	assert.Error(t, err, "a truncated body must fail here rather than reach the upstream half-written")
}
