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

// GetUnreachableEmails lists addresses whose most recent send was rejected by
// the mail server, most recent first.
//
// This answers "can we still reach this person", and only to the extent send
// time can answer it. An address is listed when the server refused the
// message; acceptance is not proof of delivery, so absence from this list
// means "nothing has contradicted it", not "confirmed reachable".
func GetUnreachableEmails(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	rows, err := model.GetUnreachableEmails(limit)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if rows == nil {
		rows = []*model.EmailDeliveryState{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": rows})
}
