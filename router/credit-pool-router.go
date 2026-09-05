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

func registerCreditPoolRoutes(apiRouter *gin.RouterGroup) {
	apiRouter.GET("/credit-pool/self", middleware.UserAuth(), controller.GetMyPromotionalCredits)

	poolRoute := apiRouter.Group("/credit-pool")
	poolRoute.Use(middleware.AdminAuth())
	{
		poolRoute.GET("/", controller.GetCreditPools)
		poolRoute.GET("/:id", controller.GetCreditPool)
		poolRoute.POST("/", middleware.RootAuth(), controller.CreateCreditPool)
		poolRoute.POST("/:id/lots", middleware.RootAuth(), controller.AddCreditPoolLot)
		poolRoute.POST("/:id/grants", middleware.RootAuth(), controller.AddTenantCreditGrant)
	}

}
