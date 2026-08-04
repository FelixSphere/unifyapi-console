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

// Operator-facing tenant reporting. Admin-only: this exposes every customer's
// balance and spend, so it must never be reachable by a normal user.
//
// Upstream's admin surfaces are per-user and global, so there was no way to see
// "which customers exist, what are they running, what have they spent" without
// aggregating by hand.

const defaultTenantPageSize = 100

// GET /api/tenant/
// Query: start_at, end_at (unix seconds, optional), page_size, offset
func GetTenantOverviews(c *gin.Context) {
	startAt, _ := strconv.ParseInt(c.Query("start_at"), 10, 64)
	endAt, _ := strconv.ParseInt(c.Query("end_at"), 10, 64)

	pageSize, _ := strconv.Atoi(c.Query("page_size"))
	if pageSize <= 0 {
		pageSize = defaultTenantPageSize
	}
	offset, _ := strconv.Atoi(c.Query("offset"))

	overviews, err := model.GetTenantOverviews(startAt, endAt, pageSize, offset)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	total, err := model.CountTenants()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"items": overviews,
			"total": total,
		},
	})
}

// GET /api/tenant/:id/usage
// Query: start_at, end_at (unix seconds, optional)
func GetTenantUsage(c *gin.Context) {
	tenantId, err := strconv.Atoi(c.Param("id"))
	if err != nil || tenantId <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid tenant id"})
		return
	}

	startAt, _ := strconv.ParseInt(c.Query("start_at"), 10, 64)
	endAt, _ := strconv.ParseInt(c.Query("end_at"), 10, 64)

	tenant, err := model.GetTenantById(tenantId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	usage, err := model.GetTenantModelUsage(tenantId, startAt, endAt)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	members, err := model.GetTenantMembers(tenantId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	// Project members down to what an operator needs. The full User row carries
	// password hashes and OAuth identifiers that have no business here.
	type memberView struct {
		Id          int    `json:"id"`
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
		Status      int    `json:"status"`
		Role        int    `json:"role"`
		LastLoginAt int64  `json:"last_login_at"`
	}
	memberViews := make([]memberView, 0, len(members))
	for _, m := range members {
		memberViews = append(memberViews, memberView{
			Id:          m.Id,
			Username:    m.Username,
			DisplayName: m.DisplayName,
			Email:       m.Email,
			Status:      m.Status,
			Role:        m.Role,
			LastLoginAt: m.LastLoginAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"tenant":  tenant,
			"models":  usage,
			"members": memberViews,
		},
	})
}

// ---------------------------------------------------------------------------
// Lifecycle actions
// ---------------------------------------------------------------------------

type tenantSuspendRequest struct {
	Reason string `json:"reason"`
}

// POST /api/tenant/:id/suspend
func SuspendTenant(c *gin.Context) {
	tenantId, ok := tenantIdParam(c)
	if !ok {
		return
	}
	var req tenantSuspendRequest
	_ = c.ShouldBindJSON(&req) // reason is optional

	if err := model.SuspendTenant(tenantId, req.Reason); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

// POST /api/tenant/:id/resume
func ResumeTenant(c *gin.Context) {
	tenantId, ok := tenantIdParam(c)
	if !ok {
		return
	}
	if err := model.ResumeTenant(tenantId); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

type tenantExtendRequest struct {
	Days      int   `json:"days"`
	ExpiresAt int64 `json:"expires_at"`
}

// POST /api/tenant/:id/extend
// Either {"days": 30} to extend relative to the current term, or
// {"expires_at": <unix>} to set it outright (0 = open-ended).
func ExtendTenantTerm(c *gin.Context) {
	tenantId, ok := tenantIdParam(c)
	if !ok {
		return
	}
	var req tenantExtendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid request body"})
		return
	}

	if req.Days != 0 {
		expiry, err := model.ExtendTenantTerm(tenantId, req.Days)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"success": true, "message": "",
			"data": gin.H{"expires_at": expiry},
		})
		return
	}

	if err := model.SetTenantExpiry(tenantId, req.ExpiresAt); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true, "message": "",
		"data": gin.H{"expires_at": req.ExpiresAt},
	})
}

// GET /api/tenant/:id/payments
func GetTenantPayments(c *gin.Context) {
	tenantId, ok := tenantIdParam(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	payments, err := model.GetTenantPayments(tenantId, limit)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": payments})
}

// GET /api/tenant/:id/audits
func GetTenantAudits(c *gin.Context) {
	tenantId, ok := tenantIdParam(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	entries, err := model.GetTenantAuditLog(tenantId, limit)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": entries})
}

func tenantIdParam(c *gin.Context) (int, bool) {
	tenantId, err := strconv.Atoi(c.Param("id"))
	if err != nil || tenantId <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid tenant id"})
		return 0, false
	}
	return tenantId, true
}
