/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package helper

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A System One request is validated here because the relay forwards it
// verbatim afterwards: whatever this accepts is what the vendor is asked to
// bill for. The one limit of our own is the question count -- the vendor
// re-reads the whole state per question and charges input tokens, so an
// unbounded map is an unbounded bill.

func systemOneContext(t *testing.T, body string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}

func TestAValidSystemOneRequestIsAcceptedWithItsStateAndQuestions(t *testing.T) {
	request, err := GetAndValidateSystemOneRequest(systemOneContext(t, `{
		"model": "jev-1.13",
		"state": "the customer says the invoice is wrong",
		"questions": {
			"is_billing": {"type": "noul", "instructions": "Is this about billing?",
				"criteria": {"true": "about money", "false": "anything else"}}
		}
	}`))
	require.NoError(t, err)
	assert.Equal(t, "jev-1.13", request.Model)
	assert.Len(t, request.Questions, 1)

	// The pre-consume estimate is built from everything the vendor reads: the
	// state and each question's text. Miss either and a large call is
	// under-reserved.
	meta := request.GetTokenCountMeta()
	require.NotNil(t, meta)
	assert.Contains(t, meta.CombineText, "invoice is wrong", "the state is input")
	assert.Contains(t, meta.CombineText, "Is this about billing?", "a question's instructions are input")
	assert.Contains(t, meta.CombineText, "about money", "a question's criteria are input")
}

func TestSystemOneRequestsMissingWhatTheVendorRequiresAreRefused(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"no model", `{"state":"s","questions":{"q":{"type":"noul"}}}`, "model is required"},
		{"no state", `{"model":"jev-1.13","questions":{"q":{"type":"noul"}}}`, "state is required"},
		{"no questions", `{"model":"jev-1.13","state":"s"}`, "questions is required"},
		{"empty questions", `{"model":"jev-1.13","state":"s","questions":{}}`, "questions is required"},
		{"question is not an object", `{"model":"jev-1.13","state":"s","questions":{"q":"noul"}}`, `question "q" must be an object`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := GetAndValidateSystemOneRequest(systemOneContext(t, tc.body))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestTheQuestionCountIsBoundedSoOneCallCannotDrainABalance(t *testing.T) {
	var questions []string
	for i := 0; i <= dto.MaxSystemOneQuestions; i++ {
		questions = append(questions, fmt.Sprintf(`"q%d":{"type":"noul"}`, i))
	}
	body := fmt.Sprintf(`{"model":"jev-1.13","state":"s","questions":{%s}}`, strings.Join(questions, ","))

	_, err := GetAndValidateSystemOneRequest(systemOneContext(t, body))
	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("at most %d", dto.MaxSystemOneQuestions))
}

// A state may be an object or an array, not just a string; the count must walk
// it rather than silently reserving nothing for a large structured state.
func TestAStructuredStateIsStillCountedAsInput(t *testing.T) {
	request, err := GetAndValidateSystemOneRequest(systemOneContext(t, `{
		"model": "jev-1.13",
		"state": {"ticket": {"subject": "refund for order 5512"}, "tags": ["urgent", "payments"]},
		"questions": {"q": {"type": "choice", "criteria": {"refund": "asks for money back"}}}
	}`))
	require.NoError(t, err)

	text := request.GetTokenCountMeta().CombineText
	assert.Contains(t, text, "refund for order 5512")
	assert.Contains(t, text, "urgent")
	assert.Contains(t, text, "asks for money back")
}
