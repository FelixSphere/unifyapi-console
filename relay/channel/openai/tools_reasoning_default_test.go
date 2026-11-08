/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api (Copyright (C) 2023-2026
QuantumNous), distributed under the GNU Affero General Public License v3.
See BRANDING.md for the relationship between this fork and its upstream.

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
package openai

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// gpt-5.6-luna refuses function tools while reasoning is on (see
// tools_reasoning_default.go). These pin what is sent upstream, through the
// adaptor's real conversion, so the wiring is covered and not just the helper.

func lunaToolRequest(model, effort string) *dto.GeneralOpenAIRequest {
	r := req(model)
	r.Tools = []dto.ToolCallRequest{{Type: "function", Function: dto.FunctionRequest{Name: "get_weather"}}}
	r.ReasoningEffort = effort
	return r
}

func convertedBody(t *testing.T, model string, r *dto.GeneralOpenAIRequest) map[string]any {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	out, err := (&Adaptor{}).ConvertOpenAIRequest(c, openAIInfo(model, "https://router.flatkey.ai", nil), r)
	require.NoError(t, err)
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	got := map[string]any{}
	require.NoError(t, json.Unmarshal(raw, &got))
	return got
}

func TestLunaWithToolsAndNoEffortIsSentReasoningNone(t *testing.T) {
	got := convertedBody(t, "gpt-5.6-luna", lunaToolRequest("gpt-5.6-luna", ""))
	require.Equal(t, "none", got["reasoning_effort"])
}

func TestAnEffortTheCallerChoseIsLeftAlone(t *testing.T) {
	got := convertedBody(t, "gpt-5.6-luna", lunaToolRequest("gpt-5.6-luna", "low"))
	require.Equal(t, "low", got["reasoning_effort"])
}

func TestLunaWithoutToolsIsUnchanged(t *testing.T) {
	got := convertedBody(t, "gpt-5.6-luna", req("gpt-5.6-luna"))
	_, has := got["reasoning_effort"]
	require.False(t, has, "no tools, no conflict: the upstream default stands")
}

// gpt-6-astra hit the same 400 historically but rejects "none" outright, so
// it must never receive it from this rule.
func TestOtherModelsAreNeverGivenNone(t *testing.T) {
	for _, model := range []string{"gpt-6-astra", "gpt-5.6-sol", "gpt-5-mini", "gpt-5.6-luna-high"} {
		got := convertedBody(t, model, lunaToolRequest(model, ""))
		require.NotEqual(t, "none", got["reasoning_effort"], model)
	}
}
