package service

// UNIFYAPI-FORK: GLM is sold at Z.AI's international list, never the mainland
// one. Operator decision 2026-11-15: UnifyAI is an international company. The
// mainland list (open.bigmodel.cn, CNY, tiered at 32K input) sat 15-40% below
// what our supplier bills, so glm-5 and glm-5.1 were sold below cost.
//
// Expected dollars are typed from docs.z.ai/guides/overview/pricing, read
// 2026-11-15, not derived from the catalog under test.

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

func TestGLMBillsAtTheZaiInternationalList(t *testing.T) {
	withDiscount(t, `{}`)

	// model -> $ for 1M input + 1M output at list.
	want := map[string]float64{
		"glm-4.7":     0.6 + 2.2,
		"glm-5":       1 + 3.2,
		"glm-5-turbo": 1.2 + 4, // not on docs.z.ai's page; models.dev zai + OpenRouter z-ai
		"glm-5.1":     1.4 + 4.4,
		"glm-5.2":     1.4 + 4.4,
		"glm-5.3":     1.4 + 4.4,
	}
	for model, usd := range want {
		t.Run(model, func(t *testing.T) {
			require.InDelta(t, usd, chargeUSD(t, model, "default", oneMillionEach()), 1e-6)
		})
	}
}

// The mainland list had a cheaper tier under 32K input. The international list
// has one price, so a short request must cost exactly its share of the 1M one.
func TestGLMShortPromptsAreNotDiscounted(t *testing.T) {
	withDiscount(t, `{}`)

	short := &dto.Usage{PromptTokens: 10_000, CompletionTokens: 10_000, TotalTokens: 20_000}
	for model, usd := range map[string]float64{"glm-5": (1 + 3.2) / 100, "glm-5.1": (1.4 + 4.4) / 100} {
		t.Run(model, func(t *testing.T) {
			require.InDelta(t, usd, chargeUSD(t, model, "default", short), 1e-6)
		})
	}
}

func TestGLMIsVerifiedAgainstZaiNotTheMainlandFeed(t *testing.T) {
	for _, model := range []string{"glm-4.7", "glm-5", "glm-5.1", "glm-5.2", "glm-5.3"} {
		entry, ok := ratio_setting.CatalogEntryFor(model)
		require.True(t, ok, model)
		require.Equal(t, "zai", entry.Vendor, "%s must be checked against models.dev zai (api.z.ai), not zhipuai (open.bigmodel.cn)", model)
		require.Nil(t, entry.ContextTier, "%s: the international list has no context tier", model)
	}
}
