package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupModelDiscountIsFinalPerCustomerAndModel(t *testing.T) {
	require.NoError(t, UpdateGroupModelDiscountByJSONString(`{
		"GenAI":{"claude-opus-4-8":0.8,"claude-opus-5":0.9},
		"UnifyAI":{"claude-opus-4-8":0.95}
	}`))
	t.Cleanup(func() { require.NoError(t, UpdateGroupModelDiscountByJSONString(`{}`)) })

	ratio, ok := GetGroupModelDiscount("GenAI", "claude-opus-4-8")
	require.True(t, ok)
	require.InDelta(t, 0.8, ratio, 1e-12)

	ratio, ok = GetGroupModelDiscount("GenAI", "claude-opus-5")
	require.True(t, ok)
	require.InDelta(t, 0.9, ratio, 1e-12)

	ratio, ok = GetGroupModelDiscount("UnifyAI", "claude-opus-4-8")
	require.True(t, ok)
	require.InDelta(t, 0.95, ratio, 1e-12)

	_, ok = GetGroupModelDiscount("UnifyAI", "claude-opus-5")
	require.False(t, ok)
}

func TestGroupModelDiscountRejectsUnsafeValuesWithoutChangingLiveTable(t *testing.T) {
	require.NoError(t, UpdateGroupModelDiscountByJSONString(`{"GenAI":{"gpt-4o":0.8}}`))
	t.Cleanup(func() { require.NoError(t, UpdateGroupModelDiscountByJSONString(`{}`)) })

	for _, payload := range []string{
		`{"GenAI":{"not-in-catalog":0.8}}`,
		`{"GenAI":{"gpt-4o":0}}`,
		`{"GenAI":{"gpt-4o":10.1}}`,
	} {
		require.Error(t, UpdateGroupModelDiscountByJSONString(payload))
		ratio, ok := GetGroupModelDiscount("GenAI", "gpt-4o")
		require.True(t, ok)
		require.InDelta(t, 0.8, ratio, 1e-12)
	}
}

func TestGroupModelDiscountCopyCannotMutateLiveTable(t *testing.T) {
	require.NoError(t, UpdateGroupModelDiscountByJSONString(`{"GenAI":{"gpt-4o":0.8}}`))
	t.Cleanup(func() { require.NoError(t, UpdateGroupModelDiscountByJSONString(`{}`)) })

	copy := GetGroupModelDiscountCopy()
	copy["GenAI"]["gpt-4o"] = 7

	ratio, ok := GetGroupModelDiscount("GenAI", "gpt-4o")
	require.True(t, ok)
	require.InDelta(t, 0.8, ratio, 1e-12)
}
