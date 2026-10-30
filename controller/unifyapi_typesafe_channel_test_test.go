/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The channel test sends a chat completion by default. A TypeSafe channel has
// no chat surface and refuses one by design, so the operator saw a healthy
// channel report "typesafe serves System One requests at /v1/systemone" and
// had no dropdown entry to fix it with. The channel's own type has to settle
// the shape of its test.

func TestATypeSafeChannelIsTestedAsSystemOneEvenOnAutoDetect(t *testing.T) {
	channel := &model.Channel{Type: constant.ChannelTypeTypeSafe}

	// "" is what the dialog sends for "Auto detect (default)".
	for _, endpointType := range []string{"", string(constant.EndpointTypeSystemOne)} {
		request := buildTestRequest("jev-1.13", endpointType, channel, false)

		systemOne, ok := request.(*dto.SystemOneRequest)
		require.True(t, ok, "endpoint type %q produced %T, which the TypeSafe adaptor refuses", endpointType, request)
		assert.Equal(t, "jev-1.13", systemOne.Model)
		assert.NotNil(t, systemOne.State, "a System One call without a state is rejected before it reaches the vendor")
		require.Len(t, systemOne.Questions, 1, "one question keeps the test to a few input tokens")
	}
}

// Picking chat explicitly on a TypeSafe channel must not silently test
// something else, but it must not be the shape either: the channel type wins,
// so an operator cannot produce a red cross on a working channel.
func TestChoosingChatOnATypeSafeChannelStillTestsSystemOne(t *testing.T) {
	request := buildTestRequest("jev-1.13", string(constant.EndpointTypeOpenAI),
		&model.Channel{Type: constant.ChannelTypeTypeSafe}, false)

	_, ok := request.(*dto.SystemOneRequest)
	assert.True(t, ok, "the channel type decides, not the dropdown")
}

// The guard must be exactly that narrow: every other channel keeps the chat
// test it has always had.
func TestOtherChannelsAreStillTestedAsChat(t *testing.T) {
	request := buildTestRequest("gpt-4o", "", &model.Channel{Type: constant.ChannelTypeOpenAI}, false)

	_, ok := request.(*dto.GeneralOpenAIRequest)
	assert.True(t, ok, "an OpenAI channel must still be tested with a chat completion, got %T", request)
}
