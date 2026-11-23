/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package controller

// UNIFYAPI-FORK: admin endpoints for emailing customers from the Users page.
// Every route is AdminAuth (router/unifyapi_admin_email.go); the Reply-To
// setting is RootAuth like every other option write. Writes are audited by
// AdminAuth itself. See service/unifyapi_admin_email.go.

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type adminEmailTemplateRequest struct {
	Name     string `json:"name"`
	Subject  string `json:"subject"`
	Headline string `json:"headline"`
	Body     string `json:"body"`
	CTALabel string `json:"cta_label"`
	CTAURL   string `json:"cta_url"`
}

type adminEmailComposeRequest struct {
	service.AdminEmailContent
	UserIds []int `json:"user_ids"`
}

func adminEmailFail(c *gin.Context, message string) {
	c.JSON(http.StatusOK, gin.H{"success": false, "message": message})
}

func adminEmailOK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": data})
}

// GET /api/admin-email/templates
func ListAdminEmailTemplates(c *gin.Context) {
	rows, err := model.ListAdminEmailTemplates()
	if err != nil {
		adminEmailFail(c, err.Error())
		return
	}
	adminEmailOK(c, gin.H{
		"templates":  rows,
		"variables":  service.AdminEmailVariables,
		"reply_to":   strings.TrimSpace(system_setting.GetAdminEmailSettings().ReplyTo),
		"smtp_ready": common.SMTPServer != "",
		"max":        service.AdminEmailMaxRecipients,
	})
}

func (r adminEmailTemplateRequest) toTemplate() (*model.AdminEmailTemplate, error) {
	t := &model.AdminEmailTemplate{
		Name:     strings.TrimSpace(r.Name),
		Subject:  strings.TrimSpace(r.Subject),
		Headline: strings.TrimSpace(r.Headline),
		Body:     strings.TrimSpace(r.Body),
		CTALabel: strings.TrimSpace(r.CTALabel),
		CTAURL:   strings.TrimSpace(r.CTAURL),
	}
	if t.Name == "" || utf8.RuneCountInString(t.Name) > 100 {
		return nil, errors.New("a template needs a name of at most 100 characters")
	}
	if err := service.ValidateAdminEmailContent(service.AdminEmailContent{Subject: t.Subject, Headline: t.Headline,
		Body: t.Body, CTALabel: t.CTALabel, CTAURL: t.CTAURL}); err != nil {
		return nil, err
	}
	return t, nil
}

// POST /api/admin-email/templates
func CreateAdminEmailTemplate(c *gin.Context) {
	var req adminEmailTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminEmailFail(c, "invalid request")
		return
	}
	t, err := req.toTemplate()
	if err != nil {
		adminEmailFail(c, err.Error())
		return
	}
	t.CreatedBy = c.GetInt("id")
	if err := model.CreateAdminEmailTemplate(t); err != nil {
		adminEmailFail(c, err.Error())
		return
	}
	adminEmailOK(c, t)
}

// PUT /api/admin-email/templates/:id
func UpdateAdminEmailTemplate(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		adminEmailFail(c, "invalid template id")
		return
	}
	var req adminEmailTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminEmailFail(c, "invalid request")
		return
	}
	t, err := req.toTemplate()
	if err != nil {
		adminEmailFail(c, err.Error())
		return
	}
	t.Id, t.UpdatedBy = id, c.GetInt("id")
	if err := model.UpdateAdminEmailTemplate(t); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			adminEmailFail(c, "template not found")
			return
		}
		adminEmailFail(c, err.Error())
		return
	}
	saved, err := model.GetAdminEmailTemplate(id)
	if err != nil {
		adminEmailFail(c, err.Error())
		return
	}
	adminEmailOK(c, saved)
}

// DELETE /api/admin-email/templates/:id
func DeleteAdminEmailTemplate(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		adminEmailFail(c, "invalid template id")
		return
	}
	if err := model.DeleteAdminEmailTemplate(id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			adminEmailFail(c, "template not found")
			return
		}
		adminEmailFail(c, err.Error())
		return
	}
	adminEmailOK(c, nil)
}

// POST /api/admin-email/preview -- renders, sends nothing, writes nothing.
func PreviewAdminEmail(c *gin.Context) {
	var req adminEmailComposeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminEmailFail(c, "invalid request")
		return
	}
	p, err := service.PreviewAdminEmail(req.AdminEmailContent, req.UserIds)
	if err != nil {
		adminEmailFail(c, err.Error())
		return
	}
	adminEmailOK(c, p)
}

// POST /api/admin-email/send
func SendAdminEmail(c *gin.Context) {
	var req adminEmailComposeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminEmailFail(c, "invalid request")
		return
	}
	result, err := service.SendAdminEmail(req.AdminEmailContent, req.UserIds, c.GetInt("id"))
	if err != nil {
		adminEmailFail(c, err.Error())
		return
	}
	adminEmailOK(c, result)
}

// GET /api/admin-email/sends?user_id=&batch_id=&limit=
func ListAdminEmailSends(c *gin.Context) {
	userId, _ := strconv.Atoi(c.Query("user_id"))
	limit, _ := strconv.Atoi(c.Query("limit"))
	rows, err := model.ListAdminEmailSends(userId, strings.TrimSpace(c.Query("batch_id")), limit)
	if err != nil {
		adminEmailFail(c, err.Error())
		return
	}
	adminEmailOK(c, rows)
}

// PUT /api/admin-email/settings {reply_to} -- RootAuth.
func UpdateAdminEmailSettings(c *gin.Context) {
	var req struct {
		ReplyTo string `json:"reply_to"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		adminEmailFail(c, "invalid request")
		return
	}
	replyTo := strings.TrimSpace(req.ReplyTo)
	if replyTo != "" && !common.IsBareEmailAddress(replyTo) {
		adminEmailFail(c, "Reply-To must be one bare address, like support@example.com (no name, no brackets)")
		return
	}
	if err := model.UpdateOption("admin_email.reply_to", replyTo); err != nil {
		adminEmailFail(c, err.Error())
		return
	}
	adminEmailOK(c, gin.H{"reply_to": replyTo})
}
