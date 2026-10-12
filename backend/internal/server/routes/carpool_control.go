package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func registerCarpoolControlAdminRoutes(admin *gin.RouterGroup, h *handler.Handlers, stepUp middleware.StepUpAuthMiddleware) {
	if h == nil || h.CarpoolControl == nil {
		return
	}
	control := h.CarpoolControl
	if control.MainEnabled() {
		admin.GET("/carpool/control", control.MainInfo)
		admin.GET("/carpool/cars/:id/connection", control.Connection)
		admin.GET("/carpool/cars/:id/sync", control.SyncStatus)
		admin.GET("/carpool/cars/:id/native", control.CarNativeStatus)
		admin.GET("/carpool/cars/:id/native/accounts", control.CarNativeAccounts)
		admin.GET("/carpool/cars/:id/native/quota", control.CarQuota)
		admin.POST("/carpool/cars/:id/native/quota/sync", gin.HandlerFunc(stepUp), control.SyncCarQuota)
		admin.POST("/carpool/cars/:id/native/prepare", gin.HandlerFunc(stepUp), control.PrepareNativeCar)
		admin.POST("/carpool/cars/:id/native/activate", gin.HandlerFunc(stepUp), control.ActivateNativeCar)
		admin.POST("/carpool/cars/:id/sync/retry", gin.HandlerFunc(stepUp), control.RetrySync)
		admin.PUT("/carpool/nodes/:id/connection", gin.HandlerFunc(stepUp), control.SaveConnection)
		admin.POST("/carpool/nodes/:id/connection/check", gin.HandlerFunc(stepUp), control.CheckConnection)
	}
	if control.NodeEnabled() {
		admin.GET("/carpool-control/v1/info", control.NodeInfo)
		admin.GET("/carpool-control/v1/accounts", control.NativeAccounts)
		admin.POST("/carpool-control/v1/subscription/initialize", control.InitializeNativeSubscription)
		admin.GET("/carpool-control/v1/subscription/quota", control.NativeQuota)
		admin.POST("/carpool-control/v1/subscription/quota/sync", control.SyncNativeQuota)
		admin.GET("/carpool-control/v1/members/:subject/subscription", control.NativeMemberSubscription)
		admin.PUT("/carpool-control/v1/members/:subject", control.ProvisionMember)
		admin.PUT("/carpool-control/v1/members/:subject/grant", control.ApplyMemberGrant)
		admin.POST("/carpool-control/v1/members/:subject/keys", control.CreateMemberKey)
		admin.PATCH("/carpool-control/v1/members/:subject/keys/:key_id", control.UpdateMemberKey)
		admin.DELETE("/carpool-control/v1/members/:subject/keys/:key_id", control.DeleteMemberKey)
	}
}
