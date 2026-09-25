package oaichat

import (
	"encoding/json"
	"fmt"
	"strings"

	"context"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	relaymedia "github.com/QuantumNous/new-api/relaykit/relayconvert/internal/media"
	sharedclaude "github.com/QuantumNous/new-api/relaykit/relayconvert/internal/shared/claude"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/reasoning"
)

const (
	webSearchMaxUsesLow    = 1
	webSearchMaxUsesMedium = 5
	webSearchMaxUsesHigh   = 10
)

type openRouterRequestReasoning struct {
	Enabled   bool   `json:"enabled"`
	Effort    string `json:"effort,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
	Exclude   bool   `json:"exclude,omitempty"`
}

// claudeOutputFormat maps OpenAI's response_format onto Anthropic's
// output_format.
//
// Only json_schema carries over. "json_object" asks for valid JSON without
// saying which shape, and Anthropic's output_format has no equivalent of that,
// so sending one without a schema would be rejected -- that case is left as it
// behaves today rather than converted into a 400 the caller did not have
// before.
func claudeOutputFormat(responseFormat *dto.ResponseFormat) json.RawMessage {
	if responseFormat == nil || responseFormat.Type != "json_schema" || len(responseFormat.JsonSchema) == 0 {
		return nil
	}
	var jsonSchema dto.FormatJsonSchema
	if err := kitutil.Unmarshal(responseFormat.JsonSchema, &jsonSchema); err != nil || jsonSchema.Schema == nil {
		return nil
	}
	encoded, err := kitutil.Marshal(map[string]any{
		"type":   "json_schema",
		"schema": jsonSchema.Schema,
	})
	if err != nil {
		return nil
	}
	return encoded
}

func OpenAIChatRequestToClaudeMessages(c context.Context, info convmeta.Meta, textRequest dto.GeneralOpenAIRequest) (*dto.ClaudeRequest, error) {
	opts := convmeta.OptionsOf(info)
	claudeTools := make([]any, 0, len(textRequest.Tools))

	for _, tool := range textRequest.Tools {
		if params, ok := tool.Function.Parameters.(map[string]any); ok {
			claudeTool := dto.Tool{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
			}
			claudeTool.InputSchema = make(map[string]interface{})
			if params["type"] != nil {
				claudeTool.InputSchema["type"] = params["type"].(string)
			}
			claudeTool.InputSchema["properties"] = params["properties"]
			claudeTool.InputSchema["required"] = params["required"]
			for key, value := range params {
				if key == "type" || key == "properties" || key == "required" {
					continue
				}
				claudeTool.InputSchema[key] = value
			}
			claudeTools = append(claudeTools, &claudeTool)
		}
	}

	if textRequest.WebSearchOptions != nil {
		webSearchTool := dto.ClaudeWebSearchTool{
			Type: "web_search_20250305",
			Name: "web_search",
		}

		if textRequest.WebSearchOptions.UserLocation != nil {
			anthropicUserLocation := &dto.ClaudeWebSearchUserLocation{
				Type: "approximate",
			}

			var userLocationMap map[string]interface{}
			if err := kitutil.Unmarshal(textRequest.WebSearchOptions.UserLocation, &userLocationMap); err == nil {
				if approximateData, ok := userLocationMap["approximate"].(map[string]interface{}); ok {
					if timezone, ok := approximateData["timezone"].(string); ok && timezone != "" {
						anthropicUserLocation.Timezone = timezone
					}
					if country, ok := approximateData["country"].(string); ok && country != "" {
						anthropicUserLocation.Country = country
					}
					if region, ok := approximateData["region"].(string); ok && region != "" {
						anthropicUserLocation.Region = region
					}
					if city, ok := approximateData["city"].(string); ok && city != "" {
						anthropicUserLocation.City = city
					}
				}
			}

			webSearchTool.UserLocation = anthropicUserLocation
		}

		switch textRequest.WebSearchOptions.SearchContextSize {
		case "low":
			webSearchTool.MaxUses = webSearchMaxUsesLow
		case "medium":
			webSearchTool.MaxUses = webSearchMaxUsesMedium
		case "high":
			webSearchTool.MaxUses = webSearchMaxUsesHigh
		}

		claudeTools = append(claudeTools, &webSearchTool)
	}

	claudeRequest := dto.ClaudeRequest{
		Model:         textRequest.Model,
		StopSequences: nil,
		Temperature:   textRequest.Temperature,
		Tools:         claudeTools,
	}
	if maxTokens := textRequest.GetMaxTokens(); maxTokens > 0 {
		claudeRequest.MaxTokens = kitutil.GetPointer(maxTokens)
	}
	if textRequest.TopP != nil {
		claudeRequest.TopP = kitutil.GetPointer(*textRequest.TopP)
	}
	if textRequest.TopK != nil {
		claudeRequest.TopK = kitutil.GetPointer(*textRequest.TopK)
	}
	if textRequest.IsStream(nil) {
		claudeRequest.Stream = kitutil.GetPointer(true)
	}

	if textRequest.ToolChoice != nil || textRequest.ParallelTooCalls != nil {
		claudeToolChoice := sharedclaude.MapOpenAIToolChoice(textRequest.ToolChoice, textRequest.ParallelTooCalls)
		if claudeToolChoice != nil {
			claudeRequest.ToolChoice = claudeToolChoice
		}
	}

	// UNIFYAPI: carry the caller's structured-output request across to Anthropic.
	//
	// Anthropic Messages has no response_format, but it does have output_format,
	// and dto.ClaudeRequest already carries that as a passthrough -- nothing was
	// ever assigning it. So the field was dropped here in silence: the caller
	// asked for json_schema, got HTTP 200 with free-form markdown prose, and
	// their JSON parser failed on a response that said nothing about why. The
	// Gemini converter has mapped this since it was written; only this one did
	// not.
	if outputFormat := claudeOutputFormat(textRequest.ResponseFormat); len(outputFormat) > 0 {
		claudeRequest.OutputFormat = outputFormat
	}

	if claudeRequest.MaxTokens == nil || *claudeRequest.MaxTokens == 0 {
		if defaultMaxTokens, configured := opts.Claude.DefaultMaxTokensFor(textRequest.Model); configured {
			value := uint(defaultMaxTokens)
			claudeRequest.MaxTokens = &value
		}
	}

	if baseModel, effortLevel, ok := reasoning.TrimEffortSuffix(textRequest.Model); ok && effortLevel != "" &&
		(strings.HasPrefix(textRequest.Model, "claude-opus-4-6") ||
			strings.HasPrefix(textRequest.Model, "claude-opus-4-7") ||
			strings.HasPrefix(textRequest.Model, "claude-opus-4-8")) {
		claudeRequest.Model = baseModel
		claudeRequest.Thinking = &dto.Thinking{
			Type: "adaptive",
		}
		claudeRequest.OutputConfig = json.RawMessage(fmt.Sprintf(`{"effort":"%s"}`, effortLevel))
		if strings.HasPrefix(baseModel, "claude-opus-4-7") ||
			strings.HasPrefix(baseModel, "claude-opus-4-8") {
			claudeRequest.Thinking.Display = "summarized"
			claudeRequest.Temperature = nil
			claudeRequest.TopP = nil
			claudeRequest.TopK = nil
		} else {
			claudeRequest.TopP = nil
			claudeRequest.Temperature = kitutil.GetPointer[float64](1.0)
		}
	} else if opts.Claude.ThinkingAdapterEnabled &&
		strings.HasSuffix(textRequest.Model, "-thinking") {

		trimmedModel := strings.TrimSuffix(textRequest.Model, "-thinking")
		if strings.HasPrefix(trimmedModel, "claude-opus-4-7") ||
			strings.HasPrefix(trimmedModel, "claude-opus-4-8") {
			claudeRequest.Thinking = &dto.Thinking{Type: "adaptive", Display: "summarized"}
			claudeRequest.OutputConfig = json.RawMessage(`{"effort":"high"}`)
			claudeRequest.Temperature = nil
			claudeRequest.TopP = nil
			claudeRequest.TopK = nil
		} else {
			if claudeRequest.MaxTokens == nil || *claudeRequest.MaxTokens < 1280 {
				claudeRequest.MaxTokens = kitutil.GetPointer[uint](1280)
			}

			claudeRequest.Thinking = &dto.Thinking{
				Type:         "enabled",
				BudgetTokens: kitutil.GetPointer[int](int(float64(*claudeRequest.MaxTokens) * opts.Claude.ThinkingAdapterBudgetTokensPercentage)),
			}
			claudeRequest.TopP = nil
			claudeRequest.Temperature = kitutil.GetPointer[float64](1.0)
		}
		if !opts.ShouldPreserveThinkingSuffix(textRequest.Model) {
			claudeRequest.Model = trimmedModel
		}
	}

	if textRequest.ReasoningEffort != "" {
		switch textRequest.ReasoningEffort {
		case "low":
			claudeRequest.Thinking = &dto.Thinking{
				Type:         "enabled",
				BudgetTokens: kitutil.GetPointer[int](1280),
			}
		case "medium":
			claudeRequest.Thinking = &dto.Thinking{
				Type:         "enabled",
				BudgetTokens: kitutil.GetPointer[int](2048),
			}
		case "high":
			claudeRequest.Thinking = &dto.Thinking{
				Type:         "enabled",
				BudgetTokens: kitutil.GetPointer[int](4096),
			}
		}
	}

	if textRequest.Reasoning != nil {
		var reasoningConfig openRouterRequestReasoning
		if err := kitutil.Unmarshal(textRequest.Reasoning, &reasoningConfig); err != nil {
			return nil, err
		}

		budgetTokens := reasoningConfig.MaxTokens
		if budgetTokens > 0 {
			claudeRequest.Thinking = &dto.Thinking{
				Type:         "enabled",
				BudgetTokens: &budgetTokens,
			}
		}
	}

	if textRequest.Stop != nil {
		switch stop := textRequest.Stop.(type) {
		case string:
			claudeRequest.StopSequences = []string{stop}
		case []interface{}:
			stopSequences := make([]string, 0)
			for _, item := range stop {
				stopSequences = append(stopSequences, item.(string))
			}
			claudeRequest.StopSequences = stopSequences
		}
	}

	formatMessages := make([]dto.Message, 0)
	lastMessage := dto.Message{
		Role: "tool",
	}
	for i, message := range textRequest.Messages {
		if message.Role == "" {
			textRequest.Messages[i].Role = "user"
		}
		fmtMessage := dto.Message{
			Role:    message.Role,
			Content: message.Content,
		}
		if message.Role == "tool" {
			fmtMessage.ToolCallId = message.ToolCallId
		}
		if message.Role == "assistant" && message.ToolCalls != nil {
			fmtMessage.ToolCalls = message.ToolCalls
		}
		if lastMessage.Role == message.Role && lastMessage.Role != "tool" {
			if lastMessage.IsStringContent() && message.IsStringContent() {
				fmtMessage.SetStringContent(strings.Trim(fmt.Sprintf("%s %s", lastMessage.StringContent(), message.StringContent()), "\""))
				formatMessages = formatMessages[:len(formatMessages)-1]
			}
		}
		if fmtMessage.Content == nil || (fmtMessage.IsStringContent() && fmtMessage.StringContent() == "") {
			fmtMessage.SetStringContent("...")
		}
		formatMessages = append(formatMessages, fmtMessage)
		lastMessage = fmtMessage
	}

	claudeMessages := make([]dto.ClaudeMessage, 0)
	isFirstMessage := true
	var systemMessages []dto.ClaudeMediaMessage

	for _, message := range formatMessages {
		if message.Role == "system" {
			if message.IsStringContent() {
				if text := message.StringContent(); text != "" {
					systemMessages = append(systemMessages, dto.ClaudeMediaMessage{
						Type: "text",
						Text: kitutil.GetPointer[string](text),
					})
				}
			} else {
				for _, ctx := range message.ParseContent() {
					if ctx.Type == "text" && ctx.Text != "" {
						systemMessages = append(systemMessages, dto.ClaudeMediaMessage{
							Type:         "text",
							Text:         kitutil.GetPointer[string](ctx.Text),
							CacheControl: ctx.CacheControl, // UNIFYAPI: prompt caching
						})
					}
				}
			}
			continue
		}

		if isFirstMessage {
			isFirstMessage = false
			if message.Role != "user" {
				claudeMessage := dto.ClaudeMessage{
					Role: "user",
					Content: []dto.ClaudeMediaMessage{
						{
							Type: "text",
							Text: kitutil.GetPointer[string]("..."),
						},
					},
				}
				claudeMessages = append(claudeMessages, claudeMessage)
			}
		}

		claudeMessage := dto.ClaudeMessage{
			Role: message.Role,
		}
		if message.Role == "tool" {
			if len(claudeMessages) > 0 && claudeMessages[len(claudeMessages)-1].Role == "user" {
				lastClaudeMessage := claudeMessages[len(claudeMessages)-1]
				if content, ok := lastClaudeMessage.Content.(string); ok {
					lastClaudeMessage.Content = []dto.ClaudeMediaMessage{
						{
							Type: "text",
							Text: kitutil.GetPointer[string](content),
						},
					}
				}
				lastClaudeMessage.Content = append(lastClaudeMessage.Content.([]dto.ClaudeMediaMessage), dto.ClaudeMediaMessage{
					Type:      "tool_result",
					ToolUseId: message.ToolCallId,
					Content:   message.Content,
				})
				claudeMessages[len(claudeMessages)-1] = lastClaudeMessage
				continue
			}

			claudeMessage.Role = "user"
			claudeMessage.Content = []dto.ClaudeMediaMessage{
				{
					Type:      "tool_result",
					ToolUseId: message.ToolCallId,
					Content:   message.Content,
				},
			}
		} else if message.IsStringContent() && message.ToolCalls == nil {
			text := message.StringContent()
			if text == "" {
				text = "..."
			}
			claudeMessage.Content = text
		} else {
			claudeMediaMessages := make([]dto.ClaudeMediaMessage, 0)
			for _, mediaMessage := range message.ParseContent() {
				switch mediaMessage.Type {
				case "text":
					if mediaMessage.Text != "" {
						claudeMediaMessages = append(claudeMediaMessages, dto.ClaudeMediaMessage{
							Type:         "text",
							Text:         kitutil.GetPointer[string](mediaMessage.Text),
							CacheControl: mediaMessage.CacheControl, // UNIFYAPI: prompt caching
						})
					}
				default:
					source := mediaMessage.ToFileSource()
					if source == nil {
						continue
					}
					base64Data, mimeType, err := relaymedia.ResolveBase64Data(c, source, "formatting image for Claude")
					if err != nil {
						return nil, fmt.Errorf("get file data failed: %s", err.Error())
					}
					claudeMediaMessage := dto.ClaudeMediaMessage{
						Source: &dto.ClaudeMessageSource{
							Type: "base64",
						},
						CacheControl: mediaMessage.CacheControl, // UNIFYAPI: prompt caching
					}
					if strings.HasPrefix(mimeType, "application/pdf") {
						claudeMediaMessage.Type = "document"
					} else {
						claudeMediaMessage.Type = "image"
					}

					claudeMediaMessage.Source.MediaType = mimeType
					claudeMediaMessage.Source.Data = base64Data
					claudeMediaMessages = append(claudeMediaMessages, claudeMediaMessage)
					continue
				}
			}

			if message.ToolCalls != nil {
				for _, toolCall := range message.ParseToolCalls() {
					inputObj := make(map[string]any)
					if args := toolCall.Function.Arguments; args != "" {
						if err := kitutil.Unmarshal([]byte(args), &inputObj); err != nil {
							kitutil.LogInfo("tool call function arguments is not a map[string]any: " + fmt.Sprintf("%v", toolCall.Function.Arguments))
						}
					}
					claudeMediaMessages = append(claudeMediaMessages, dto.ClaudeMediaMessage{
						Type:  "tool_use",
						Id:    toolCall.ID,
						Name:  toolCall.Function.Name,
						Input: inputObj,
					})
				}
			}
			claudeMessage.Content = claudeMediaMessages
		}
		claudeMessages = append(claudeMessages, claudeMessage)
	}

	if len(systemMessages) > 0 {
		claudeRequest.System = systemMessages
	}

	claudeRequest.Prompt = ""
	claudeRequest.Messages = claudeMessages
	// Checked last so every injection path (default hook, thinking adapter
	// floor) has had its chance to satisfy the required field.
	if claudeRequest.MaxTokens == nil {
		return nil, sharedclaude.ErrMissingMaxTokens
	}
	return &claudeRequest, nil
}
