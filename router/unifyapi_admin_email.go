/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-gonic/gin"
)

// UNIFYAPI-FORK: email to customers from the admin Users page. AdminAuth on
// the group, before any handler: these routes read every customer's address
// and send mail in our name. The Reply-To option is RootAuth, like every
// other option write.
func registerAdminEmailRoutes(apiRouter *gin.RouterGroup) {
	route := apiRouter.Group("/admin-email")
	route.Use(middleware.AdminAuth())
	{
		route.GET("/templates", controller.ListAdminEmailTemplates)
		route.POST("/templates", controller.CreateAdminEmailTemplate)
		route.PUT("/templates/:id", controller.UpdateAdminEmailTemplate)
		route.DELETE("/templates/:id", controller.DeleteAdminEmailTemplate)
		route.POST("/preview", controller.PreviewAdminEmail)
		route.POST("/send", controller.SendAdminEmail)
		route.GET("/sends", controller.ListAdminEmailSends)
		route.PUT("/settings", middleware.RootAuth(), controller.UpdateAdminEmailSettings)
	}
}
