/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/sjson"
)

// SystemOneHelper relays a TypeSafe System One evaluation.
//
// Unlike every other handler here it converts nothing. A System One request is
// a state plus typed questions, and the answer is a typed value with
// calibrated probabilities; both are forwarded as the client and the vendor
// wrote them, because that structure is the product. The only thing this
// rewrites is the model name, and only when the channel maps it -- which is
// also the only case that pays for a decode and re-encode of the body.
func SystemOneHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	info.InitChannelMeta(c)

	request, ok := info.Request.(*dto.SystemOneRequest)
	if !ok {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("invalid request type, expected dto.SystemOneRequest, got %T", info.Request),
			types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	requestedModel := request.Model

	if err := helper.ModelMappedHelper(c, info, request); err != nil {
		return types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}

	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}

	requestBody := common.ReaderOnly(storage)
	if request.Model != requestedModel {
		// The channel renamed the model, so the body has to say the new name.
		raw, err := io.ReadAll(common.ReaderOnly(storage))
		if err != nil {
			return types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		rewritten, err := rewriteSystemOneModel(raw, request.Model)
		if err != nil {
			return types.NewError(fmt.Errorf("could not apply the channel's model mapping: %w", err), types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		body, size, closer, err := relaycommon.NewOutboundJSONBody(rewritten)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		defer closer.Close()
		info.UpstreamRequestBodySize = size
		requestBody = body
	}

	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)

	// Remembered so a failure can say where the request actually ended up.
	// GetRequestURL is pure, so asking twice costs nothing.
	requestedURL, _ := adaptor.GetRequestURL(info)

	resp, err := adaptor.DoRequest(c, info, requestBody)
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")
	var httpResp *http.Response
	if resp != nil {
		httpResp = resp.(*http.Response)
		if httpResp.StatusCode != http.StatusOK {
			newAPIError = service.RelayErrorHandler(c.Request.Context(), httpResp, false)
			if note := describeUpstreamRedirect(requestedURL, httpResp); note != "" && newAPIError != nil && newAPIError.Err != nil {
				newAPIError.Err = fmt.Errorf("%w [%s]", newAPIError.Err, note)
			}
			service.ResetStatusCode(newAPIError, statusCodeMappingStr)
			return newAPIError
		}
	}

	usage, newAPIError := adaptor.DoResponse(c, httpResp, info)
	if newAPIError != nil {
		service.ResetStatusCode(newAPIError, statusCodeMappingStr)
		return newAPIError
	}
	service.PostTextConsumeQuota(c, info, usage.(*dto.Usage), nil)
	return nil
}

// describeUpstreamRedirect reports, in words, that the answer came from a
// different URL than the channel was configured to call.
//
// An aggregator that does not serve System One may redirect the whole path
// space at a console host, and Go's client follows that without a word. Two
// things then go wrong quietly: the redirect crosses hosts, so the client
// drops the channel's Authorization header, and the error that comes back
// names only a path -- an operator reading "Invalid URL (POST /v1/systemone)"
// cannot tell that their request was answered by a host they never configured.
// Saying so is the difference between a five-minute fix and an afternoon.
//
// It returns an empty string when nothing was redirected, which is the norm.
func describeUpstreamRedirect(requestedURL string, resp *http.Response) string {
	if requestedURL == "" || resp == nil || resp.Request == nil || resp.Request.URL == nil {
		return ""
	}
	finalURL := resp.Request.URL.String()
	if finalURL == requestedURL {
		return ""
	}
	note := fmt.Sprintf("the upstream redirected %s to %s", requestedURL, finalURL)
	if requested, err := url.Parse(requestedURL); err == nil &&
		!sameSiteForCredentials(requested.Hostname(), resp.Request.URL.Hostname()) {
		note += ", and a redirect off the original domain drops the channel's API key"
	}
	return note + "; point the channel's base URL and System One path at the endpoint that answers directly"
}

// sameSiteForCredentials mirrors net/http's own rule for carrying an
// Authorization header through a redirect: it survives to the same host or a
// subdomain of it, and is dropped anywhere else. The port is not part of the
// test, so saying "another host" would be wrong for a redirect that only
// changes the port -- and telling an operator their key was dropped when it
// was not sends them hunting for the wrong problem.
func sameSiteForCredentials(from, to string) bool {
	from, to = strings.ToLower(from), strings.ToLower(to)
	return to == from || strings.HasSuffix(to, "."+from)
}

// rewriteSystemOneModel replaces the model name and nothing else.
//
// It edits the bytes in place rather than decoding and re-encoding, which is
// how this repo already rewrites request bodies (see ApplyParamOverride). That
// matters more here than anywhere else: a decision model's input IS the
// customer's business state, so the numbers in it are the data, not
// formatting. Decoding into `any` the ordinary way turns every JSON number
// into a float64 and prints it back differently -- an id of 9007199254740993
// comes out 9007199254740992, and 12345678901234567890 loses its last three
// digits. The customer would be billed for a decision made about numbers they
// never sent, and nothing anywhere would say so.
//
// Editing in place also keeps field order and every key we have never heard
// of, including ones the vendor adds after this code is written, so the body
// the upstream reads is the one the client wrote apart from the model name.
func rewriteSystemOneModel(raw []byte, model string) ([]byte, error) {
	if !json.Valid(raw) {
		return nil, errors.New("request body is not valid JSON")
	}
	return sjson.SetBytes(raw, "model", model)
}
