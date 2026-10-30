/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

// Package typesafe relays TypeSafe's System One API.
//
// Every other channel here speaks some dialect of chat: messages in, text out.
// TypeSafe does not. A request is a state plus a map of named typed questions;
// the reply is a typed answer per question with calibrated probabilities. The
// whole point of the model is that structure, so it is forwarded verbatim in
// both directions rather than squeezed through a chat shape -- packing a
// probability map into an assistant message would throw away the only thing
// the model is for.
//
// Only the model name is ever rewritten, and only when the channel maps it.
//
// https://docs.typesafe.ai/api
package typesafe

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

const ChannelName = "typesafe"

// RequestPath is the vendor's single evaluation endpoint. Every System One
// model is selected by the `model` field rather than by path.
const RequestPath = "/v1/systemone"

type Adaptor struct{}

func (a *Adaptor) Init(_ *relaycommon.RelayInfo) {}

// GetRequestURL builds the evaluation endpoint.
//
// The vendor serves /v1/systemone, and an aggregator that fronts it may mount
// the same API anywhere: OpenRouter keeps the vendor's path under its own /api
// base, others use a different prefix entirely. So the path is configuration.
// Precedence: the channel's System One path if set, otherwise the vendor's,
// and a base that already ends in the path it needs is left alone so an
// operator who pasted the full endpoint does not call /v1/systemone twice.
func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	base := strings.TrimSuffix(info.ChannelBaseUrl, "/")
	if base == "" {
		return "", errors.New("typesafe channel has no base url")
	}

	path := strings.TrimSpace(info.ChannelOtherSettings.SystemOnePath)
	if path == "" {
		path = RequestPath
	} else if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	path = strings.TrimSuffix(path, "/")

	if strings.HasSuffix(base, path) {
		return base, nil
	}
	return base + path, nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, req)
	req.Set("Authorization", "Bearer "+info.ApiKey)
	return nil
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

// DoResponse forwards the vendor's answer to the caller unchanged and reports
// what it says was consumed. Output tokens are counted here and priced at zero
// by the catalog, because the vendor meters no output.
func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	if resp == nil {
		return nil, types.NewError(errors.New("typesafe returned no response"), types.ErrorCodeBadResponse)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeReadResponseBodyFailed)
	}
	service.CloseResponseBodyGracefully(resp)

	var parsed dto.SystemOneResponse
	if err := common.Unmarshal(body, &parsed); err != nil {
		return nil, types.NewError(fmt.Errorf("typesafe returned a body that is not System One JSON: %w", err), types.ErrorCodeBadResponseBody)
	}
	if parsed.Usage == nil {
		// Billing cannot be invented. Refusing here means the customer is not
		// charged for a response nobody can account for, and the operator sees
		// why in the log rather than discovering a silent zero-cost path.
		return nil, types.NewError(errors.New("typesafe response carried no usage; refusing to bill an unmetered call"), types.ErrorCodeBadResponseBody)
	}

	service.IOCopyBytesGracefully(c, resp, body)

	usage := &dto.Usage{
		PromptTokens:     parsed.Usage.InputTokens,
		CompletionTokens: parsed.Usage.OutputTokens,
		TotalTokens:      parsed.Usage.InputTokens + parsed.Usage.OutputTokens,
	}
	return usage, nil
}

func (a *Adaptor) GetModelList() []string { return ModelList }

func (a *Adaptor) GetChannelName() string { return ChannelName }

// The chat-shaped conversions below have no meaning for a decision model: a
// System One request needs a state and typed questions, which a chat request
// does not carry and cannot be guessed from. Refusing is the honest answer --
// inventing a mapping would bill a customer for a translation nobody asked for.
//
// It is the caller's request that is wrong, not the channel, so the refusal is
// a 400: a 500 reads as our outage on dashboards and in the probe journeys. A
// fresh error per call, because the handlers apply options such as skip-retry
// to it in place.
func errNotChat() error {
	return types.NewErrorWithStatusCode(
		errors.New("typesafe serves System One requests at /v1/systemone; it has no chat, embedding, rerank, audio or image API"),
		types.ErrorCodeInvalidRequest,
		http.StatusBadRequest,
		types.ErrOptionWithSkipRetry(),
	)
}

func (a *Adaptor) ConvertOpenAIRequest(*gin.Context, *relaycommon.RelayInfo, *dto.GeneralOpenAIRequest) (any, error) {
	return nil, errNotChat()
}

func (a *Adaptor) ConvertClaudeRequest(*gin.Context, *relaycommon.RelayInfo, *dto.ClaudeRequest) (any, error) {
	return nil, errNotChat()
}

func (a *Adaptor) ConvertGeminiRequest(*gin.Context, *relaycommon.RelayInfo, *dto.GeminiChatRequest) (any, error) {
	return nil, errNotChat()
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(*gin.Context, *relaycommon.RelayInfo, dto.OpenAIResponsesRequest) (any, error) {
	return nil, errNotChat()
}

func (a *Adaptor) ConvertRerankRequest(*gin.Context, int, dto.RerankRequest) (any, error) {
	return nil, errNotChat()
}

func (a *Adaptor) ConvertEmbeddingRequest(*gin.Context, *relaycommon.RelayInfo, dto.EmbeddingRequest) (any, error) {
	return nil, errNotChat()
}

func (a *Adaptor) ConvertAudioRequest(*gin.Context, *relaycommon.RelayInfo, dto.AudioRequest) (io.Reader, error) {
	return nil, errNotChat()
}

func (a *Adaptor) ConvertImageRequest(*gin.Context, *relaycommon.RelayInfo, dto.ImageRequest) (any, error) {
	return nil, errNotChat()
}
