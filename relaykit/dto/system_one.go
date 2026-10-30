/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package dto

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/types"
)

// TypeSafe's System One API. A request carries a state and a map of named,
// typed questions; the answer is a typed value per question with calibrated
// probabilities. There is no autoregressive decoding, so there is no stream
// and no assistant message -- which is why this cannot be folded into the
// chat shapes and gets a format of its own.
//
// https://docs.typesafe.ai/api

const (
	SystemOneQuestionNoul   = "noul"
	SystemOneQuestionChoice = "choice"
	SystemOneQuestionScore  = "score"
)

// MaxSystemOneQuestions bounds how many questions one request may carry. The
// vendor bills input tokens and the whole state is re-read per question, so an
// unbounded map is an unbounded bill; the cap is far above any documented
// pattern (the fan-out cookbook uses a handful) and exists to keep a runaway
// client from spending a customer's balance in one call.
const MaxSystemOneQuestions = 256

type SystemOneRequest struct {
	Model     string         `json:"model"`
	State     any            `json:"state"`
	Questions map[string]any `json:"questions"`
}

func (r *SystemOneRequest) SetModelName(modelName string) {
	if modelName != "" {
		r.Model = modelName
	}
}

// IsStream is always false: a decision model returns one typed answer.
func (r *SystemOneRequest) IsStream(_ *http.Request) bool {
	return false
}

// GetTokenCountMeta feeds the pre-consume estimate. Everything the vendor
// reads is input -- the state and every question's instructions and criteria --
// so the combined text is all of it. MaxTokens is zero because the model
// cannot emit more than its typed answer.
func (r *SystemOneRequest) GetTokenCountMeta() *types.TokenCountMeta {
	var b strings.Builder
	appendValue(&b, r.State)
	for name, question := range r.Questions {
		b.WriteString(name)
		b.WriteString("\n")
		appendValue(&b, question)
	}
	return &types.TokenCountMeta{
		TokenType:   types.TokenTypeTokenizer,
		CombineText: b.String(),
	}
}

// appendValue flattens the arbitrary JSON a state or question may hold into
// text for counting. It walks maps and slices rather than marshalling, so the
// count is of the content rather than of JSON punctuation.
func appendValue(b *strings.Builder, value any) {
	switch v := value.(type) {
	case nil:
		return
	case string:
		b.WriteString(v)
	case []any:
		for _, item := range v {
			appendValue(b, item)
			b.WriteString("\n")
		}
	case map[string]any:
		for key, item := range v {
			b.WriteString(key)
			b.WriteString(" ")
			appendValue(b, item)
			b.WriteString("\n")
		}
	default:
		fmt.Fprintf(b, "%v", v)
	}
}

// SystemOneUsage is what the vendor reports back. Output tokens are counted
// but not charged -- see the jev catalog row, where OutputUSD is zero with
// FreeOutput set.
type SystemOneUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// SystemOneResponse is read only for its usage. Everything else is forwarded
// to the caller byte for byte, so that a typed answer arrives as the vendor
// wrote it and a field added upstream tomorrow is not dropped here today.
type SystemOneResponse struct {
	Usage *SystemOneUsage `json:"usage"`
}
