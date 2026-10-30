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

// UNIFYAPI-FORK: unsubscribe from the weekly update. No sign-in: the link is
// signed per user (service.RetentionUnsubscribeToken).
//
// GET only shows a confirm button; POST unsubscribes. Mail scanners (Outlook
// Safe Links, corporate gateways) fetch every link in a message with GET, so
// a GET that unsubscribed would opt people out who never clicked. POST is
// also what RFC 8058 one-click sends from the mail client's own button.

import (
	"html/template"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

var unsubscribePage = template.Must(template.New("unsubscribe").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Product}} · Email preferences</title>
<style>body{font-family:Helvetica,Arial,sans-serif;background:#F5F4EF;color:#0E0E0E;margin:0;padding:48px 16px}
main{max-width:480px;margin:0 auto;background:#fff;padding:32px;border-radius:8px}
h1{font-size:22px;margin:0 0 12px}p{line-height:1.5;color:#3A3A36}
button{background:#F5A623;color:#0E0E0E;border:0;border-radius:6px;padding:12px 20px;font-size:16px;font-weight:bold;cursor:pointer}</style>
</head><body><main>
<h1>{{.Title}}</h1><p>{{.Body}}</p>
{{if .Confirm}}<form method="post"><button type="submit">Unsubscribe</button></form>{{end}}
</main></body></html>`))

type unsubscribeView struct {
	Product, Title, Body string
	Confirm              bool
}

func renderUnsubscribe(c *gin.Context, status int, v unsubscribeView) {
	v.Product = common.SystemName
	c.Status(status)
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	_ = unsubscribePage.Execute(c.Writer, v)
}

func retentionUnsubscribeTarget(c *gin.Context) (int, bool) {
	userId, err := strconv.Atoi(c.Query("u"))
	if err != nil || !service.VerifyRetentionUnsubscribeToken(userId, c.Query("t")) {
		renderUnsubscribe(c, http.StatusBadRequest, unsubscribeView{
			Title: "This link is not valid",
			Body:  "It may have been copied incompletely. Use the Unsubscribe link in the latest email, or reply to it and we will remove you.",
		})
		return 0, false
	}
	return userId, true
}

func RetentionUnsubscribePage(c *gin.Context) {
	userId, ok := retentionUnsubscribeTarget(c)
	if !ok {
		return
	}
	if model.IsRetentionEmailUnsubscribed(userId) {
		renderUnsubscribe(c, http.StatusOK, unsubscribeView{
			Title: "You are unsubscribed",
			Body:  "You will not get the weekly update. Account emails such as password resets still arrive.",
		})
		return
	}
	renderUnsubscribe(c, http.StatusOK, unsubscribeView{
		Title:   "Unsubscribe from the weekly update?",
		Body:    "You will stop getting the weekly update. Account emails such as password resets still arrive.",
		Confirm: true,
	})
}

func RetentionUnsubscribe(c *gin.Context) {
	userId, ok := retentionUnsubscribeTarget(c)
	if !ok {
		return
	}
	if err := model.UnsubscribeRetentionEmail(userId); err != nil {
		common.SysError("weekly update: unsubscribe failed: " + err.Error())
		renderUnsubscribe(c, http.StatusInternalServerError, unsubscribeView{
			Title: "Something went wrong",
			Body:  "Please try again, or reply to the email and we will remove you.",
		})
		return
	}
	renderUnsubscribe(c, http.StatusOK, unsubscribeView{
		Title: "You are unsubscribed",
		Body:  "You will not get the weekly update. Account emails such as password resets still arrive.",
	})
}
