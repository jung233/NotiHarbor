package controller

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"message-pusher/gateway"
	"message-pusher/middleware"
	"message-pusher/model"
)

const gatewayRequestLimit = 1 << 20

var errGatewayDataUnavailable = errors.New("gateway data is unavailable")

func gatewayService(c *gin.Context) (*gateway.Service, bool) {
	service := gateway.DefaultService()
	if service == nil {
		gatewayFailure(c, http.StatusServiceUnavailable, "gateway service is unavailable")
		return nil, false
	}
	return service, true
}

func gatewayLookupFailure(c *gin.Context, err error, notFoundMessage string) {
	if errors.Is(err, gateway.ErrNotFound) {
		gatewayFailure(c, http.StatusNotFound, notFoundMessage)
		return
	}
	gatewayFailure(c, http.StatusInternalServerError, "gateway data could not be loaded")
}

func gatewayMutationFailure(c *gin.Context, err error, invalidStatus int, invalidMessage, notFoundMessage, failureMessage string) {
	switch {
	case errors.Is(err, gateway.ErrNotFound):
		gatewayFailure(c, http.StatusNotFound, notFoundMessage)
	case errors.Is(err, gateway.ErrInvalidRequest):
		gatewayFailure(c, invalidStatus, invalidMessage)
	default:
		gatewayFailure(c, http.StatusInternalServerError, failureMessage)
	}
}

func gatewayFailure(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, gin.H{"success": false, "message": message})
}

func gatewaySuccess(c *gin.Context, status int, data interface{}) {
	c.JSON(status, gin.H{"success": true, "message": "", "data": data})
}

func bindGatewayJSON(c *gin.Context, target interface{}) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, gatewayRequestLimit)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra interface{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request must contain a single JSON value")
		}
		return err
	}
	return nil
}

func gatewayPage(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.Query("page"))
	pageSize, _ := strconv.Atoi(c.Query("page_size"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}

func gatewayPageData(items interface{}, total int64, page, pageSize int) gin.H {
	return gin.H{"items": items, "total": total, "page": page, "page_size": pageSize}
}

func gatewayActorID(c *gin.Context) string {
	if key, ok := c.Get(middleware.GatewayAPIKeyContext); ok {
		if apiKey, valid := key.(*model.APIKey); valid && apiKey != nil {
			return "api-key:" + apiKey.Id
		}
	}
	return strconv.Itoa(c.GetInt("id"))
}

func gatewayOwnerID(c *gin.Context) int {
	if c.GetInt("role") >= 10 {
		return 0
	}
	return c.GetInt("id")
}

func gatewayActorType(c *gin.Context) string {
	if _, ok := c.Get(middleware.GatewayAPIKeyContext); ok {
		return "api_key"
	}
	return "user"
}

func gatewayKeySource(c *gin.Context) (*model.Source, bool) {
	value, ok := c.Get(middleware.GatewaySourceContext)
	if !ok {
		return nil, false
	}
	source, ok := value.(*model.Source)
	return source, ok && source != nil
}

func gatewayNotificationScope(c *gin.Context) (sourceID string, ownerUserID int) {
	if source, ok := gatewayKeySource(c); ok {
		return source.Id, 0
	}
	return strings.TrimSpace(c.Query("source_id")), gatewayOwnerID(c)
}

func parseGatewayTime(value string) (*time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, errors.New("time filters must use RFC3339")
	}
	return &parsed, nil
}

func gatewayRequestID(c *gin.Context) string {
	requestID := strings.TrimSpace(c.GetHeader("X-Request-ID"))
	if requestID == "" || len(requestID) > 191 {
		return uuid.NewString()
	}
	return requestID
}

func gatewayGeneratedIdempotencyKey() string {
	return "gateway-auto:" + uuid.NewString()
}

func gatewayBoundedString(value string, maximum int) bool {
	return strings.TrimSpace(value) != "" && len(value) <= maximum
}

func gatewayParseIDParam(c *gin.Context, name string) string {
	return strings.TrimSpace(c.Param(name))
}
