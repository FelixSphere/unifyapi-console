/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupServiceCreditPool(t *testing.T) (*model.User, *model.Tenant, *model.CreditPool, *model.TenantCreditGrant, *model.CreditPoolLot) {
	t.Helper()
	require.NoError(t, model.DB.AutoMigrate(
		&model.Tenant{}, &model.Channel{}, &model.CreditPool{}, &model.CreditPoolLot{}, &model.TenantCreditGrant{},
		&model.CreditPoolReservation{}, &model.CreditPoolReservationLot{},
	))
	t.Cleanup(func() {
		model.DB.Exec("DELETE FROM credit_pool_reservation_lots")
		model.DB.Exec("DELETE FROM credit_pool_reservations")
		model.DB.Exec("DELETE FROM tenant_credit_grants")
		model.DB.Exec("DELETE FROM credit_pool_lots")
		model.DB.Exec("DELETE FROM credit_pools")
		model.DB.Delete(&model.Channel{}, 900007)
		model.DB.Exec("DELETE FROM tenants")
		model.DB.Exec("DELETE FROM users")
	})
	tenant := &model.Tenant{Name: "Promo tenant", Slug: "promo-tenant", Quota: 900}
	require.NoError(t, model.DB.Create(tenant).Error)
	user := &model.User{Username: "promo-user", TenantId: tenant.Id, Status: 1, Group: "customer"}
	require.NoError(t, model.DB.Create(user).Error)
	require.NoError(t, model.DB.Create(&model.Channel{Id: 900007, Name: "promo-channel", Key: "test", Status: 1}).Error)
	pool := &model.CreditPool{Name: "Promo pool", RoutingGroup: "promo-route", Models: "gpt-*"}
	require.NoError(t, model.CreateCreditPool(pool))
	lot := &model.CreditPoolLot{PoolId: pool.Id, ChannelId: 900007, SourceType: model.CreditPoolSourceFree, OriginalQuota: 1_000}
	require.NoError(t, model.AddCreditPoolLot(lot))
	grant := &model.TenantCreditGrant{TenantId: tenant.Id, PoolId: pool.Id, Name: "Welcome", OriginalQuota: 500}
	require.NoError(t, model.CreateTenantCreditGrant(grant))
	return user, tenant, pool, grant, lot
}

func TestPromotionalBillingSessionUsesGrantBeforeCash(t *testing.T) {
	user, tenant, pool, grant, lot := setupServiceCreditPool(t)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{
		RequestId: "promo-session", UserId: user.Id, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 900007}, IsPlayground: true,
		CreditPoolId: pool.Id, CreditGrantId: grant.Id,
		PriceData: hosttypes.PriceData{GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 0.5}},
	}

	apiErr := PreConsumeBilling(ctx, 100, info)
	require.Nil(t, apiErr)
	assert.Equal(t, BillingSourcePromotional, info.BillingSource)
	assert.Positive(t, info.CreditReservationId)
	require.NoError(t, info.Billing.Reserve(150))
	var pending model.CreditPoolReservation
	require.NoError(t, model.DB.First(&pending, info.CreditReservationId).Error)
	assert.Equal(t, model.CreditReservationPending, pending.Status)
	require.NoError(t, info.Billing.Settle(120))

	var storedTenant model.Tenant
	require.NoError(t, model.DB.First(&storedTenant, tenant.Id).Error)
	assert.Equal(t, 900, storedTenant.Quota)
	var storedGrant model.TenantCreditGrant
	require.NoError(t, model.DB.First(&storedGrant, grant.Id).Error)
	assert.EqualValues(t, 380, storedGrant.RemainingQuota)
	var storedLot model.CreditPoolLot
	require.NoError(t, model.DB.First(&storedLot, lot.Id).Error)
	assert.EqualValues(t, 760, storedLot.RemainingQuota)

	other := map[string]any{}
	assert.Zero(t, CustomerChargeQuota(info, 120, other))
	assert.Equal(t, 120, other["promotional_quota"])
	assert.Equal(t, info.CreditReservationId, other["credit_reservation_id"])
}
