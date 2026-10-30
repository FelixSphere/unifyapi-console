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
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func deleteProgram(t *testing.T, id string) (*httptest.ResponseRecorder, struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		EnrolledMembers int64 `json:"enrolled_members"`
	} `json:"data"`
}) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/partnership/"+id, nil)
	c.Params = gin.Params{{Key: "id", Value: id}}
	DeletePartnershipProgram(c)
	var body struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			EnrolledMembers int64 `json:"enrolled_members"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &body))
	return recorder, body
}

// The operator's actual request: a program they are finished with has to be
// removable through the console, and must then be gone from the list they
// read.
func TestRootCanRemoveAProgramThroughTheApi(t *testing.T) {
	setupPartnershipControllerTest(t)
	program := &model.PartnershipProgram{
		Name: "Mistyped program", Code: "mistyped", Group: "partner",
		GrantQuota: 5000000, GrantLimit: 50, Enabled: true,
	}
	require.NoError(t, model.CreatePartnershipProgram(program))

	_, body := deleteProgram(t, strconv.Itoa(program.Id))
	require.True(t, body.Success, body.Message)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/partnership/", nil)
	GetPartnershipPrograms(c)
	var listed struct {
		Data struct {
			Programs []model.PartnershipProgram `json:"programs"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &listed))
	assert.Empty(t, listed.Data.Programs, "the removed program must be gone from the console list")
}

// Removing a program reassigns its members the next time they connect. The
// response has to say how many, so the operator is not shown a bare success
// for something that moved people between pricing groups.
func TestRemovalReportsHowManyMembersItAffected(t *testing.T) {
	setupPartnershipControllerTest(t)
	program := &model.PartnershipProgram{
		Name: "Mistyped program", Code: "mistyped", Group: "partner",
		GrantQuota: 5000000, GrantLimit: 50, Enabled: true,
	}
	require.NoError(t, model.CreatePartnershipProgram(program))
	for _, userId := range []int{11, 12, 13} {
		require.NoError(t, model.DB.Create(&model.PartnershipEnrollment{
			ProgramId: program.Id, UserId: userId, CustomerGroup: "partner",
		}).Error)
	}

	_, body := deleteProgram(t, strconv.Itoa(program.Id))
	require.True(t, body.Success, body.Message)
	assert.Equal(t, int64(3), body.Data.EnrolledMembers)
}

func TestRemovingAProgramThatIsNotThereIsNotFound(t *testing.T) {
	setupPartnershipControllerTest(t)
	recorder, body := deleteProgram(t, "4242")
	assert.Equal(t, http.StatusNotFound, recorder.Code)
	assert.False(t, body.Success)
}

func TestAMalformedProgramIdIsRefusedBeforeTouchingTheDatabase(t *testing.T) {
	setupPartnershipControllerTest(t)
	for _, id := range []string{"abc", "0", "-3"} {
		_, body := deleteProgram(t, id)
		assert.False(t, body.Success, "id %q must be refused", id)
	}
}
