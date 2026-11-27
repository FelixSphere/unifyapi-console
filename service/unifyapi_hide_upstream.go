package service

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// UNIFYAPI-FORK: fork-owned. A channel with `hide_upstream` set in its
// settings answers without showing who the upstream is.
//
// # Why this exists
//
// Measured on production 2026-11-26: a customer calling a channel whose
// upstream is a reseller received that reseller's answer unmodified. The body
// said "model":"z-ai/glm-5.3" for a request that asked for glm-5.3, carried
// "provider":"CoreWeave", a "gen-..." id and usage.cost (what the request cost
// US), and the response headers included X-Openrouter-Release, X-Generation-Id
// and the reseller's cookies.
//
// # Why it is done where the response is written
//
// Every adaptor reaches the client through a handful of writers:
// IOCopyBytesGracefully for a whole body, the SSE helpers in relay/helper for a
// stream, ShouldCopyUpstreamHeader for headers and the relay controller for an
// error. Rewriting there covers every adaptor and every conversion between
// formats with one rule set, and it cannot touch billing: usage is read from
// the upstream's bytes before they get here, and what is rewritten is a copy.
//
// # Why it recognises shapes rather than formats
//
// The writers do not know which protocol a payload is in, and one channel can
// answer in several (an OpenAI channel serves /v1/messages through a
// converter). Each rule below names a field by where it sits and applies only
// when that field is present, so a payload that has none of them leaves
// byte for byte as it arrived.

// requestedModelContextKey holds the model name the client sent. The context's
// "original_model" is not that: a retry rewrites it from RelayInfo, whose
// OriginModelName has by then been changed by the compaction and Gemini
// thinking paths, once to a name built from the upstream's.
const requestedModelContextKey = "unifyapi_requested_model"

// RememberRequestedModel records the model the client asked for. Only the
// first call counts; that one comes from the distributor, before any relay
// code has had a chance to rename the model.
func RememberRequestedModel(c *gin.Context, modelName string) {
	if c == nil || modelName == "" {
		return
	}
	if _, exists := c.Get(requestedModelContextKey); exists {
		return
	}
	c.Set(requestedModelContextKey, strings.TrimSuffix(modelName, ratio_setting.CompactModelSuffix))
}

// requestedModel is the model name the client sent. Outside the distributor
// (a test, a route that selects no channel) nothing was remembered and the
// context's own name for the model is the best there is.
func requestedModel(c *gin.Context) string {
	if remembered := c.GetString(requestedModelContextKey); remembered != "" {
		return remembered
	}
	return c.GetString(string(constant.ContextKeyOriginalModel))
}

// HidesUpstream reports whether the channel serving this request has
// hide_upstream set.
func HidesUpstream(c *gin.Context) bool {
	if c == nil {
		return false
	}
	settings, ok := common.GetContextKeyType[dto.ChannelOtherSettings](c, constant.ContextKeyChannelOtherSetting)
	return ok && settings.HideUpstream
}

// upstreamHeadersKeptWhenHidden are the upstream response headers a client
// needs in order to read the body. Everything else an upstream sends describes
// the upstream: its request ids, its edge, its cookies, its rate limits on our
// key.
var upstreamHeadersKeptWhenHidden = map[string]struct{}{
	"Content-Type":        {},
	"Content-Encoding":    {},
	"Content-Disposition": {},
	"Cache-Control":       {},
}

func keepsUpstreamHeaderWhenHidden(name string) bool {
	_, kept := upstreamHeadersKeptWhenHidden[http.CanonicalHeaderKey(name)]
	return kept
}

// upstreamOnlyUsageFields are what a reseller adds to usage to bill US: its
// price for the request. Token counts are not in this list and are never
// touched.
//
// billing_usage is ours, not the reseller's: the converters attach the usage
// they settle from to a Claude-format answer, and it is serialised with it. It
// repeats the reseller's cost and names the protocol the upstream speaks
// ("oai_chat" on a /v1/messages answer), and no API format has such a field.
var upstreamOnlyUsageFields = []string{"cost", "cost_details", "is_byok", "billing_usage"}

// HideUpstreamInBody returns what the client should receive in place of a
// response body, or of the data of one stream event. It returns its argument
// untouched unless the channel hides its upstream and the payload is a JSON
// object. The argument is never modified.
func HideUpstreamInBody[T string | []byte](c *gin.Context, payload T) T {
	if !HidesUpstream(c) {
		return payload
	}
	body := []byte(payload)
	if !bytes.HasPrefix(bytes.TrimLeft(body, " \t\r\n"), []byte("{")) || !gjson.ValidBytes(body) {
		return payload
	}

	requested := requestedModel(c)
	requestID := c.GetString(common.RequestIdKey)
	rewritten := hiddenUpstreamBody{json: body}

	// The same fields sit at the top of a whole answer, under "message" in a
	// Claude message_start and under "response" in a Responses API event.
	for _, scope := range []string{"", "message.", "response."} {
		rewritten.replaceString(scope+"model", requested)
		rewritten.remove(scope + "provider")
		for _, field := range upstreamOnlyUsageFields {
			rewritten.remove(scope + "usage." + field)
		}
	}
	rewritten.replaceString("modelVersion", requested)
	rewritten.replaceString("responseId", requestID)

	object := gjson.GetBytes(body, "object").String()
	eventType := gjson.GetBytes(body, "type").String()
	choices := gjson.GetBytes(body, "choices")
	switch {
	case object == "text_completion":
		rewritten.replaceString("id", "cmpl-"+requestID)
	case strings.HasPrefix(object, "chat.completion") || choices.IsArray():
		rewritten.replaceString("id", "chatcmpl-"+requestID)
	case object == "response":
		rewritten.replaceString("id", "resp_"+requestID)
	case eventType == "message":
		rewritten.replaceString("id", "msg_"+requestID)
	}
	if eventType == "message_start" {
		rewritten.replaceString("message.id", "msg_"+requestID)
	}
	if gjson.GetBytes(body, "response.object").String() == "response" {
		rewritten.replaceString("response.id", "resp_"+requestID)
	}

	for i := range choices.Array() {
		choice := "choices." + strconv.Itoa(i) + "."
		rewritten.remove(choice + "native_finish_reason")
		for _, holder := range []string{"message.", "delta."} {
			rewritten.remove(choice + holder + "reasoning_details")
			rewritten.renameReasoning(choice + holder)
		}
	}

	// An error frame inside a stream is the upstream's own error object.
	rewritten.remove("error.metadata")
	if message := gjson.GetBytes(body, "error.message"); message.Type == gjson.String {
		rewritten.replaceString("error.message", hideUpstreamInText(c, message.String()))
	}

	return T(rewritten.json)
}

// hiddenUpstreamBody edits a JSON document field by field, leaving every byte
// it is not asked about as it was. Decoding into a struct and encoding again
// would drop the fields our DTOs do not know and reorder the rest.
type hiddenUpstreamBody struct {
	json []byte
}

// replaceString overwrites a string field. A field that is absent, or is not a
// string, stays as it is, so nothing is ever added to a payload; and a field
// is never blanked when there is nothing to put in it.
func (b *hiddenUpstreamBody) replaceString(path string, value string) {
	current := gjson.GetBytes(b.json, path)
	if value == "" || current.Type != gjson.String || current.String() == value {
		return
	}
	if edited, err := sjson.SetBytes(b.json, path, value); err == nil {
		b.json = edited
	}
}

func (b *hiddenUpstreamBody) remove(path string) {
	if !gjson.GetBytes(b.json, path).Exists() {
		return
	}
	if edited, err := sjson.DeleteBytes(b.json, path); err == nil {
		b.json = edited
	}
}

// renameReasoning turns the "reasoning" some resellers send into the
// "reasoning_content" the DeepSeek, Qwen and GLM APIs send themselves. A
// message that already has reasoning_content keeps both fields as they are.
func (b *hiddenUpstreamBody) renameReasoning(holder string) {
	reasoning := gjson.GetBytes(b.json, holder+"reasoning")
	if !reasoning.Exists() {
		return
	}
	if reasoning.Type == gjson.Null {
		b.remove(holder + "reasoning")
		return
	}
	if gjson.GetBytes(b.json, holder+"reasoning_content").String() != "" {
		return
	}
	edited, err := sjson.SetRawBytes(b.json, holder+"reasoning_content", []byte(reasoning.Raw))
	if err != nil {
		return
	}
	b.json = edited
	b.remove(holder + "reasoning")
}

// upstreamRequestIDNote is how every new-api deployment ends an error message.
// When the upstream is one, the id in it is the upstream's.
var upstreamRequestIDNote = regexp.MustCompile(`\s*\(request id: [^)]*\)`)

// hideUpstreamInText removes what identifies the upstream from a sentence the
// upstream wrote, and keeps the sentence: a caller told "max_tokens is too
// large" can act on it, a caller told "upstream error" cannot.
func hideUpstreamInText(c *gin.Context, text string) string {
	text = upstreamRequestIDNote.ReplaceAllString(text, "")

	requested := requestedModel(c)
	if upstreamModel := mappedUpstreamModel(c, requested); upstreamModel != "" {
		text = strings.ReplaceAll(text, upstreamModel, requested)
	}

	// The upstream's name is not configured anywhere, but its address is, and
	// "openrouter.ai" names OpenRouter. Labels under four letters are "api",
	// "www" and the like.
	baseURL, err := url.Parse(common.GetContextKeyString(c, constant.ContextKeyChannelBaseUrl))
	if err != nil {
		return text
	}
	labels := strings.Split(baseURL.Hostname(), ".")
	for _, label := range labels[:len(labels)-1] {
		if len(label) < 4 {
			continue
		}
		text = regexp.MustCompile(`(?i)`+regexp.QuoteMeta(label)).ReplaceAllString(text, "upstream")
	}
	return text
}

// mappedUpstreamModel follows the channel's model mapping from the requested
// model to the name sent upstream, the way ModelMappedHelper does. It returns
// "" when the channel does not rename the model.
func mappedUpstreamModel(c *gin.Context, requestedModel string) string {
	mapping := map[string]string{}
	if err := common.UnmarshalJsonStr(common.GetContextKeyString(c, constant.ContextKeyChannelModelMapping), &mapping); err != nil {
		return ""
	}
	current := requestedModel
	for range mapping {
		next := mapping[current]
		if next == "" || next == current {
			break
		}
		current = next
	}
	if current == requestedModel {
		return ""
	}
	return current
}

// HideUpstreamInError rewrites an error before it is written to the client of
// a channel that hides its upstream. The status code is kept. The message is
// kept too, minus the upstream's name, its model id and its request id; the
// reseller's metadata blob (provider name, raw provider body) is dropped.
//
// The error log row and the backend log are written before this runs and keep
// the full text.
func HideUpstreamInError(c *gin.Context, apiErr *types.NewAPIError) {
	if apiErr == nil || !HidesUpstream(c) {
		return
	}
	metadata := apiErr.Metadata
	apiErr.Metadata = nil
	switch relayError := apiErr.RelayError.(type) {
	case types.OpenAIError:
		relayError.Message = hideUpstreamInErrorMessage(c, relayError.Message, metadata)
		relayError.Type = hideUpstreamInText(c, relayError.Type)
		if code, ok := relayError.Code.(string); ok {
			relayError.Code = hideUpstreamInText(c, code)
		}
		relayError.Metadata = nil
		apiErr.RelayError = relayError
	case types.ClaudeError:
		relayError.Message = hideUpstreamInErrorMessage(c, relayError.Message, metadata)
		relayError.Type = hideUpstreamInText(c, relayError.Type)
		apiErr.RelayError = relayError
	}
	apiErr.SetMessage(hideUpstreamInErrorMessage(c, apiErr.Error(), metadata))
}

// hideUpstreamInErrorMessage undoes the two things types.WithOpenAIError does
// with a reseller's metadata -- appending the blob, or naming the provider it
// came from -- and then cleans the sentence that is left.
func hideUpstreamInErrorMessage(c *gin.Context, message string, metadata []byte) string {
	if len(metadata) > 0 {
		message = strings.Replace(message, fmt.Sprintf(" (%s)", metadata), "", 1)
		if provider := gjson.GetBytes(metadata, "provider_name").String(); provider != "" {
			message = strings.TrimPrefix(message, provider+" rejected the request: ")
		}
	}
	return hideUpstreamInText(c, message)
}
