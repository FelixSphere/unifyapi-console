package relay

import (
	"encoding/json"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"
)

// keepConvertedSystemPromptCacheable undoes one step of the chat-to-Responses
// conversion: it puts the system prompt back in front of the conversation as a
// message instead of leaving it in `instructions`.
//
// The two spellings mean the same thing to the model on a stateless request,
// but not to the prompt cache. Measured against the supplier on 2026-11-18,
// gpt-6.2-sol and gpt-6.2-astra never cache `instructions` (no cache write, no
// cache read, on four consecutive identical 8.7K-token prompts) and cache the
// same text as a leading developer message on the second call. gpt-6-astra
// caches both. A converted call would therefore pay the full input price for
// a system prompt the native chat endpoint bills at a tenth.
//
// Off unless the policy sets instructions_as_input, so upstream's conversion
// is untouched by default. A request this function does not understand is
// forwarded as the converter built it.
func keepConvertedSystemPromptCacheable(req *dto.OpenAIResponsesRequest) {
	if req == nil || !model_setting.GetGlobalSettings().ChatCompletionsToResponsesPolicy.InstructionsAsInput {
		return
	}
	if len(req.Instructions) == 0 {
		return
	}
	var instructions string
	if err := common.Unmarshal(req.Instructions, &instructions); err != nil || strings.TrimSpace(instructions) == "" {
		return
	}
	var items []json.RawMessage
	if len(req.Input) > 0 {
		if err := common.Unmarshal(req.Input, &items); err != nil {
			return
		}
	}
	lead, err := common.Marshal(map[string]string{"role": "developer", "content": instructions})
	if err != nil {
		return
	}
	input, err := common.Marshal(append([]json.RawMessage{lead}, items...))
	if err != nil {
		return
	}
	req.Input = input
	req.Instructions = nil
}
