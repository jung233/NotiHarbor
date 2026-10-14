package middleware

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"message-pusher/gateway"
)

const (
	GatewayAPIKeyContext   = "gateway_api_key"
	GatewaySourceContext   = "gateway_source"
	GatewaySourceIDContext = "gateway_source_id"
)

// GatewayAPIKeyAuth authenticates only an explicit Authorization: Bearer header.
// Gateway credentials are never read from cookies, query parameters, or form data.
func GatewayAPIKeyAuth(requiredScope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		authorization := strings.TrimSpace(c.GetHeader("Authorization"))
		parts := strings.Fields(authorization)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
			gatewayAuthError(c, http.StatusUnauthorized, "missing or invalid bearer credential")
			return
		}

		service := gateway.DefaultService()
		if service == nil {
			gatewayAuthError(c, http.StatusServiceUnavailable, "gateway is unavailable")
			return
		}
		key, source, err := service.LookupAPIKey(c.Request.Context(), parts[1])
		if errors.Is(err, gateway.ErrNotFound) {
			gatewayAuthError(c, http.StatusUnauthorized, "invalid or inactive API key")
			return
		}
		if err != nil {
			gatewayAuthError(c, http.StatusServiceUnavailable, "API key store is unavailable")
			return
		}
		if key == nil || source == nil {
			gatewayAuthError(c, http.StatusUnauthorized, "invalid or inactive API key")
			return
		}
		if requiredScope != "" && !service.HasScope(key, requiredScope) {
			gatewayAuthError(c, http.StatusForbidden, "API key does not grant the required scope")
			return
		}

		limit := source.RateLimitPerMinute
		if limit <= 0 {
			limit = 60
		}
		// The configured limit belongs to the source, so credentials issued
		// for that source share one Redis counter.
		allowed, err := service.AllowAPIKey(c.Request.Context(), "source:"+source.Id, limit, time.Minute)
		if err != nil {
			gatewayAuthError(c, http.StatusServiceUnavailable, "rate limit service is unavailable")
			return
		}
		if !allowed {
			c.Header("Retry-After", "60")
			gatewayAuthError(c, http.StatusTooManyRequests, "source rate limit exceeded")
			return
		}

		c.Set(GatewayAPIKeyContext, key)
		c.Set(GatewaySourceContext, source)
		c.Set(GatewaySourceIDContext, source.Id)
		c.Next()
	}
}

func gatewayAuthError(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, gin.H{
		"success": false,
		"message": message,
	})
}
