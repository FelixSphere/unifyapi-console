/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package service

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// NotifyOperatorCredit tells a user that an operator has just added credit to
// their account. Operator rule (2026-09-20): "如果是 super admin 给他们加了钱，
// 给这些用户的邮箱发信息通知". It goes through NotifyUser, so it lands in the
// user's email by default and in their webhook/Bark/Gotify if they configured
// one instead, and it is subject to the same hourly notification limit as the
// low-balance notice. Nothing is sent for a zero or negative delta.
//
// Called after the credit is applied; failure to notify never fails the
// credit, it is logged.
func NotifyOperatorCredit(user *model.User, added int, balance int) {
	if user == nil || added <= 0 {
		return
	}
	prompt := "Credit added to your account" // UNIFYAPI-BRAND: English copy
	amount := logger.FormatQuota(added)
	remaining := logger.FormatQuota(balance)
	link := strings.TrimRight(system_setting.ServerAddress, "/")

	setting := user.GetSetting()
	var content string
	var values []interface{}
	switch setting.NotifyType {
	case dto.NotifyTypeBark, dto.NotifyTypeGotify:
		// Short text, no HTML.
		content = "{{value}} has been added to your account by the operator. Your balance is now {{value}}."
		values = []interface{}{amount, remaining}
	default:
		content = "{{value}} has been added to your account by the operator.<br/>Your balance is now <b>{{value}}</b>.<br/>Sign in to see the details: <a href='{{value}}'>{{value}}</a>"
		values = []interface{}{amount, remaining, link, link}
	}
	if err := NotifyUser(user.Id, user.Email, setting, dto.NewNotify(dto.NotifyTypeOperatorCredit, prompt, content, values)); err != nil {
		common.SysError(fmt.Sprintf("failed to notify user %d of an operator credit: %s", user.Id, err.Error()))
	}
}
