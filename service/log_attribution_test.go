package service

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

func TestCustomerGroupForLogKeepsCompanyAndRecordsRoute(t *testing.T) {
	other := map[string]interface{}{}
	group := CustomerGroupForLog(&relaycommon.RelayInfo{
		UserGroup:  "GenAI",
		UsingGroup: "model--claude-opus-5",
	}, other)

	require.Equal(t, "GenAI", group)
	require.Equal(t, "model--claude-opus-5", other["routing_group"])
}

func TestCustomerGroupForLogPreservesLegacyFallback(t *testing.T) {
	other := map[string]interface{}{}
	group := CustomerGroupForLog(&relaycommon.RelayInfo{UsingGroup: "default"}, other)
	require.Equal(t, "default", group)
	require.NotContains(t, other, "routing_group")
}
