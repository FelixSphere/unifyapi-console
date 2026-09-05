/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package controller

// UNIFYAPI-FORK: root-only management of the supplier credit supply. See
// docs/credit-supply.md. The supplier-facing portal lives in credit_supplier_portal.go.

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func GetCreditSupplyOverview(c *gin.Context) {
	overview, err := model.GetCreditSupplyOverview()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": overview})
}

func GetCreditSuppliers(c *gin.Context) {
	suppliers, err := model.GetCreditSuppliers()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": suppliers})
}

func CreateCreditSupplier(c *gin.Context) {
	var supplier model.CreditSupplier
	if err := c.ShouldBindJSON(&supplier); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid request body"})
		return
	}
	if err := model.CreateCreditSupplier(&supplier); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": supplier})
}

func UpdateCreditSupplier(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid supplier id"})
		return
	}
	var supplier model.CreditSupplier
	if err := c.ShouldBindJSON(&supplier); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid request body"})
		return
	}
	if err := model.UpdateCreditSupplier(id, &supplier); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "supplier not found"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

func GetCreditLots(c *gin.Context) {
	supplierId, _ := strconv.Atoi(c.Query("supplier_id"))
	lots, err := model.GetCreditLots(model.CreditLotFilter{
		SupplierId: supplierId,
		Status:     c.Query("status"),
	})
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": lots})
}

// creditLotPayload is what the screen may set. Consumption, notification state
// and retirement are never accepted from the caller.
type creditLotPayload struct {
	SupplierId      int     `json:"supplier_id"`
	Vendor          string  `json:"vendor"`
	ChannelId       int     `json:"channel_id"`
	FaceValueUSD    float64 `json:"face_value_usd"`
	AcquisitionRate float64 `json:"acquisition_rate"`
	LowWaterUSD     float64 `json:"low_water_usd"`
	ExpiresAt       int64   `json:"expires_at"`
	Status          string  `json:"status"`
	Note            string  `json:"note"`
}

func (p creditLotPayload) lot() *model.CreditLot {
	return &model.CreditLot{
		SupplierId:      p.SupplierId,
		Vendor:          p.Vendor,
		ChannelId:       p.ChannelId,
		FaceValueUSD:    p.FaceValueUSD,
		AcquisitionRate: p.AcquisitionRate,
		LowWaterUSD:     p.LowWaterUSD,
		ExpiresAt:       p.ExpiresAt,
		Status:          p.Status,
		Source:          model.CreditLotSourceAdmin,
		Note:            p.Note,
	}
}

func CreateCreditLot(c *gin.Context) {
	var payload creditLotPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid request body"})
		return
	}
	lot := payload.lot()
	if err := model.CreateCreditLot(lot, optionChangeActor(c)); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": lot})
}

func UpdateCreditLot(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid lot id"})
		return
	}
	var payload creditLotPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid request body"})
		return
	}
	if err := model.UpdateCreditLot(id, payload.lot(), optionChangeActor(c)); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "lot not found"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

func TransitionCreditLot(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid lot id"})
		return
	}
	var req struct {
		To                      string `json:"to"`
		Reason                  string `json:"reason"`
		TransferRightsConfirmed bool   `json:"transfer_rights_confirmed"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.To == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "expected {\"to\": \"active|suspended|rejected\", \"reason\": \"...\", \"transfer_rights_confirmed\": true}"})
		return
	}
	switch req.To {
	case model.CreditLotStatusActive, model.CreditLotStatusSuspended, model.CreditLotStatusRejected:
	default:
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "exhausted and expired are reached automatically, not by request"})
		return
	}
	lot, err := model.TransitionCreditLot(id, model.CreditLotTransition{
		To: req.To, Actor: optionChangeActor(c), Reason: req.Reason,
		TransferRightsConfirmed: req.TransferRightsConfirmed,
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "lot not found"})
			return
		}
		status := http.StatusOK
		if errors.Is(err, model.ErrCreditLotTransition) {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": lot})
}

func GetCreditLotEvents(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid lot id"})
		return
	}
	events, err := model.GetCreditLotEvents(id, 100)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": events})
}

func GetCreditLotUsage(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid lot id"})
		return
	}
	days, _ := strconv.Atoi(c.DefaultQuery("days", "30"))
	if days > 366 {
		days = 366
	}
	rows, err := model.GetCreditLotUsage(id, days)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": rows})
}
