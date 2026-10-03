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
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Contract revision 2026-10-02: sku is required on settle. Missing is
// 400 invalid_request, not sku_mismatch and not the hold's SKU filled in.
func TestComputeBridgeSettleWithoutSkuIsInvalidAndRecordsNothing(t *testing.T) {
	db := setupComputeBridgeTestDB(t)
	engine := newComputeBridgeTestRouter()
	user, tenant, token := createComputeBridgeCustomer(t, db, "skuless", 5_000_000, 0)
	require.NoError(t, db.Model(token).Update("unlimited_quota", true).Error)
	body := `{"hold_id":"hold_s","user_id":` + strconv.Itoa(user.Id) + `,"token_id":` + strconv.Itoa(token.Id) +
		`,"quota":1000000,"job_id":"job_s","sku":"a10g-24gb-x1","expires_at":` + strconv.FormatInt(time.Now().Unix()+3600, 10) + `}`
	code, envelope := callComputeBridge(t, engine, "/api/compute/v1/holds", body)
	require.Equal(t, http.StatusOK, code, envelope)

	for name, settle := range map[string]string{
		"absent": `{"event_id":"ue_s1","cumulative_quota":500,"gpu_seconds":1,"final":false}`,
		"empty":  `{"event_id":"ue_s1","cumulative_quota":500,"gpu_seconds":1,"sku":"","final":false}`,
		"final":  `{"event_id":"ue_s1","cumulative_quota":500,"gpu_seconds":1,"final":true}`,
	} {
		code, envelope = callComputeBridge(t, engine, "/api/compute/v1/holds/hold_s/settle", settle)
		assert.Equal(t, http.StatusBadRequest, code, name)
		assert.Equal(t, "invalid_request", envelope["message"], name)
	}

	var consumeRows int64
	require.NoError(t, db.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&consumeRows).Error)
	assert.Zero(t, consumeRows, "a refused settle writes no consume row")
	var stored model.Tenant
	require.NoError(t, db.First(&stored, tenant.Id).Error)
	assert.Zero(t, stored.UsedQuota)
	hold, err := model.GetComputeHold("hold_s")
	require.NoError(t, err)
	assert.Zero(t, hold.SettledQuota)
	assert.Equal(t, model.ComputeHoldStatusActive, hold.Status, "a refused final settle does not close the hold")

	code, envelope = callComputeBridge(t, engine, "/api/compute/v1/holds/hold_s/settle",
		`{"event_id":"ue_s1","cumulative_quota":500,"gpu_seconds":1,"sku":"a10g-24gb-x1","final":false}`)
	require.Equal(t, http.StatusOK, code, "the refused event_id was not consumed: %v", envelope)
	assert.EqualValues(t, 500, envelope["data"].(map[string]any)["settled_quota"])

	code, envelope = callComputeBridge(t, engine, "/api/compute/v1/holds/nope/settle",
		`{"event_id":"ue_s2","cumulative_quota":1,"gpu_seconds":1,"final":false}`)
	assert.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, "hold_not_found", envelope["message"], "an unknown hold is reported before a missing sku")
}
