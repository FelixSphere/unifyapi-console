package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// New registrations land in the `default` group -- nothing in Register sets a
// group, so the users.group column default applies. On 2026-09-15 the
// operator asked why `default` is absent from the Customer model prices
// editor, where every other group's 0.9 overrides are maintained.
//
// The answer is one line in GetGroupModelPricing: the editor lists the keys
// of GroupRatio, and `default` has never been one. The fix is configuration,
// not code -- add `default` to GroupRatio at 1 -- and these tests pin the
// three facts that make that safe:
//
//  1. once GroupRatio holds `default`, the editor lists it;
//  2. until then, saving overrides for `default` is refused, which is why the
//     row was never there to begin with;
//  3. `default` in GroupRatio does NOT reach the anonymous Model Square,
//     because that view is filtered to UserUsableGroups, which does not
//     include `default`. The public price list is unchanged.

// productionGroupRatioWithDefault is the five customer groups production has
// today plus the one line the operator adds. Ratios are all 1: the discount is
// delivered per model through GroupModelDiscount, exactly as for the others.
const productionGroupRatioWithDefault = `{"Builder_hub_2026":1,"Chinhin":1,"GenAI":1,"UnifyAI":1,"Vip User":1,"default":1}`
const productionUserUsableGroups = `{"Builder_hub_2026":"","Chinhin":"","GenAI":"","UnifyAI":"","Vip User":""}`

func withGroupRatio(t *testing.T, jsonStr string) {
	t.Helper()
	previous := ratio_setting.GroupRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(jsonStr))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previous)) })
}

func withUserUsableGroups(t *testing.T, jsonStr string) {
	t.Helper()
	previous := setting.UserUsableGroups2JSONString()
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(jsonStr))
	t.Cleanup(func() { require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previous)) })
}

func groupModelPricingGroups(t *testing.T) []string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	GetGroupModelPricing(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Data struct {
			Groups []string `json:"groups"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	return response.Data.Groups
}

// TestDefaultGroupAppearsInTheEditorOnceItHasAGroupRatio is the operator's
// request, stated as the invariant that satisfies it.
func TestDefaultGroupAppearsInTheEditorOnceItHasAGroupRatio(t *testing.T) {
	ratio_setting.InitRatioSettings()

	withGroupRatio(t, `{"Builder_hub_2026":1,"Chinhin":1,"GenAI":1,"UnifyAI":1,"Vip User":1}`)
	assert.NotContains(t, groupModelPricingGroups(t), "default",
		"precondition: this is production today, and why the editor has no `default` row")

	withGroupRatio(t, productionGroupRatioWithDefault)
	groups := groupModelPricingGroups(t)
	assert.Contains(t, groups, "default")
	assert.ElementsMatch(t, []string{"Builder_hub_2026", "Chinhin", "GenAI", "UnifyAI", "Vip User", "default"}, groups,
		"adding `default` must not drop or rename any existing customer row")
}

// TestOverridesForDefaultAreRefusedUntilItHasAGroupRatio pins the guard that
// explains the gap. Saving a customer price for a group GroupRatio does not
// know is refused with a pointer to Group Pricing -- so the operator cannot
// get into a state where `default` has overrides but no row to show them.
func TestOverridesForDefaultAreRefusedUntilItHasAGroupRatio(t *testing.T) {
	ratio_setting.InitRatioSettings()
	withGroupRatio(t, `{"GenAI":1}`)
	previous := ratio_setting.GroupModelDiscount2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateGroupModelDiscountByJSONString(previous)) })

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	body, err := json.Marshal(map[string]any{"group": "default", "discounts": map[string]float64{"gpt-4o": 0.9}})
	require.NoError(t, err)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/pricing/group_model", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	UpdateGroupModelPricing(ctx)

	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Contains(t, response.Message, `"default"`)
	assert.Contains(t, response.Message, "Group Pricing", "the refusal must say where to fix it")

	_, ok := ratio_setting.GetGroupModelDiscount("default", "gpt-4o")
	assert.False(t, ok, "a refused save must not have written anything")
}

// TestDefaultInGroupRatioDoesNotReachTheAnonymousModelSquare. GetPricing
// deletes every GroupRatio key the viewer cannot use, and drops every model
// row whose only groups the viewer cannot use. An anonymous viewer's usable
// set is UserUsableGroups, which production does not extend to `default`. So
// the public price list -- which the operator has explicitly asked to keep
// as-is -- is byte-for-byte what it was.
//
// This exercises the two filters GetPricing composes, with the same inputs it
// gives them, rather than the handler itself, which needs the database.
func TestDefaultInGroupRatioDoesNotReachTheAnonymousModelSquare(t *testing.T) {
	ratio_setting.InitRatioSettings()
	withUserUsableGroups(t, productionUserUsableGroups)

	anonymousUsable := service.GetUserUsableGroups("")
	require.NotContains(t, anonymousUsable, "default",
		"precondition: production does not list `default` in UserUsableGroups")

	// The group-ratio table the page shows: GetPricing's deletion loop.
	withGroupRatio(t, productionGroupRatioWithDefault)
	shown := map[string]float64{}
	for group, ratio := range ratio_setting.GetGroupRatioCopy() {
		if _, ok := anonymousUsable[group]; ok {
			shown[group] = ratio
		}
	}
	assert.Equal(t, map[string]float64{"Builder_hub_2026": 1, "Chinhin": 1, "GenAI": 1, "UnifyAI": 1, "Vip User": 1}, shown,
		"the anonymous group-ratio table must not gain a `default` column")

	// The model rows: a model reachable only through `default` is not public;
	// a model reachable through a public group still is, unchanged.
	rows := []model.Pricing{
		{ModelName: "only-in-default", EnableGroup: []string{"default"}},
		{ModelName: "gemini-3.8-flash", EnableGroup: []string{"default", "GenAI"}},
		{ModelName: "gpt-4o", EnableGroup: []string{"UnifyAI"}},
	}
	public := filterPricingByUsableGroups(rows, anonymousUsable)
	names := make([]string, 0, len(public))
	for _, row := range public {
		names = append(names, row.ModelName)
	}
	assert.ElementsMatch(t, []string{"gemini-3.8-flash", "gpt-4o"}, names,
		"anonymous viewers see exactly the rows they saw before `default` existed in GroupRatio")

	// And a logged-in `default` user DOES see the column, which is the point.
	defaultUsable := service.GetUserUsableGroups("default")
	assert.Contains(t, defaultUsable, "default",
		"a default user's own group is always usable, so their view gains the row")
}
