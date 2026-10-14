package router

import (
	"github.com/gin-gonic/gin"
	"message-pusher/controller"
	"message-pusher/gateway"
	"message-pusher/middleware"
)

// SetGatewayRouter registers the v1 upstream API and the session-authenticated
// management API. API keys are accepted only by the upstream routes.
func SetGatewayRouter(engine *gin.Engine) {
	engine.GET("/healthz", controller.GatewayHealth)
	engine.GET("/readyz", controller.GatewayReady)
	engine.POST("/api/v1/qqbot/callback", gin.WrapH(controller.GatewayQQBotCallback()))

	v1 := engine.Group("/api/v1")
	{
		v1.POST("/notifications", middleware.GatewayAPIKeyAuth(gateway.ScopeNotificationsWrite), controller.CreateGatewayNotification)
		v1.GET("/notifications", middleware.GatewayAPIKeyAuth(gateway.ScopeNotificationsRead), controller.ListGatewayNotifications)
		v1.GET("/notifications/:id", middleware.GatewayAPIKeyAuth(gateway.ScopeNotificationsRead), controller.GetGatewayNotification)
		v1.GET("/notifications/:id/deliveries", middleware.GatewayAPIKeyAuth(gateway.ScopeNotificationsRead), controller.ListGatewayNotificationDeliveries)
		v1.GET("/notifications/:id/deliveries/:deliveryID/attempts", middleware.GatewayAPIKeyAuth(gateway.ScopeNotificationsRead), controller.ListGatewayDeliveryAttempts)
		v1.POST("/notifications/batch/retry", middleware.GatewayAPIKeyAuth("notifications:retry"), controller.BatchRetryGatewayNotifications)
		v1.POST("/notifications/:id/retry", middleware.GatewayAPIKeyAuth("notifications:retry"), controller.RetryGatewayNotification)
		v1.POST("/notifications/:id/cancel", middleware.GatewayAPIKeyAuth("notifications:cancel"), controller.CancelGatewayNotification)
		v1.POST("/notifications/:id/resend", middleware.GatewayAPIKeyAuth(gateway.ScopeNotificationsWrite), controller.ResendGatewayNotification)
		v1.GET("/channels", middleware.GatewayAPIKeyAuth("channels:read"), controller.ListGatewayChannels)
		v1.POST("/channels/:channelID/test", middleware.GatewayAPIKeyAuth("channels:test"), controller.TestGatewayChannel)
		v1.GET("/logs", middleware.GatewayAPIKeyAuth("logs:read"), controller.ListGatewayLogs)
	}

	admin := v1.Group("/admin")
	admin.Use(middleware.UserAuth())
	{
		admin.GET("/dashboard", controller.GetGatewayDashboard)
		admin.GET("/notifications", controller.ListGatewayNotifications)
		admin.GET("/notifications/:id", controller.GetGatewayNotification)
		admin.GET("/notifications/:id/deliveries", controller.ListGatewayNotificationDeliveries)
		admin.GET("/notifications/:id/deliveries/:deliveryID/attempts", controller.ListGatewayDeliveryAttempts)
		admin.POST("/notifications/batch/retry", controller.BatchRetryGatewayNotifications)
		admin.POST("/notifications/:id/retry", controller.RetryGatewayNotification)
		admin.POST("/notifications/:id/cancel", controller.CancelGatewayNotification)
		admin.POST("/notifications/:id/resend", controller.ResendGatewayNotification)
		admin.GET("/channels", controller.ListGatewayChannels)
		admin.POST("/channels/:channelID/test", controller.TestGatewayChannel)
		admin.GET("/logs", controller.ListGatewayLogs)
		admin.GET("/audit-logs", controller.ListGatewayAuditLogs)

		admin.GET("/sources", controller.ListGatewaySources)
		admin.POST("/sources", controller.CreateGatewaySource)
		admin.GET("/sources/:sourceID", controller.GetGatewaySource)
		admin.PATCH("/sources/:sourceID", controller.UpdateGatewaySource)
		admin.DELETE("/sources/:sourceID", controller.DeleteGatewaySource)
		admin.GET("/sources/:sourceID/api-keys", controller.ListGatewayAPIKeys)
		admin.POST("/sources/:sourceID/api-keys", controller.CreateGatewayAPIKey)
		admin.POST("/sources/:sourceID/api-keys/:keyID/rotate", controller.RotateGatewayAPIKey)
		admin.DELETE("/sources/:sourceID/api-keys/:keyID", controller.RevokeGatewayAPIKey)
		admin.GET("/sources/:sourceID/targets", controller.ListGatewayTargets)
		admin.POST("/sources/:sourceID/targets", controller.CreateGatewayTarget)
		admin.PATCH("/sources/:sourceID/targets/:targetID", controller.UpdateGatewayTarget)
		admin.DELETE("/sources/:sourceID/targets/:targetID", controller.DeleteGatewayTarget)
	}
}
