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
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// UNIFYAPI-FORK: send reasoning_effort "none" with function tools to models
// whose upstream refuses tools while reasoning is on.
//
// gpt-5.6-luna on /v1/chat/completions answers a request that carries function
// tools AND any reasoning with 400 "Function tools with reasoning_effort are
// not supported for gpt-5.6-luna in /v1/chat/completions. To use function
// tools, use /v1/responses or set reasoning_effort to 'none'." Measured
// 2026-11-08 through production (channel 1, FlatKey):
//
//   - reasoning_effort "medium" + tools: that 400, every time.
//   - reasoning_effort "none"   + tools: 200, tools called, 0 reasoning tokens.
//   - reasoning_effort unset    + tools: usually 200, but the upstream's own
//     default sometimes reasons (9 reasoning tokens observed) and then refuses.
//     A customer's load test hit it 3 times on 2026-11-07, all streaming tool
//     calls, none of which set reasoning_effort; it reported the 400s as
//     "injected by the gateway". The relay never adds the field (checked).
//
// So when the caller sends tools and leaves reasoning_effort unset, we set it
// to "none" -- the value the upstream itself asks for, and what the request
// effectively got on every success. A caller who sets reasoning_effort keeps
// it. Streaming first token with tools was also ~0.25 s faster with "none"
// (median 1.55 s -> 1.30 s over 12 calls each).
//
// Listed per model, exact names, from measurement only. gpt-6-astra hit the
// same 400 four times in Sep-Oct but does NOT accept "none" at all ("does not
// support 'none' with this model"), so adding it here would break every astra
// tool call; it is deliberately absent.
var toolsNeedReasoningNone = map[string]bool{
	"gpt-5.6-luna": true,
}

func applyToolsReasoningDefault(info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) {
	if info == nil || request == nil || info.ChannelMeta == nil {
		return
	}
	if !toolsNeedReasoningNone[info.UpstreamModelName] {
		return
	}
	if len(request.Tools) == 0 || request.ReasoningEffort != "" || len(request.Reasoning) > 0 {
		return
	}
	request.ReasoningEffort = "none"
}
