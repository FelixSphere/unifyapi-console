package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// channelEditorModels calls GET /api/channel/models the way the channel
// editor does and returns the ids in the order the dropdown shows them.
func channelEditorModels(t *testing.T) []dto.OpenAIModels {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/channel/models", nil)
	ChannelListModels(c)
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Success bool               `json:"success"`
		Data    []dto.OpenAIModels `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.True(t, body.Success)
	return body.Data
}

func offered(models []dto.OpenAIModels, id string) bool {
	for _, m := range models {
		if m.Id == id {
			return true
		}
	}
	return false
}

// The defect this replaces: a model listed by configuration, with no release,
// could not be picked in the channel editor because the dropdown was a list
// compiled into the binary. Each of the three ways a model becomes known at
// run time must reach the dropdown with no code change.
func TestChannelEditorOffersAModelAsSoonAsItIsListed(t *testing.T) {
	db := setupModelListControllerTestDB(t)

	const priced, exprOnly, typedOnce = "zz-priced-in-the-console", "zz-priced-by-expression", "zz-typed-on-a-channel"
	for _, id := range []string{priced, exprOnly, typedOnce} {
		require.False(t, offered(openAIModels, id), "%s must not be in a compiled list, or this test proves nothing", id)
	}

	before := channelEditorModels(t)
	for _, id := range []string{priced, exprOnly, typedOnce} {
		require.False(t, offered(before, id), "%s offered before it was listed", id)
	}

	// 1. A price typed into the console (the ExtraModelPricing option).
	previous := ratio_setting.ExtraModels2JSONString()
	require.NoError(t, ratio_setting.UpdateExtraModelsByJSONString(
		`{"`+priced+`":{"input_usd":3,"output_usd":15}}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateExtraModelsByJSONString(previous)) })

	// 2. A model priced only by a billing expression, as a tiered model is.
	// (There is no call that removes a hand-written expression, so this one
	// stays for the rest of the package's run, as in the other tests that use
	// the helper. Its id is one no other test asks for.)
	billing_setting.SetBillingExprForTest(exprOnly, `tier("standard", p*2 + c*10)`)

	// 3. An id somebody typed onto a channel, here one that is switched off.
	require.NoError(t, db.Create(&model.Ability{
		Group: "default", Model: typedOnce, ChannelId: 7, Enabled: false,
	}).Error)

	after := channelEditorModels(t)
	for _, id := range []string{priced, exprOnly, typedOnce} {
		require.True(t, offered(after, id), "%s is listed but the channel editor does not offer it", id)
	}
}

// Every model the catalogue prices is offered, ahead of the compiled tail, and
// nothing the editor offered before is lost or repeated.
func TestChannelEditorOffersTheWholeCatalogueFirstAndLosesNothing(t *testing.T) {
	setupModelListControllerTestDB(t)

	got := channelEditorModels(t)
	position := make(map[string]int, len(got))
	for i, m := range got {
		_, dup := position[m.Id]
		require.False(t, dup, "%s is offered twice", m.Id)
		position[m.Id] = i
	}

	catalog := ratio_setting.Catalog()
	require.NotEmpty(t, catalog)
	for i, entry := range catalog {
		at, ok := position[entry.Model]
		require.True(t, ok, "catalogue model %s is not offered", entry.Model)
		require.Equal(t, i, at, "catalogue model %s is not in catalogue order at the head", entry.Model)
	}

	for _, static := range openAIModels {
		at, ok := position[static.Id]
		require.True(t, ok, "%s was offered before this change and is gone", static.Id)
		require.Equal(t, static.OwnedBy, got[at].OwnedBy, "%s changed owner", static.Id)
	}

	// The ids #309 added by hand stay offered through the compiled tail.
	for _, id := range []string{"claude-fable-5-2", "claude-mythos-5-1", "claude-mythos-5-2"} {
		require.True(t, offered(got, id), "%s is not offered", id)
	}
}
