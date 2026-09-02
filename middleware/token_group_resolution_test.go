package middleware

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAutoTokenGroupKeepsCustomerAsUsingGroup(t *testing.T) {
	assert.Equal(t, "GenAI", effectiveUsingGroup("GenAI", "auto"))
}

func TestExplicitTokenGroupBecomesUsingGroup(t *testing.T) {
	assert.Equal(t, "Premium", effectiveUsingGroup("GenAI", "Premium"))
}

func TestEmptyTokenGroupKeepsCustomerAsUsingGroup(t *testing.T) {
	assert.Equal(t, "GenAI", effectiveUsingGroup("GenAI", ""))
}
