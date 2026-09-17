/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package controller

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type programReply struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func postProgram(t *testing.T, body string) programReply {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/partnership/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	CreatePartnershipProgram(c)
	var reply programReply
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &reply))
	return reply
}

func putProgram(t *testing.T, id int, body string) programReply {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/partnership/"+strconv.Itoa(id), strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(id)}}
	UpdatePartnershipProgram(c)
	var reply programReply
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &reply))
	return reply
}

// The model accepted a program with no group, but the HTTP layer refused it
// before the model ever saw it -- so the feature was dead on arrival while
// every model test passed. This is that missing layer.
func TestTheApiAcceptsAProgramWithNoDefaultGroup(t *testing.T) {
	setupPartnershipControllerTest(t)
	reply := postProgram(t, `{
		"name":"Teams only","code":"teams-only","group":"",
		"grant_quota":5000000,"grant_limit":50,"enabled":true
	}`)
	assert.True(t, reply.Success, "an empty group must reach the model, not be refused here: %s", reply.Message)
	assert.NotContains(t, reply.Message, "Group Pricing")
}

// And the path the operator actually takes: clearing the group on a program
// that already has one.
func TestTheApiAcceptsClearingAProgramsGroup(t *testing.T) {
	setupPartnershipControllerTest(t)
	created := postProgram(t, `{
		"name":"Builder hub","code":"builder-hub","group":"partner",
		"grant_quota":5000000,"grant_limit":50,"enabled":true
	}`)
	require.True(t, created.Success, created.Message)

	var program model.PartnershipProgram
	require.NoError(t, model.DB.Where("code = ?", "builder-hub").First(&program).Error)

	reply := putProgram(t, program.Id, `{
		"name":"Builder hub","code":"builder-hub","group":"",
		"grant_quota":5000000,"grant_limit":50,"enabled":true
	}`)
	assert.True(t, reply.Success, "clearing the group must be accepted: %s", reply.Message)

	var after model.PartnershipProgram
	require.NoError(t, model.DB.First(&after, program.Id).Error)
	assert.Empty(t, after.Group, "and it must actually be cleared, not silently kept")

	var defaults int64
	require.NoError(t, model.DB.Model(&model.PartnershipCustomer{}).
		Where("program_id = ? AND is_default = ? AND removed_at = ?", program.Id, true, 0).
		Count(&defaults).Error)
	assert.Zero(t, defaults, "the default customer must be retired")
}

// A named group that does not exist is still refused.
//
// Note what this does NOT prove: deleting the controller's check leaves this
// passing, because the model refuses the same thing and its error also says
// "Group Pricing". The controller check is defence in depth, not the only
// guard. What is asserted here is the behaviour -- a missing group is
// refused -- which is the property worth holding either way.
func TestTheApiStillRefusesAGroupThatDoesNotExist(t *testing.T) {
	setupPartnershipControllerTest(t)
	reply := postProgram(t, `{
		"name":"Bad group","code":"bad-group","group":"no-such-group",
		"grant_quota":5000000,"grant_limit":50,"enabled":true
	}`)
	assert.False(t, reply.Success)
	assert.Contains(t, reply.Message, "Group Pricing")
}

// A customer is not a program: it is somebody's billing identity and must
// always have a group. That check is untouched.
func TestACustomerStillRequiresAGroup(t *testing.T) {
	setupPartnershipControllerTest(t)
	created := postProgram(t, `{
		"name":"Builder hub","code":"builder-hub","group":"partner",
		"grant_quota":5000000,"grant_limit":50,"enabled":true
	}`)
	require.True(t, created.Success, created.Message)
	var program model.PartnershipProgram
	require.NoError(t, model.DB.Where("code = ?", "builder-hub").First(&program).Error)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost,
		"/api/partnership-programs/"+strconv.Itoa(program.Id)+"/customers",
		strings.NewReader(`{"name":"Nusa Labs","code":"nusa-labs","group":"","enabled":true}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(program.Id)}}
	CreatePartnershipCustomer(c)

	var reply programReply
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &reply))
	assert.False(t, reply.Success, "a customer with no group must still be refused")
}
