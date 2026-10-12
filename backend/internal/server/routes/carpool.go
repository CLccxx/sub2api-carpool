package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
)

func registerCarpoolUserRoutes(authenticated *gin.RouterGroup, h *handler.Handlers, heavy ...gin.HandlerFunc) {
	if h == nil || !h.Carpool.Enabled() {
		return
	}
	c := authenticated.Group("/carpool")
	c.GET("/cars", h.Carpool.ListCars)
	c.GET("/cars/:id", h.Carpool.GetCar)
	c.POST("/cars/:id/reservations", h.Carpool.Reserve)
	c.GET("/orders/:id", h.Carpool.GetOrder)
	c.POST("/orders/:id/cancel", h.Carpool.CancelOrder)
	c.GET("/mine", h.Carpool.Mine)
	if h.CarpoolControl.MainEnabled() {
		control := h.CarpoolControl
		c.GET("/cars/:id/console", control.MemberConsole)
		c.GET("/cars/:id/keys", control.MemberKeys)
		c.GET("/cars/:id/subscription", control.CarSubscription)
		c.POST("/cars/:id/keys", control.CreateCarKey)
		c.PATCH("/cars/:id/keys/:key_id", control.UpdateCarKey)
		c.DELETE("/cars/:id/keys/:key_id", control.DeleteCarKey)
		usage := c.Group("/cars/:id/usage", heavy...)
		usage.GET("", control.CarUsage)
		usage.GET("/stats", control.CarUsageStats)
	}
}

func registerCarpoolAdminRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	if h == nil || !h.Carpool.Enabled() {
		return
	}
	c := admin.Group("/carpool")
	c.GET("/cars", h.Carpool.AdminListCars)
	c.POST("/cars", h.Carpool.CreateCar)
	c.GET("/cars/:id", h.Carpool.AdminGetCar)
	c.PATCH("/cars/:id", h.Carpool.UpdateCar)
	c.POST("/cars/:id/publish", h.Carpool.PublishCar)
	c.POST("/cars/:id/cancel", h.Carpool.CancelCar)
	c.GET("/cars/:id/members", h.Carpool.ListMembers)
	c.POST("/cars/:id/members", h.Carpool.EnrollManual)
	c.GET("/orders", h.Carpool.AdminListManualOrders)
	c.GET("/orders/:id", h.Carpool.AdminGetManualOrder)
	c.POST("/orders/:id/withdraw", h.Carpool.WithdrawManual)
	c.POST("/orders/:id/refund", h.Carpool.RefundManual)
	c.POST("/nodes", h.Carpool.RegisterNode)
}
