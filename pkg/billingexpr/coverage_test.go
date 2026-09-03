package billingexpr

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseExprVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		expression  string
		wantVersion int
		wantBody    string
	}{
		{name: "explicit v1", expression: "v1:p * 2", wantVersion: 1, wantBody: "p * 2"},
		{name: "implicit default", expression: "p * 2", wantVersion: DefaultExprVersion, wantBody: "p * 2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version, body := ParseExprVersion(tt.expression)
			assert.Equal(t, tt.wantVersion, version)
			assert.Equal(t, tt.wantBody, body)
		})
	}
}

func TestExpressionMetadataLifecycle(t *testing.T) {
	InvalidateCache()
	t.Cleanup(InvalidateCache)

	assert.Equal(t, DefaultExprVersion, ExprVersion(""))
	assert.Nil(t, UsedVars(""))

	expression := "v1:p * 2 + c + cr"
	assert.Equal(t, 1, ExprVersion(expression), "uncached expressions still expose their declared version")
	assert.Equal(t, map[string]bool{"p": true, "c": true, "cr": true}, UsedVars(expression))
	assert.Equal(t, 1, ExprVersion(expression), "compiled expressions read metadata from cache")
	assert.Equal(t, map[string]bool{"p": true, "c": true, "cr": true}, UsedVars(expression))

	assert.Nil(t, UsedVars("invalid +-+ syntax"), "invalid expressions have no usable variable metadata")
}

func TestCompileCacheHitAndCapacityReset(t *testing.T) {
	InvalidateCache()
	t.Cleanup(InvalidateCache)

	expression := "p * 2"
	hash := ExprHashString(expression)
	first, err := CompileFromCacheByHash(expression, hash)
	require.NoError(t, err)
	second, err := CompileFromCacheByHash("this body is ignored on a cache hit", hash)
	require.NoError(t, err)
	assert.Same(t, first, second)

	for i := 0; i < maxCacheSize; i++ {
		expr := fmt.Sprintf("p + %d", i)
		_, err := CompileFromCache(expr)
		require.NoError(t, err)
	}

	cacheMu.RLock()
	size := len(cache)
	cacheMu.RUnlock()
	assert.Less(t, size, maxCacheSize, "the bounded cache must reset instead of growing without limit")
}

func TestRunExprByHashAndErrorPropagation(t *testing.T) {
	t.Parallel()

	expression := `tier("paid", p * 2 + c * 5)`
	cost, trace, err := RunExprByHash(expression, ExprHashString(expression), TokenParams{P: 10, C: 4})
	require.NoError(t, err)
	assert.Equal(t, float64(40), cost)
	assert.Equal(t, "paid", trace.MatchedTier)

	_, _, err = RunExprByHash("invalid +-+ syntax", "deliberately-distinct-hash", TokenParams{})
	require.ErrorContains(t, err, "expr compile error")
}

func TestRequestHelpersNormalizeAndRejectEmptyValues(t *testing.T) {
	t.Parallel()

	expression := `header(" x-plan ") == "premium" && param(" enabled ") == true && !has(nil, "x") && !has("abc", "") ? p : 0`
	cost, _, err := RunExprWithRequest(expression, TokenParams{P: 42}, RequestInput{
		Headers: map[string]string{
			" X-Plan ": " premium ",
			"":         "ignored",
			"X-Empty":  " ",
		},
		Body: []byte(`{"enabled":true}`),
	})
	require.NoError(t, err)
	assert.Equal(t, float64(42), cost)

	cost, _, err = RunExprWithRequest(`param("") == nil && param("missing") == nil ? 1 : 0`, TokenParams{}, RequestInput{})
	require.NoError(t, err)
	assert.Equal(t, float64(1), cost)

	assert.Empty(t, normalizeHeaders(nil))
}

func TestQuotaRoundStrict(t *testing.T) {
	t.Parallel()

	got, err := QuotaRoundStrict(1.5)
	require.NoError(t, err)
	assert.Equal(t, 2, got)

	_, err = QuotaRoundStrict(math.MaxFloat64)
	require.Error(t, err)
}

func TestComputeTieredQuotaPropagatesCompileFailure(t *testing.T) {
	t.Parallel()

	expression := "invalid +-+ syntax"
	result, err := ComputeTieredQuota(&BillingSnapshot{
		ExprString:   expression,
		ExprHash:     ExprHashString(expression),
		GroupRatio:   1,
		QuotaPerUnit: 500_000,
	}, TokenParams{P: 10})

	require.ErrorContains(t, err, "expr compile error")
	assert.Equal(t, TieredResult{}, result)
}

func TestAllTokenDimensionsReachExpression(t *testing.T) {
	t.Parallel()

	expression := strings.Join([]string{"p", "c", "len", "cr", "cc", "cc1h", "img", "img_o", "ai", "ao"}, " + ")
	cost, _, err := RunExpr(expression, TokenParams{
		P: 1, C: 2, Len: 3, CR: 4, CC: 5, CC1h: 6, Img: 7, ImgO: 8, AI: 9, AO: 10,
	})
	require.NoError(t, err)
	assert.Equal(t, float64(55), cost)
}
