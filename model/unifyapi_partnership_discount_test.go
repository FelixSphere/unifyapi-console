/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

// A program discount is materialised into every customer's Customer model
// prices. The hazard that shapes every test here: re-applying it must never
// flatten a price somebody negotiated by hand.

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupProgramDiscountTest(t *testing.T) int {
	t.Helper()
	setupGroupRatioProvisionTest(t)
	ratio_setting.InitRatioSettings()

	program := PartnershipProgram{Name: "Nusa", Code: "nusa", Enabled: true}
	require.NoError(t, DB.Create(&program).Error)
	for _, c := range []PartnershipCustomer{
		{ProgramId: program.Id, Name: "Alpha", Code: "alpha", Group: "Alpha Co", Enabled: true},
		{ProgramId: program.Id, Name: "Beta", Code: "beta", Group: "Beta Co", Enabled: true},
		{ProgramId: program.Id, Name: "Gone", Code: "gone", Group: "Gone Co", Enabled: true},
	} {
		customer := c
		require.NoError(t, DB.Create(&customer).Error)
	}
	// Disabled with an explicit UPDATE, not by creating it false. Enabled is
	// tagged `default:true`, so GORM omits a zero-value bool from the INSERT
	// and the row comes back ENABLED -- a fixture that quietly tests the
	// opposite of what it says. Caught by this test failing 3/2.
	require.NoError(t, DB.Model(&PartnershipCustomer{}).
		Where("code = ?", "gone").Update("enabled", false).Error)
	return program.Id
}

func customerModelPrices(t *testing.T) map[string]map[string]float64 {
	t.Helper()
	var option Option
	if err := DB.Where("key = ?", "GroupModelDiscount").First(&option).Error; err != nil {
		return map[string]map[string]float64{}
	}
	out := map[string]map[string]float64{}
	require.NoError(t, common.Unmarshal([]byte(option.Value), &out))
	return out
}

func TestAProgramDiscountReachesEveryLiveCustomerAndEveryModel(t *testing.T) {
	programId := setupProgramDiscountTest(t)

	result, err := ApplyProgramDiscount(programId, 0, 0.85)
	require.NoError(t, err)

	prices := customerModelPrices(t)
	models := ratio_setting.CatalogModels()
	require.NotEmpty(t, models)

	for _, group := range []string{"Alpha Co", "Beta Co"} {
		require.Contains(t, prices, group)
		assert.Len(t, prices[group], len(models),
			"%s must be priced for every catalogued model", group)
		for _, name := range models {
			assert.InDelta(t, 0.85, prices[group][name], 1e-9, "%s / %s", group, name)
		}
	}
	assert.NotContains(t, prices, "Gone Co",
		"a disabled customer must not be repriced -- nobody will bill under it")
	assert.Equal(t, 2, result.CustomersChanged)
	assert.Equal(t, 2*len(models), result.ModelsWritten)
	assert.Empty(t, result.Preserved)
}

// The one that matters. A price set by hand after the program discount was
// applied is a negotiated number, and re-applying must leave it alone.
func TestReapplyingAProgramDiscountPreservesAHandSetPrice(t *testing.T) {
	programId := setupProgramDiscountTest(t)
	models := ratio_setting.CatalogModels()
	negotiated := models[0]

	require.NoError(t, func() error { _, err := ApplyProgramDiscount(programId, 0, 0.9); return err }())

	// The operator gives Alpha a special price on one model.
	prices := customerModelPrices(t)
	prices["Alpha Co"][negotiated] = 0.55
	encoded, err := common.Marshal(prices)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", "GroupModelDiscount").
		Update("value", string(encoded)).Error)

	// The program moves from 0.9 to 0.8.
	result, err := ApplyProgramDiscount(programId, 0.9, 0.8)
	require.NoError(t, err)

	after := customerModelPrices(t)
	assert.InDelta(t, 0.55, after["Alpha Co"][negotiated], 1e-9,
		"the negotiated price must survive a program-wide change")
	assert.Contains(t, result.Preserved, "Alpha Co / "+negotiated,
		"and the operator must be told it was not touched")
	for _, name := range models[1:] {
		assert.InDelta(t, 0.8, after["Alpha Co"][name], 1e-9, "every other model moves")
	}
	for _, name := range models {
		assert.InDelta(t, 0.8, after["Beta Co"][name], 1e-9, "and so does every other customer")
	}
}

// Clearing the discount removes what the program owns and nothing else.
func TestClearingAProgramDiscountRemovesOnlyItsOwnRows(t *testing.T) {
	programId := setupProgramDiscountTest(t)
	models := ratio_setting.CatalogModels()
	negotiated := models[0]

	require.NoError(t, func() error { _, err := ApplyProgramDiscount(programId, 0, 0.9); return err }())
	prices := customerModelPrices(t)
	prices["Alpha Co"][negotiated] = 0.55
	encoded, err := common.Marshal(prices)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", "GroupModelDiscount").
		Update("value", string(encoded)).Error)

	_, err = ApplyProgramDiscount(programId, 0.9, 0)
	require.NoError(t, err)

	after := customerModelPrices(t)
	assert.InDelta(t, 0.55, after["Alpha Co"][negotiated], 1e-9, "the negotiated price stays")
	assert.Len(t, after["Alpha Co"], 1, "everything the program wrote is gone")
	assert.NotContains(t, after, "Beta Co", "a customer with nothing left drops out entirely")
}

// Another program's customers, and groups that belong to nobody, must not move.
func TestAProgramDiscountTouchesNoOtherGroup(t *testing.T) {
	programId := setupProgramDiscountTest(t)
	other := PartnershipProgram{Name: "Other", Code: "other", Enabled: true}
	require.NoError(t, DB.Create(&other).Error)
	require.NoError(t, DB.Create(&PartnershipCustomer{
		ProgramId: other.Id, Name: "Zeta", Code: "zeta", Group: "Zeta Co", Enabled: true,
	}).Error)
	require.NoError(t, DB.Create(&Option{
		Key: "GroupModelDiscount", Value: `{"Zeta Co":{"gpt-4o":0.7},"Unrelated":{"gpt-4o":0.6}}`,
	}).Error)

	_, err := ApplyProgramDiscount(programId, 0, 0.85)
	require.NoError(t, err)

	after := customerModelPrices(t)
	assert.InDelta(t, 0.7, after["Zeta Co"]["gpt-4o"], 1e-9, "another program's customer is untouched")
	assert.InDelta(t, 0.6, after["Unrelated"]["gpt-4o"], 1e-9, "and so is a group with no program")
}

func TestAProgramDiscountOutsideZeroToOneIsRefused(t *testing.T) {
	programId := setupProgramDiscountTest(t)
	for _, bad := range []float64{-0.1, 1.5} {
		_, err := ApplyProgramDiscount(programId, 0, bad)
		assert.Error(t, err, "%g is not a discount", bad)
	}
	assert.Error(t, ValidatePartnershipProgram(&PartnershipProgram{
		Name: "x", Code: "xxx", Discount: 1.5,
	}))
	assert.NoError(t, ValidatePartnershipProgram(&PartnershipProgram{
		Name: "x", Code: "xxx", Discount: 0.9,
	}))
}

// The catalogue is compiled in, so "a new model shipped" looks like a row that
// is simply absent. The operator's question was "then I have to edit the new
// model at every customer by hand?" -- the answer has to be no, and this is
// what makes it no.
func TestANewCatalogueModelIsFilledInWithoutTouchingHandSetPrices(t *testing.T) {
	programId := setupProgramDiscountTest(t)
	models := ratio_setting.CatalogModels()
	newcomer, negotiated := models[1], models[0]

	// The boot pass reads the discount off the PROGRAM row, so setting it only
	// in the option would test a state that cannot occur.
	require.NoError(t, DB.Model(&PartnershipProgram{}).Where("id = ?", programId).
		Update("discount", 0.85).Error)
	_, err := ApplyProgramDiscount(programId, 0, 0.85)
	require.NoError(t, err)

	// One model has a negotiated price; another is missing entirely, which is
	// the state every customer is in the moment a model joins the catalogue.
	prices := customerModelPrices(t)
	prices["Alpha Co"][negotiated] = 0.5
	delete(prices["Alpha Co"], newcomer)
	delete(prices["Beta Co"], newcomer)
	encoded, err := common.Marshal(prices)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", "GroupModelDiscount").
		Update("value", string(encoded)).Error)

	require.NoError(t, ReapplyProgramDiscounts())

	after := customerModelPrices(t)
	assert.InDelta(t, 0.85, after["Alpha Co"][newcomer], 1e-9,
		"the new model must be priced without anyone editing a customer")
	assert.InDelta(t, 0.85, after["Beta Co"][newcomer], 1e-9)
	assert.InDelta(t, 0.5, after["Alpha Co"][negotiated], 1e-9,
		"and the negotiated price must survive the fill")
}

// Running it twice must do nothing the second time, because it runs on every
// boot.
func TestReapplyingOnBootIsIdempotent(t *testing.T) {
	programId := setupProgramDiscountTest(t)
	_, err := ApplyProgramDiscount(programId, 0, 0.85)
	require.NoError(t, err)

	require.NoError(t, ReapplyProgramDiscounts())
	first := customerModelPrices(t)
	require.NoError(t, ReapplyProgramDiscounts())
	assert.Equal(t, first, customerModelPrices(t))
}

// A customer added after the discount was set is the same gap as a new model.
func TestACustomerAddedLaterIsPricedByTheProgram(t *testing.T) {
	programId := setupProgramDiscountTest(t)
	require.NoError(t, DB.Model(&PartnershipProgram{}).Where("id = ?", programId).
		Update("discount", 0.8).Error)
	_, err := ApplyProgramDiscount(programId, 0, 0.8)
	require.NoError(t, err)

	// A customer's group has to exist in Group Pricing before the customer can
	// be created -- the same rule the admin path enforces.
	require.NoError(t, EnsurePartnershipGroupRatio("Delta Co"))

	newcomer := PartnershipCustomer{Name: "Delta", Code: "delta", Group: "Delta Co", Enabled: true}
	require.NoError(t, CreatePartnershipCustomer(programId, &newcomer))

	after := customerModelPrices(t)
	require.Contains(t, after, "Delta Co", "a customer joining later must be priced too")
	for _, name := range ratio_setting.CatalogModels() {
		assert.InDelta(t, 0.8, after["Delta Co"][name], 1e-9)
	}
}

// Operator, 2026-09-22: a customer that belongs to no program has no program
// discount and falls through to the ordinary group ratio. Nothing here may
// write a row for them, because a row would REPLACE that group ratio and
// silently take over their pricing.
func TestACustomerInNoProgramIsLeftToTheGroupRatio(t *testing.T) {
	programId := setupProgramDiscountTest(t)
	require.NoError(t, DB.Create(&Option{
		Key: "GroupModelDiscount", Value: `{}`,
	}).Error)

	_, err := ApplyProgramDiscount(programId, 0, 0.85)
	require.NoError(t, err)
	require.NoError(t, ReapplyProgramDiscounts())

	after := customerModelPrices(t)
	for _, group := range []string{"default", "vip", "partner"} {
		assert.NotContains(t, after, group,
			"%s belongs to no program, so its price stays the group ratio's business", group)
	}
}

// OPERATOR RULE, 2026-09-22, stated as absolute: the discount the bill uses and
// the discount the UI shows must always be the same number. Nobody may change
// that.
//
// It is why a program discount is MATERIALISED into Customer model prices
// instead of being consulted at billing time. A resolution-time fallback would
// bill 0.85 while that editor showed nothing -- the same class of gap as
// publishing a discount nobody set, pointing the other way.
//
// This test reads the value from where the UI reads it (the GroupModelDiscount
// option, which is what the Customer model prices editor renders) and from
// where the relay reads it (ratio_setting.GetGroupModelDiscount, which
// HandleGroupRatio calls), and requires them to agree for every customer and
// every model.
func TestTheBilledDiscountIsTheDiscountTheEditorShows(t *testing.T) {
	programId := setupProgramDiscountTest(t)
	require.NoError(t, DB.Model(&PartnershipProgram{}).Where("id = ?", programId).
		Update("discount", 0.85).Error)
	_, err := ApplyProgramDiscount(programId, 0, 0.85)
	require.NoError(t, err)

	// One negotiated price, so the test covers a row the program does not own.
	models := ratio_setting.CatalogModels()
	shown := customerModelPrices(t)
	shown["Alpha Co"][models[0]] = 0.6
	encoded, err := common.Marshal(shown)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", "GroupModelDiscount").
		Update("value", string(encoded)).Error)
	require.NoError(t, updateOptionMap("GroupModelDiscount", string(encoded)))

	shown = customerModelPrices(t)
	require.NotEmpty(t, shown)

	for group, perModel := range shown {
		for modelName, uiValue := range perModel {
			billed, ok := ratio_setting.GetGroupModelDiscount(group, modelName)
			require.True(t, ok,
				"%s / %s is shown in the editor but the relay finds no discount for it", group, modelName)
			assert.InDelta(t, uiValue, billed, 1e-9,
				"%s / %s: the editor shows %g and the bill uses %g", group, modelName, uiValue, billed)
		}
	}
}
