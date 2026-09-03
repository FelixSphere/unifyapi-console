/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package controller

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func partnershipProgramPayload(c *gin.Context) (*model.PartnershipProgram, bool) {
	var program model.PartnershipProgram
	if err := c.ShouldBindJSON(&program); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid request body"})
		return nil, false
	}
	if err := model.ValidatePartnershipProgram(&program); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return nil, false
	}
	groups := setting.GetUserUsableGroupsCopy()
	if _, ok := groups[program.Group]; !ok || !ratio_setting.ContainsGroupRatio(program.Group) {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "group must exist in Group Pricing"})
		return nil, false
	}
	return &program, true
}

func GetPublicPartnershipProgram(c *gin.Context) {
	program, err := model.GetPartnershipProgramByCode(c.Param("code"))
	if err != nil || !model.IsPartnershipProgramActive(program, time.Now().Unix()) {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "partnership program is unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{
		"name": program.Name, "code": program.Code, "group": program.Group,
		"grant_quota":     program.GrantQuota,
		"grant_available": program.GrantQuota > 0 && program.ClaimedCount < program.GrantLimit,
	}})
}

func ConnectExistingUserToPartnership(c *gin.Context) {
	connection, err := model.ConnectExistingUserToPartnership(c.GetInt("id"), c.Param("code"))
	if err != nil {
		if errors.Is(err, model.ErrPartnershipProgramUnavailable) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": connection})
}

func GetPartnershipPrograms(c *gin.Context) {
	programs, err := model.GetPartnershipPrograms()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{
		"programs":     programs,
		"group_ratios": ratio_setting.GetGroupRatioCopy(),
		"groups":       setting.GetUserUsableGroupsCopy(),
	}})
}

func CreatePartnershipProgram(c *gin.Context) {
	program, ok := partnershipProgramPayload(c)
	if !ok {
		return
	}
	if err := model.CreatePartnershipProgram(program); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": program})
}

func UpdatePartnershipProgram(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid partnership program id"})
		return
	}
	program, ok := partnershipProgramPayload(c)
	if !ok {
		return
	}
	if err := model.UpdatePartnershipProgram(id, program); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "partnership program not found"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}
