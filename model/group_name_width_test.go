/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
 * Group names must fit everywhere a group name travels.
 *
 * Production broke on 2026-09-19: creating a channel answered
 * "value too long for type character varying(64) (SQLSTATE 22001)". Builder had
 * auto-provisioned `Builder_hub_2026_Sep_Batch_UnifyAPI-2` (37 chars), and
 * `channels.group` stores a COMMA-JOINED list -- the eight production groups
 * join to 101 characters against a 64-wide column. With the seven groups that
 * existed the day before, the same list was 63: one character of headroom.
 *
 * SQLite does not enforce varchar widths, so none of this can fail on a dev box
 * or in CI. These tests therefore assert the declared widths and the behaviour
 * that depends on them, rather than trying to provoke a driver error.
 */
package model

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// productionGroups is what app.unifyapi.ai served on 2026-09-19, longest first.
var productionGroups = []string{
	"Builder_hub_2026_Sep_Batch_UnifyAPI-2",
	"Builder_hub_2026",
	"Vip User",
	"Chinhin",
	"Kingdee",
	"UnifyAI",
	"default",
	"GenAI",
}

// requiredSingleGroupWidth is deliberately a literal, not the production
// constant: a test that reads the same constant the code sets would still pass
// if both were lowered together. The number comes from the requirement --
// customerPricingGroupName documents a team name of up to 120 characters, and
// it prefixes the program name and a separator, so a legible bill-to line needs
// roughly twice that.
const requiredSingleGroupWidth = 255

// declaredWidth reads the varchar width off a model's gorm tag.
func declaredWidth(t *testing.T, model any, field string) int {
	t.Helper()
	structField, ok := reflect.TypeOf(model).FieldByName(field)
	require.True(t, ok, "%T has no field %s", model, field)
	tag := structField.Tag.Get("gorm")
	marker := "type:varchar("
	start := strings.Index(tag, marker)
	require.GreaterOrEqual(t, start, 0, "%T.%s is not a varchar: %q", model, field, tag)
	rest := tag[start+len(marker):]
	end := strings.Index(rest, ")")
	require.GreaterOrEqual(t, end, 0)
	width := 0
	for _, digit := range rest[:end] {
		width = width*10 + int(digit-'0')
	}
	return width
}

func TestEverySingleGroupColumnHoldsAProvisionedName(t *testing.T) {
	// One name, not a list. The widest realistic name is program + team, and a
	// team name may be 120 characters (see customerPricingGroupName).
	for _, column := range []struct {
		model any
		field string
	}{
		{Ability{}, "Group"},
		{User{}, "Group"},
		{Tenant{}, "Group"},
		{Task{}, "Group"},
		{TopUp{}, "CustomerGroup"},
		{CustomerWallet{}, "Group"},
	} {
		width := declaredWidth(t, column.model, column.field)
		assert.GreaterOrEqualf(t, width, requiredSingleGroupWidth,
			"%T.%s is %d wide; a provisioned group name needs %d",
			column.model, column.field, width, requiredSingleGroupWidth)
	}
}

func TestTheChannelColumnHoldsEveryProductionGroupAtOnce(t *testing.T) {
	// channels.group is the joined list, and an operator selecting every group
	// is the action that failed. 64 could not hold it; assert the real list
	// fits with room for the groups Builder will provision next.
	joined := strings.Join(productionGroups, ",")
	width := declaredWidth(t, Channel{}, "Group")
	assert.Greaterf(t, width, len(joined),
		"channels.group is %d wide but the live group list is already %d characters",
		width, len(joined))
	assert.GreaterOrEqual(t, width, 1024, "leave room for groups not provisioned yet")
}

func TestNoProductionGroupIsSkippedFromRouting(t *testing.T) {
	// routingPricingGroups drops a group longer than maxRoutingGroupLength with
	// only a log line, which would leave that group with no channels at all.
	for _, group := range productionGroups {
		assert.LessOrEqualf(t, len([]rune(group)), maxRoutingGroupLength,
			"%q (%d chars) would be silently skipped from routing", group, len([]rune(group)))
	}
}

func TestAProvisionedNameKeepsTheBillToLineLegible(t *testing.T) {
	// customerPricingGroupName degrades to an opaque code when the name cannot
	// fit the column. The group is printed as the bill-to line, so at 64 a
	// realistic program + team fell all the way through to the code.
	program := &PartnershipProgram{Name: "Builder_hub_2026_Sep_Batch", Code: "BH26"}
	team := "UnifyAPI-2"

	name := customerPricingGroupName(program, team, "BH26X1")
	assert.Equal(t, "Builder_hub_2026_Sep_Batch_UnifyAPI-2", name)

	// A 120-character team name is the documented worst case; it must still
	// carry the program, not collapse to the bare code.
	long := strings.Repeat("A", 120)
	assert.Equal(t, program.Name+groupNameSeparator+long,
		customerPricingGroupName(program, long, "BH26X2"),
		"a long team name must still bill under its program")
}
