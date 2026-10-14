package controller

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"message-pusher/common"
	"message-pusher/gateway"
	"message-pusher/model"
)

func GatewayHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func GatewayReady(c *gin.Context) {
	service := gateway.DefaultService()
	if service == nil || model.DB == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready", "message": "gateway dependencies are unavailable"})
		return
	}
	if err := service.Validate(); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready", "message": "gateway dependencies are unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	sqlDB, err := model.DB.DB()
	if err != nil || sqlDB.PingContext(ctx) != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready", "message": "database is unavailable"})
		return
	}
	if common.RDB == nil || common.RDB.Ping(ctx).Err() != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready", "message": "Redis is unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}
