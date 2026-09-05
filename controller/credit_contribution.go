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

func contributionIdParam(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid credit contribution id"})
		return 0, false
	}
	return id, true
}

func GetMyCreditContributions(c *gin.Context) {
	contributions, err := model.ListUserCreditContributions(c.GetInt("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": contributions})
}

func CreateMyCreditContribution(c *gin.Context) {
	var input model.CreateCreditContributionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid request body"})
		return
	}
	contribution, err := model.CreateCreditContribution(c.GetInt("id"), input)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": contribution})
}

func CancelMyCreditContribution(c *gin.Context) {
	id, ok := contributionIdParam(c)
	if !ok {
		return
	}
	if err := model.CancelCreditContribution(c.GetInt("id"), id); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

func GetCreditContributions(c *gin.Context) {
	contributions, err := model.ListCreditContributions()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": contributions})
}

func ReviewCreditContribution(c *gin.Context) {
	id, ok := contributionIdParam(c)
	if !ok {
		return
	}
	var input struct {
		Status     string `json:"status"`
		Message    string `json:"message"`
		AdminNotes string `json:"admin_notes"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid request body"})
		return
	}
	if err := model.ReviewCreditContribution(id, c.GetInt("id"), input.Status, input.Message, input.AdminNotes); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	recordManageAudit(c, "credit_contribution.review", map[string]interface{}{"contribution_id": id, "status": input.Status})
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

func ActivateCreditContribution(c *gin.Context) {
	id, ok := contributionIdParam(c)
	if !ok {
		return
	}
	var input model.ActivateCreditContributionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid request body"})
		return
	}
	contribution, err := model.ActivateCreditContribution(id, c.GetInt("id"), input)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	recordManageAudit(c, "credit_contribution.activate", map[string]interface{}{"contribution_id": id, "pool_id": input.PoolId, "channel_id": input.ChannelId, "quota": input.ApprovedQuota})
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": contribution})
}

func ResetCreditContribution(c *gin.Context) {
	id, ok := contributionIdParam(c)
	if !ok {
		return
	}
	var input model.ResetCreditContributionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid request body"})
		return
	}
	contribution, err := model.ResetCreditContribution(id, c.GetInt("id"), input)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	recordManageAudit(c, "credit_contribution.reset", map[string]interface{}{"contribution_id": id, "cycle": contribution.Cycle, "quota": input.VerifiedQuota})
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": contribution})
}

func RevokeCreditContribution(c *gin.Context) {
	id, ok := contributionIdParam(c)
	if !ok {
		return
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid request body"})
		return
	}
	if err := model.RevokeCreditContribution(id, c.GetInt("id"), input.Reason); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	recordManageAudit(c, "credit_contribution.revoke", map[string]interface{}{"contribution_id": id})
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

func CreateContributionPayout(c *gin.Context) {
	id, ok := contributionIdParam(c)
	if !ok {
		return
	}
	var input struct {
		AmountQuota int64  `json:"amount_quota"`
		Note        string `json:"note"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid request body"})
		return
	}
	payout, err := model.CreateContributionPayout(id, c.GetInt("id"), input.AmountQuota, input.Note)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	recordManageAudit(c, "credit_contribution.payout_create", map[string]interface{}{"contribution_id": id, "payout_id": payout.Id, "amount_quota": payout.AmountQuota})
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": payout})
}

func UpdateContributionPayout(c *gin.Context) {
	payoutId, err := strconv.Atoi(c.Param("payout_id"))
	if err != nil || payoutId <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid payout id"})
		return
	}
	var input struct {
		Status            string `json:"status"`
		ExternalReference string `json:"external_reference"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid request body"})
		return
	}
	if err := model.UpdateContributionPayout(payoutId, c.GetInt("id"), input.Status, input.ExternalReference); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	recordManageAudit(c, "credit_contribution.payout_update", map[string]interface{}{"payout_id": payoutId, "status": input.Status})
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}
