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

type renamePricingGroupRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// RenamePricingGroup renames a pricing group and everything that refers to it.
// See model/unifyapi_pricing_group_rename.go.
func RenamePricingGroup(c *gin.Context) {
	var req renamePricingGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid request body: " + err.Error()})
		return
	}

	// The customer price editor merges one group into the table it read from
	// memory; a save landing in the middle of a rename would write the old
	// name back.
	groupModelPricingUpdateMu.Lock()
	defer groupModelPricingUpdateMu.Unlock()

	result, err := model.RenamePricingGroup(req.From, req.To, optionChangeActor(c))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	recordManageAudit(c, "pricing.group_rename", map[string]interface{}{
		"from": result.From, "to": result.To, "options": result.Options, "rows": result.Rows,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Renamed %q to %q", result.From, result.To),
		"data":    result,
	})
}
