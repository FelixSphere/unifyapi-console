package service

// UNIFYAPI-FORK: tests for the models.dev price lookup.
//
// The behaviour that matters is not "it finds a price" -- it is that it does NOT
// choose one. The same model id is listed by hundreds of providers at different
// prices, and a console that silently picked would be inventing a commercial
// decision on the operator's behalf.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsFirstPartyListing(t *testing.T) {
	cases := []struct {
		provider, model string
		want            bool
	}{
		// Vendors the compiled catalog already sources prices from.
		{"zhipuai", "glm-5.3", true},
		{"minimax", "MiniMax-M3", true},
		{"deepseek", "deepseek-v4-pro", true},
		{"anthropic", "claude-opus-4-8", true},
		// Resellers and aggregators.
		{"openrouter", "z-ai/glm-5.3", false},
		{"vercel", "minimax/minimax-m3", false},
		{"llmgateway-providers", "zai/glm-5.3", false},
		{"nano-gpt", "z-ai/glm-5.3", false},
		// The trap the live smoke test found: a \$0 subscription listing whose
		// id merely starts with a real vendor name.
		{"zhipuai-coding-plan", "glm-5.3", false},
		{"minimax-cn-coding-plan", "MiniMax-M3", false},
		{"alibaba-token-plan", "deepseek-v4-pro", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, isFirstPartyListing(tc.provider, tc.model),
			"%s/%s", tc.provider, tc.model)
	}
}

func TestLookupRejectsAnEmptyQuery(t *testing.T) {
	// Without this the lookup would sweep the entire feed and return every
	// model on models.dev, which is not a useful answer to "no input".
	_, err := LookupModelPrice("   ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "模型名")
}

// TestCandidateSortPutsTheVendorFirst. An operator syncing glm-5.3 wants
// Zhipu's price, not the cheapest reseller's. Burying the vendor in a list
// sorted by price is how the wrong number gets picked.
func TestCandidateSortPutsTheVendorFirst(t *testing.T) {
	candidates := []ModelPriceCandidate{
		{Provider: "crof", Model: "glm-5.3", InputUSD: 0.4},
		{Provider: "zhipuai", Model: "glm-5.3", InputUSD: 1.4, FirstParty: true},
		{Provider: "openrouter", Model: "z-ai/glm-5.3", InputUSD: 1.4},
	}
	sortCandidates(candidates)

	assert.Equal(t, "zhipuai", candidates[0].Provider,
		"the vendor's own listing must come first even when a reseller is cheaper")
	assert.Equal(t, "crof", candidates[1].Provider, "then cheapest")
}
