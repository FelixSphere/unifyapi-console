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

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"net/http"
	"strconv"
)

// SupportFeedback is the read-only connector surface. AdminAuth protects the
// production route; the role check also prevents accidental unguarded wiring.
func SupportFeedback(c *gin.Context) {
	if c.GetInt("role") < common.RoleAdminUser {
		supportFail(c, http.StatusForbidden, "operators only")
		return
	}
	after, err := strconv.Atoi(c.DefaultQuery("after", "0"))
	if err != nil || after < 0 {
		supportFail(c, http.StatusBadRequest, "invalid feedback cursor")
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "25"))
	if err != nil || limit < 1 || limit > 100 {
		supportFail(c, http.StatusBadRequest, "invalid feedback page size")
		return
	}
	messages, err := model.ListSupportFeedback(after, limit)
	if err != nil {
		supportFail(c, http.StatusInternalServerError, "could not read feedback")
		return
	}
	next := after
	if len(messages) > 0 {
		next = messages[len(messages)-1].Id
	}
	supportOK(c, gin.H{"messages": messages, "next_cursor": next})
}
