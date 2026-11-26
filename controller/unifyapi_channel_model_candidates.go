package controller

import (
	"sort"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// channelModelCandidates is what the channel editor offers in its Models
// dropdown (GET /api/channel/models).
//
// Upstream answers that route with a list frozen into the binary: every
// adaptor's hard-coded ModelList. It cannot know a model that was listed after
// the build, so each new model had to be typed by hand until somebody added
// its id to an adaptor and cut a release (operator, 2026-11-21, on the Claude
// 5.x ids added by #309: "这个设计很愚蠢").
//
// The models an operator actually puts on a channel are the ones this
// deployment can sell, and that set is already known at run time:
//
//  1. the pricing catalogue, including the admin-entered extras
//     (ExtraModelPricing) -- a model with no price is refused by the relay;
//  2. models priced only by a billing expression (billing_setting), which is
//     how a tiered model is listed without a release;
//  3. every model already on a channel, whatever the channel's state, so an
//     id somebody typed once is offered the next time.
//
// Those come first, in that order. The adaptors' static lists follow as a
// tail, so nothing the editor offered before is lost; they are no longer what
// decides whether a model can be picked.
//
// It is a list of suggestions and nothing else: it prices nothing, routes
// nothing and is not what /v1/models returns.
func channelModelCandidates() []dto.OpenAIModels {
	// The entry upstream returned for an id, so a model that is both priced and
	// in a compiled list keeps the metadata the editor has always been given.
	// Not openAIModelsMap: that map is built before the list is de-duplicated
	// and disagrees with it on the owner of an id two adaptors share.
	compiled := make(map[string]dto.OpenAIModels, len(openAIModels))
	for _, m := range openAIModels {
		compiled[m.Id] = m
	}
	seen := make(map[string]struct{}, len(openAIModels)+128)
	out := make([]dto.OpenAIModels, 0, len(openAIModels)+128)
	add := func(id, owner string) {
		if id == "" {
			return
		}
		if _, dup := seen[id]; dup {
			return
		}
		seen[id] = struct{}{}
		if static, ok := compiled[id]; ok {
			out = append(out, static)
			return
		}
		if owner == "" {
			owner = "custom"
		}
		out = append(out, dto.OpenAIModels{
			Id:      id,
			Object:  "model",
			Created: 1626777600,
			OwnedBy: owner,
		})
	}

	for _, entry := range ratio_setting.Catalog() {
		add(entry.Model, entry.Vendor)
	}
	for _, id := range sortedKeys(billing_setting.GetBillingExprCopy()) {
		add(id, "")
	}
	onChannels := model.ChannelModelNames()
	sort.Strings(onChannels)
	for _, id := range onChannels {
		add(id, "")
	}
	for _, static := range openAIModels {
		add(static.Id, static.OwnedBy)
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
