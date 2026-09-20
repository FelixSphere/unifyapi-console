/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package controller

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// When an operator adds credit to a user (Users -> ... -> quota, "add" or an
// "override" that raises the balance), the user is told by email. These tests
// observe the send at the mail boundary: the delivery recorder sees every
// attempt with its purpose and recipient, whether or not an SMTP server is
// configured (in tests it is not, so the attempt itself is the evidence).

type recordedSend struct {
	receiver, purpose string
}

func captureEmailAttempts(t *testing.T) (*[]recordedSend, *sync.Mutex) {
	t.Helper()
	var mu sync.Mutex
	var sends []recordedSend
	common.SetEmailDeliveryRecorder(func(receiver, purpose string, _ error) {
		mu.Lock()
		defer mu.Unlock()
		sends = append(sends, recordedSend{receiver, purpose})
	})
	t.Cleanup(func() { common.SetEmailDeliveryRecorder(nil) })
	return &sends, &mu
}

func awaitOperatorCreditNotice(t *testing.T) chan int {
	t.Helper()
	done := make(chan int, 4)
	operatorCreditNotified = done
	t.Cleanup(func() { operatorCreditNotified = nil })
	return done
}

func setupOperatorCreditTest(t *testing.T) {
	t.Helper()
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.TopUp{}, &model.Tenant{}))
	previousServer, previousAccount := common.SMTPServer, common.SMTPAccount
	common.SMTPServer, common.SMTPAccount = "", ""
	// The hourly notification limit is read from the environment at boot and is
	// 0 in a bare test process, which would silently drop every notice.
	previousLimit := constant.NotifyLimitCount
	constant.NotifyLimitCount = 10
	t.Cleanup(func() {
		common.SMTPServer, common.SMTPAccount = previousServer, previousAccount
		constant.NotifyLimitCount = previousLimit
	})
}

func waitFor(t *testing.T, done <-chan int, userId int) {
	t.Helper()
	select {
	case got := <-done:
		require.Equal(t, userId, got)
	case <-time.After(10 * time.Second):
		t.Fatal("the credit notice was never attempted")
	}
}

func TestAddingCreditEmailsTheUser(t *testing.T) {
	setupOperatorCreditTest(t)
	sends, mu := captureEmailAttempts(t)
	done := awaitOperatorCreditNotice(t)
	user := model.User{Username: "credited", Password: "password", Email: "credited@example.com",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, model.DB.Create(&user).Error)

	recorder := performManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":"add","value":2500000}`, user.Id))
	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	waitFor(t, done, user.Id)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, *sends, 1, "exactly one notice for one credit")
	assert.Equal(t, "credited@example.com", (*sends)[0].receiver)
	assert.Equal(t, "notification", (*sends)[0].purpose)
	balance, err := model.GetUserQuota(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, 2500000, balance, "the credit itself is applied regardless of mail")
}

func TestAnOverrideThatRaisesTheBalanceEmailsTheUserButALoweringOneDoesNot(t *testing.T) {
	setupOperatorCreditTest(t)
	sends, mu := captureEmailAttempts(t)
	done := awaitOperatorCreditNotice(t)
	user := model.User{Username: "overridden", Password: "password", Email: "overridden@example.com",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", Quota: 1000}
	require.NoError(t, model.DB.Create(&user).Error)

	recorder := performManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":"override","value":6000}`, user.Id))
	require.Equal(t, http.StatusOK, recorder.Code)
	waitFor(t, done, user.Id)

	recorder = performManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":"override","value":500}`, user.Id))
	require.Equal(t, http.StatusOK, recorder.Code)
	recorder = performManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":"subtract","value":100}`, user.Id))
	require.Equal(t, http.StatusOK, recorder.Code)

	mu.Lock()
	defer mu.Unlock()
	assert.Len(t, *sends, 1, "raising the balance notifies; lowering it or subtracting does not")
	assert.Equal(t, "overridden@example.com", (*sends)[0].receiver)
}

func TestAUserWithoutAnEmailIsCreditedSilently(t *testing.T) {
	setupOperatorCreditTest(t)
	sends, mu := captureEmailAttempts(t)
	done := awaitOperatorCreditNotice(t)
	user := model.User{Username: "no-email", Password: "password",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, model.DB.Create(&user).Error)

	recorder := performManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":"add","value":500000}`, user.Id))
	require.Equal(t, http.StatusOK, recorder.Code)
	waitFor(t, done, user.Id)

	mu.Lock()
	defer mu.Unlock()
	assert.Empty(t, *sends, "nowhere to send; the credit still lands")
	balance, err := model.GetUserQuota(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, 500000, balance)
}
