/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package typesafe

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A chat request sent to jev-1.13 is the caller's mistake. It used to surface
// as a 500, which the UX probe journeys and the request log both read as an
// outage on our side. The handlers wrap a conversion error exactly as below,
// so this checks the status the customer actually receives.
func TestAChatRequestToSystemOneIsRefusedAsA400ThatIsNotRetried(t *testing.T) {
	adaptor := &Adaptor{}
	refusals := map[string]error{}
	_, refusals["openai"] = adaptor.ConvertOpenAIRequest(nil, nil, &dto.GeneralOpenAIRequest{})
	_, refusals["claude"] = adaptor.ConvertClaudeRequest(nil, nil, &dto.ClaudeRequest{})
	_, refusals["embedding"] = adaptor.ConvertEmbeddingRequest(nil, nil, dto.EmbeddingRequest{})
	_, refusals["rerank"] = adaptor.ConvertRerankRequest(nil, 0, dto.RerankRequest{})

	for surface, err := range refusals {
		require.Error(t, err, surface)
		wrapped := types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		assert.Equal(t, http.StatusBadRequest, wrapped.StatusCode, surface)
		assert.True(t, types.IsSkipRetryError(wrapped), "%s: retrying another channel cannot make a chat request valid", surface)
		assert.Contains(t, wrapped.Error(), "/v1/systemone", surface)
	}

	_, first := adaptor.ConvertOpenAIRequest(nil, nil, &dto.GeneralOpenAIRequest{})
	_, second := adaptor.ConvertOpenAIRequest(nil, nil, &dto.GeneralOpenAIRequest{})
	assert.NotSame(t, first.(*types.NewAPIError), second.(*types.NewAPIError), "handlers mutate the error in place, so it must not be shared across requests")
}
