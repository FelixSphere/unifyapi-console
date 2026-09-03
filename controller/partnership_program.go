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

	"github.com/QuantumNous/new-api/model"
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
	if !ratio_setting.ContainsGroupRatio(program.Group) {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "group must exist in Group Pricing"})
		return nil, false
	}
	return &program, true
}

func GetPublicPartnershipProgram(c *gin.Context) {
	offer, err := model.GetActivePartnershipOfferByCode(c.Param("code"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "partnership program is unavailable"})
		return
	}
	program := offer.Program
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{
		"name": program.Name, "code": offer.CustomerCode, "group": offer.CustomerGroup,
		"customer_name":   offer.CustomerName,
		"grant_quota":     program.GrantQuota,
		"grant_available": program.GrantQuota > 0 && program.ClaimedCount < program.GrantLimit,
	}})
}

func partnershipCustomerPayload(c *gin.Context) (*model.PartnershipCustomer, bool) {
	var customer model.PartnershipCustomer
	if err := c.ShouldBindJSON(&customer); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid request body"})
		return nil, false
	}
	if err := model.ValidatePartnershipCustomer(&customer); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return nil, false
	}
	if !ratio_setting.ContainsGroupRatio(customer.Group) {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "group must exist in Group Pricing"})
		return nil, false
	}
	return &customer, true
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
		"groups":       ratio_setting.GetGroupRatioCopy(),
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

func CreatePartnershipCustomer(c *gin.Context) {
	programId, err := strconv.Atoi(c.Param("id"))
	if err != nil || programId <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid partnership program id"})
		return
	}
	customer, ok := partnershipCustomerPayload(c)
	if !ok {
		return
	}
	if err := model.CreatePartnershipCustomer(programId, customer); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "partnership program not found"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": customer})
}

func UpdatePartnershipCustomer(c *gin.Context) {
	programId, programErr := strconv.Atoi(c.Param("id"))
	customerId, customerErr := strconv.Atoi(c.Param("customerId"))
	if programErr != nil || customerErr != nil || programId <= 0 || customerId <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid partnership customer id"})
		return
	}
	customer, ok := partnershipCustomerPayload(c)
	if !ok {
		return
	}
	if err := model.UpdatePartnershipCustomer(programId, customerId, customer); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "partnership customer not found"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

func RemovePartnershipCustomer(c *gin.Context) {
	programId, programErr := strconv.Atoi(c.Param("id"))
	customerId, customerErr := strconv.Atoi(c.Param("customerId"))
	if programErr != nil || customerErr != nil || programId <= 0 || customerId <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid partnership customer id"})
		return
	}
	if err := model.RemovePartnershipCustomer(programId, customerId); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "partnership customer not found"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}
