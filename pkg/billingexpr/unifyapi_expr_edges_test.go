package billingexpr

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAnExpressionThatCannotProduceANumberIsRejected.
//
// This is the failure mode that matters most in the package. An expression is
// admin-authored; a typo that evaluates to a string or a bool is entirely
// possible. If a non-numeric result were coerced to 0, every request priced by
// that expression would be billed nothing, and nothing would be logged.
//
// The guard is stronger than a runtime check: the compiler is told the result
// must be a float64, so these are rejected at SAVE time and never reach the
// billing path at all.
func TestAnExpressionThatCannotProduceANumberIsRejected(t *testing.T) {
	for _, exprStr := range []string{
		`"not a number"`,
		`true`,
		`[1, 2, 3]`,
		`header("x-tier")`,
	} {
		_, _, err := RunExpr(exprStr, TokenParams{P: 1000, C: 500})
		require.Error(t, err, "expression %q must not silently price at zero", exprStr)
		assert.Contains(t, err.Error(), "compile error",
			"a non-numeric expression must fail to compile, not fail late at billing time")
	}
}

// TestATypeFaultTheCompilerCannotSeeStillFailsLoudly.
//
// param() reads request JSON, so its type is unknown until a request arrives.
// A rule that multiplies a string field by a rate compiles fine and faults on
// the first real request. It must come back as an error -- the caller then
// falls back to the flat rate -- rather than as a zero.
func TestATypeFaultTheCompilerCannotSeeStillFailsLoudly(t *testing.T) {
	for _, exprStr := range []string{
		`param("model")`,
		`param("model") + 1`,
	} {
		out, _, err := RunExprWithRequest(exprStr, TokenParams{P: 1000},
			RequestInput{Body: []byte(`{"model":"gpt-4o"}`)})

		require.Error(t, err, "expression %q faulted at runtime and must say so", exprStr)
		assert.Contains(t, err.Error(), "run error")
		assert.Zero(t, out, "the value is meaningless when the error is set; the caller must check err")
	}
}

// TestUsedVarsOnAnUnparseableExpression returns nil rather than an empty map.
//
// Callers use UsedVars to decide which usage counters an expression needs. A
// nil result means "cannot tell", and the caller supplies everything; an empty
// map would mean "needs nothing", and the expression would be evaluated
// against zeroed inputs -- again, a zero bill.
func TestUsedVarsOnAnUnparseableExpression(t *testing.T) {
	assert.Nil(t, UsedVars(`p * (((`), "a broken expression must not report an empty variable set")
	assert.Nil(t, UsedVars(""), "an absent expression uses nothing at all")

	vars := UsedVars(`p * 0.000002 + c * 0.00001`)
	require.NotNil(t, vars)
	assert.True(t, vars["p"])
	assert.True(t, vars["c"])
	assert.False(t, vars["ai"])
}

// TestUsedVarsIsServedFromTheCompileCache on the second call. The relay asks
// for this on every request; recompiling each time would put the expr parser
// on the hot path.
func TestUsedVarsIsServedFromTheCompileCache(t *testing.T) {
	const exprStr = `len * 0.0000015 + c * 0.000006`
	InvalidateCache()

	first := UsedVars(exprStr)
	second := UsedVars(exprStr)

	require.NotNil(t, first)
	assert.Equal(t, first, second)
	assert.True(t, first["len"] && first["c"])
	assert.False(t, first["p"], "len and p are different inputs -- p has sub-categories subtracted")
}
