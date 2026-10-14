package middleware

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"message-pusher/common"
)

// Query strings, headers and bodies are intentionally excluded: legacy push
// authentication puts tokens in the query string.
func RequestLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := uuid.NewString()
		c.Set("request_id", requestID)
		c.Header("X-Request-ID", requestID)
		started := time.Now()
		c.Next()
		entry := map[string]interface{}{"type": "http_request", "request_id": requestID, "trace_id": requestID, "method": c.Request.Method, "path": c.Request.URL.Path, "status": c.Writer.Status(), "duration_ms": time.Since(started).Milliseconds(), "time": time.Now().UTC().Format(time.RFC3339Nano)}
		if encoded, err := json.Marshal(entry); err == nil {
			_, _ = fmt.Fprintln(gin.DefaultWriter, string(encoded))
		}
	}
}

func SafeRecovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				common.SysError("request handler panic; request_id=" + c.GetString("request_id"))
				c.AbortWithStatusJSON(500, gin.H{"success": false, "message": "internal server error"})
			}
		}()
		c.Next()
	}
}
