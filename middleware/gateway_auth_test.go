package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestGatewayAPIKeyAuthRejectsCookieAndQueryCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/protected", GatewayAPIKeyAuth("notifications:read"), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	requests := []*http.Request{
		httptest.NewRequest(http.MethodGet, "/protected", nil),
		httptest.NewRequest(http.MethodGet, "/protected?api_key=mp_test-key", nil),
	}
	requests[0].AddCookie(&http.Cookie{Name: "api_key", Value: "mp_test-key"})

	for _, request := range requests {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("credential outside Authorization header returned status %d; want %d", response.Code, http.StatusUnauthorized)
		}
	}
}
