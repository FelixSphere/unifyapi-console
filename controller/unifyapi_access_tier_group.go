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

	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

type accessTierGroupRequest struct {
	Group      string `json:"group"`
	AccessTier *bool  `json:"access_tier"`
}

// GetAccessTierGroups lists the pricing groups marked as access tiers.
// See model/unifyapi_access_tier_group.go.
func GetAccessTierGroups(c *gin.Context) {
	groups, err := model.AccessTierGroups()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"groups": groups}})
}

// SetAccessTierGroup marks one pricing group as an access tier, or removes the
// mark. It never moves a login between wallets.
func SetAccessTierGroup(c *gin.Context) {
	var req accessTierGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid request body: " + err.Error()})
		return
	}
	// A missing flag must not read as "remove the mark".
	if req.AccessTier == nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "access_tier is required"})
		return
	}
	change, err := model.SetPricingGroupAccessTier(req.Group, *req.AccessTier, optionChangeActor(c))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	action, message := "pricing.group_access_tier_unmark", fmt.Sprintf("%q is no longer an access tier", change.Group)
	if change.AccessTier {
		action, message = "pricing.group_access_tier_mark", fmt.Sprintf("%q is an access tier: its logins keep their own wallets", change.Group)
	}
	if change.Changed {
		recordManageAudit(c, action, map[string]interface{}{
			"group": change.Group, "released_wallet_tenant": change.ReleasedWalletTenant,
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": message, "data": change})
}
