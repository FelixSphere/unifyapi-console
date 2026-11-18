/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api (Copyright (C) 2023-2026
QuantumNous), distributed under the GNU Affero General Public License v3.
See BRANDING.md for the relationship between this fork and its upstream.

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
package controller

// UNIFYAPI-FORK: operator endpoints for the new-model announcement email.
// RootAuth: Send mails every customer. See service/unifyapi_model_launch_email.go.

import (
	"net/http"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type modelLaunchRequest struct {
	Models []string `json:"models"`
	Note   string   `json:"note"`
}

// GET /api/model-launch/
func GetModelLaunchState(c *gin.Context) {
	candidates, err := service.ModelLaunchCandidates()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	enabled, err := model.EnabledModelNames()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	launches, err := model.ListModelLaunchEmails(20)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	recipients, err := model.CountMarketingRecipients()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{
		"enabled":    service.ModelLaunchEmailEnabled(),
		"candidates": candidates,
		"models":     enabled,
		"recipients": recipients,
		"launches":   launches,
	}})
}

// POST /api/model-launch/preview
func PreviewModelLaunch(c *gin.Context) {
	var req modelLaunchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid request"})
		return
	}
	p, err := service.PreviewModelLaunchEmail(req.Models, req.Note)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": p})
}

// POST /api/model-launch/send
func SendModelLaunch(c *gin.Context) {
	var req modelLaunchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid request"})
		return
	}
	launch, err := service.StartModelLaunchEmail(req.Models, req.Note, c.GetInt("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": launch})
}
