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

	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

func creditPoolIdParam(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid credit pool id"})
		return 0, false
	}
	return id, true
}

func GetCreditPools(c *gin.Context) {
	pools, err := model.ListCreditPools()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": pools})
}

func GetCreditPool(c *gin.Context) {
	poolId, ok := creditPoolIdParam(c)
	if !ok {
		return
	}
	lots, err := model.GetCreditPoolLots(poolId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	grants, err := model.GetCreditPoolGrants(poolId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"lots": lots, "grants": grants}})
}

func CreateCreditPool(c *gin.Context) {
	var pool model.CreditPool
	if err := c.ShouldBindJSON(&pool); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid request body"})
		return
	}
	pool.Id = 0
	if err := model.CreateCreditPool(&pool); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": pool})
}

func AddCreditPoolLot(c *gin.Context) {
	poolId, ok := creditPoolIdParam(c)
	if !ok {
		return
	}
	var lot model.CreditPoolLot
	if err := c.ShouldBindJSON(&lot); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid request body"})
		return
	}
	lot.Id = 0
	lot.PoolId = poolId
	if err := model.AddCreditPoolLot(&lot); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": lot})
}

func AddTenantCreditGrant(c *gin.Context) {
	poolId, ok := creditPoolIdParam(c)
	if !ok {
		return
	}
	var grant model.TenantCreditGrant
	if err := c.ShouldBindJSON(&grant); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid request body"})
		return
	}
	grant.Id = 0
	grant.PoolId = poolId
	if err := model.CreateTenantCreditGrant(&grant); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": grant})
}

func GetMyPromotionalCredits(c *gin.Context) {
	grants, err := model.GetUserCreditGrants(c.GetInt("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	var original, remaining int64
	for _, grant := range grants {
		original += grant.OriginalQuota
		remaining += grant.RemainingQuota
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true, "message": "",
		"data": gin.H{"original_quota": original, "remaining_quota": remaining, "grants": grants},
	})
}
