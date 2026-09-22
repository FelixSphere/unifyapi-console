/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package typesafe

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A decision model's answer is the product: a typed value per question with
// calibrated probabilities. These tests pin that it reaches the caller exactly
// as the vendor wrote it, and that what we bill comes from the vendor's own
// usage rather than from anything we infer.

func doResponse(t *testing.T, status int, body string) (*httptest.ResponseRecorder, any, error) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader("{}"))

	resp := &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	adaptor := &Adaptor{}
	usage, apiErr := adaptor.DoResponse(c, resp, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}})
	if apiErr != nil {
		return recorder, nil, apiErr
	}
	return recorder, usage, nil
}

func TestTheTypedAnswerReachesTheCallerUntouchedAndBillsTheVendorsInputTokens(t *testing.T) {
	body := `{"answers":{"is_spam":{"type":"noul","noul":0.95},` +
		`"topic":{"type":"choice","choice":"billing","probabilities":{"billing":0.8,"other":0.2},"confidence":0.81}},` +
		`"usage":{"input_tokens":296,"output_tokens":20}}`

	recorder, usage, err := doResponse(t, http.StatusOK, body)
	require.NoError(t, err)

	assert.JSONEq(t, body, recorder.Body.String(),
		"the probabilities are the product; they must not be reshaped on the way out")

	counted, ok := usage.(*dto.Usage)
	require.True(t, ok)
	assert.Equal(t, 296, counted.PromptTokens, "input tokens come from the vendor, not from our own count")
	assert.Equal(t, 20, counted.CompletionTokens, "output tokens are counted here and priced at zero by the catalog")
	assert.Equal(t, 316, counted.TotalTokens)
}

// A response with no usage is unaccountable. Billing it as zero would hand out
// free calls silently; guessing a number would invent a charge. Refuse.
func TestAResponseWithoutUsageIsRefusedRatherThanBilledAsZero(t *testing.T) {
	_, _, err := doResponse(t, http.StatusOK, `{"answers":{"is_spam":{"type":"noul","noul":0.95}}}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "usage")
}

func TestABodyThatIsNotSystemOneJSONIsRefused(t *testing.T) {
	_, _, err := doResponse(t, http.StatusOK, `not json at all`)
	require.Error(t, err)
}

func TestTheEndpointIsAppendedOnceHoweverTheOperatorTypedTheBaseURL(t *testing.T) {
	adaptor := &Adaptor{}
	for _, tc := range []struct{ base, want string }{
		{"https://api.typesafe.ai", "https://api.typesafe.ai/v1/systemone"},
		{"https://api.typesafe.ai/", "https://api.typesafe.ai/v1/systemone"},
		{"https://api.typesafe.ai/v1/systemone", "https://api.typesafe.ai/v1/systemone"},
		{"https://gateway.example.com/typesafe", "https://gateway.example.com/typesafe/v1/systemone"},
	} {
		got, err := adaptor.GetRequestURL(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: tc.base}})
		require.NoError(t, err, tc.base)
		assert.Equal(t, tc.want, got, "base url %q", tc.base)
	}

	_, err := adaptor.GetRequestURL(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}})
	assert.Error(t, err, "a channel with no base url must fail loudly, not post to a relative path")
}

// Refusing is the product decision this adaptor encodes: a chat request has no
// state and no typed questions, so any mapping would be invented, and the
// customer would be billed for our guess.
func TestTheChatShapedConversionsRefuseInsteadOfInventingAMapping(t *testing.T) {
	adaptor := &Adaptor{}
	_, err := adaptor.ConvertOpenAIRequest(nil, nil, &dto.GeneralOpenAIRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "/v1/systemone", "the error must say where the model actually lives")

	_, err = adaptor.ConvertClaudeRequest(nil, nil, &dto.ClaudeRequest{})
	require.Error(t, err)
	_, err = adaptor.ConvertRerankRequest(nil, 0, dto.RerankRequest{})
	require.Error(t, err)
	_, err = adaptor.ConvertEmbeddingRequest(nil, nil, dto.EmbeddingRequest{})
	require.Error(t, err)
}

// A channel prefilled with a model the catalog does not price would bill at
// whatever the placeholder happens to be. The two lists must agree.
func TestEveryPrefilledModelIsPricedByTheCatalog(t *testing.T) {
	for _, name := range ModelList {
		entry, ok := ratio_setting.CatalogEntryFor(name)
		require.True(t, ok, "%s is offered by the channel but carries no catalog price", name)
		assert.Greater(t, entry.InputUSD, float64(0), "%s must have an input price", name)
	}
}
