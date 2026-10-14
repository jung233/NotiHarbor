package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"message-pusher/common"
	"message-pusher/gateway"
	"message-pusher/model"
)

type gatewayNotificationRequest struct {
	Title          string            `json:"title"`
	Content        string            `json:"content"`
	Priority       string            `json:"priority,omitempty"`
	MessageType    string            `json:"message_type,omitempty"`
	Channels       []string          `json:"channels"`
	Targets        map[string]string `json:"targets,omitempty"`
	DeliveryPolicy string            `json:"delivery_policy,omitempty"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
	Metadata       json.RawMessage   `json:"metadata,omitempty"`
}

type gatewayBatchRetryRequest struct {
	IDs []string `json:"ids"`
}

func CreateGatewayNotification(c *gin.Context) {
	service, ok := gatewayService(c)
	if !ok {
		return
	}
	source, ok := gatewayKeySource(c)
	if !ok {
		gatewayFailure(c, http.StatusUnauthorized, "API key source is unavailable")
		return
	}

	var request gatewayNotificationRequest
	if err := bindGatewayJSON(c, &request); err != nil {
		gatewayFailure(c, http.StatusBadRequest, "invalid JSON request")
		return
	}
	if !gatewayBoundedString(request.Title, 512) || strings.TrimSpace(request.Content) == "" || len(request.Content) > gatewayRequestLimit {
		gatewayFailure(c, http.StatusBadRequest, "title and content are required and must be within the request size limit")
		return
	}
	if len(request.Channels) == 0 || len(request.Channels) > 16 {
		gatewayFailure(c, http.StatusBadRequest, "one to sixteen channels must be selected")
		return
	}
	if request.Priority == "" {
		request.Priority = "normal"
	}
	if !gatewayBoundedString(request.Priority, 32) {
		gatewayFailure(c, http.StatusBadRequest, "invalid priority")
		return
	}
	if request.DeliveryPolicy == "" {
		request.DeliveryPolicy = gateway.PolicyBroadcast
	}
	if request.DeliveryPolicy != gateway.PolicyBroadcast && request.DeliveryPolicy != gateway.PolicyFallback {
		gatewayFailure(c, http.StatusBadRequest, "delivery_policy must be broadcast or fallback")
		return
	}
	if request.MessageType != "" && request.MessageType != "text" && request.MessageType != "markdown" {
		gatewayFailure(c, http.StatusBadRequest, "message_type must be text or markdown")
		return
	}
	if request.IdempotencyKey == "" {
		request.IdempotencyKey = strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	}
	if request.IdempotencyKey == "" {
		request.IdempotencyKey = gatewayGeneratedIdempotencyKey()
	}
	if !gatewayBoundedString(request.IdempotencyKey, 191) {
		gatewayFailure(c, http.StatusBadRequest, "idempotency_key must not exceed 191 characters")
		return
	}

	deliveries, err := gatewayDeliverySpecs(c.Request.Context(), service, source, request.Channels, request.Targets, request.MessageType)
	if err != nil {
		if errors.Is(err, errGatewayDataUnavailable) {
			gatewayFailure(c, http.StatusServiceUnavailable, "channel configuration could not be verified")
			return
		}
		gatewayFailure(c, http.StatusBadRequest, err.Error())
		return
	}
	notification, duplicate, err := service.Enqueue(c.Request.Context(), gateway.EnqueueRequest{
		SourceID:       source.Id,
		IdempotencyKey: request.IdempotencyKey,
		RequestID:      gatewayRequestID(c),
		Title:          strings.TrimSpace(request.Title),
		Content:        request.Content,
		Priority:       request.Priority,
		DeliveryPolicy: request.DeliveryPolicy,
		Metadata:       request.Metadata,
		Deliveries:     deliveries,
	})
	if errors.Is(err, gateway.ErrIdempotencyConflict) {
		gatewayFailure(c, http.StatusConflict, "idempotency key was already used with a different request")
		return
	}
	if err != nil {
		if errors.Is(err, gateway.ErrInvalidRequest) {
			gatewayFailure(c, http.StatusBadRequest, "notification request is invalid for this source")
		} else {
			gatewayFailure(c, http.StatusInternalServerError, "notification could not be persisted")
		}
		return
	}
	status := http.StatusAccepted
	if duplicate {
		status = http.StatusOK
	}
	gatewaySuccess(c, status, gin.H{"notification_id": notification.Id, "status": notification.Status, "idempotency_key": request.IdempotencyKey, "idempotent_replay": duplicate})
}

func gatewayDeliverySpecs(ctx context.Context, service *gateway.Service, source *model.Source, channelRefs []string, requestedTargets map[string]string, messageType string) ([]gateway.DeliverySpec, error) {
	specs := make([]gateway.DeliverySpec, 0, len(channelRefs))
	targets, err := service.ListTargets(ctx, source.OwnerUserID, source.Id)
	if err != nil {
		return nil, errGatewayDataUnavailable
	}
	seen := make(map[string]struct{}, len(channelRefs))
	for index, reference := range channelRefs {
		reference = strings.TrimSpace(reference)
		if reference == "" || len(reference) > 191 {
			return nil, errors.New("channel references must be non-empty and at most 191 characters")
		}
		channel, err := gatewayChannelForOwner(ctx, reference, source.OwnerUserID)
		if errors.Is(err, errGatewayDataUnavailable) {
			return nil, errGatewayDataUnavailable
		}
		if err != nil || channel.Status != common.ChannelStatusEnabled {
			return nil, errors.New("a selected channel is unavailable")
		}
		channelID := strconv.Itoa(channel.Id)
		if _, exists := seen[channelID]; exists {
			return nil, errors.New("a channel may only be selected once")
		}
		seen[channelID] = struct{}{}
		allowed, err := service.SourceAllowsChannel(ctx, source.Id, channelID)
		if err != nil {
			return nil, errGatewayDataUnavailable
		}
		if !allowed {
			return nil, errors.New("the source is not authorized to use a selected channel")
		}

		targetRef := strings.TrimSpace(requestedTargets[reference])
		if targetRef == "" {
			targetRef = strings.TrimSpace(requestedTargets[channelID])
		}
		var selected *model.Target
		for i := range targets {
			target := &targets[i]
			if target.ChannelID != channelID || target.Status != model.GatewayStatusActive {
				continue
			}
			if targetRef != "" && target.Id == targetRef {
				selected = target
				break
			}
			if targetRef == "" && target.IsDefault {
				selected = target
			}
		}
		if targetRef != "" && selected == nil {
			return nil, errors.New("a selected target is unavailable for its channel")
		}
		spec := gateway.DeliverySpec{
			ChannelID:   channelID,
			ChannelName: channel.Name,
			ChannelType: channel.Type,
			MessageType: messageType,
			RouteOrder:  index,
		}
		if selected != nil {
			spec.TargetID = selected.Id
			spec.Target = selected.ExternalID
		} else if strings.TrimSpace(channel.AccountId) != "" {
			// Preserve configured legacy channel defaults when no gateway target exists.
			spec.Target = channel.AccountId
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

func gatewayChannelForOwner(ctx context.Context, reference string, ownerUserID int) (*model.Channel, error) {
	query := func() *gorm.DB {
		return model.DB.WithContext(ctx).Select("id", "type", "user_id", "name", "description", "status", "app_id", "account_id", "other").Where("user_id = ?", ownerUserID)
	}
	if id, err := strconv.Atoi(reference); err == nil && id > 0 {
		var channel model.Channel
		lookupErr := query().Where("id = ?", id).First(&channel).Error
		if lookupErr == nil {
			return &channel, nil
		}
		if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			return nil, errGatewayDataUnavailable
		}
	}
	var channel model.Channel
	err := query().Where("name = ?", reference).First(&channel).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, gateway.ErrNotFound
	}
	if err != nil {
		return nil, errGatewayDataUnavailable
	}
	return &channel, nil
}

func ListGatewayNotifications(c *gin.Context) {
	service, ok := gatewayService(c)
	if !ok {
		return
	}
	from, err := parseGatewayTime(c.Query("from"))
	if err != nil {
		gatewayFailure(c, http.StatusBadRequest, err.Error())
		return
	}
	to, err := parseGatewayTime(c.Query("to"))
	if err != nil {
		gatewayFailure(c, http.StatusBadRequest, err.Error())
		return
	}
	page, pageSize := gatewayPage(c)
	sourceID, ownerUserID := gatewayNotificationScope(c)
	if keySource, isKey := gatewayKeySource(c); isKey {
		if requested := strings.TrimSpace(c.Query("source_id")); requested != "" && requested != keySource.Id {
			gatewayFailure(c, http.StatusForbidden, "API keys can only access their own source")
			return
		}
	}
	for name, value := range map[string]string{
		"channel_id": c.Query("channel_id"), "status": c.Query("status"),
		"error": c.Query("error"), "search": c.Query("search"),
	} {
		if len(value) > 191 {
			gatewayFailure(c, http.StatusBadRequest, name+" filter may not exceed 191 characters")
			return
		}
	}
	if sourceID != "" && ownerUserID != 0 {
		if _, err := service.GetSource(c.Request.Context(), ownerUserID, sourceID); err != nil {
			gatewayLookupFailure(c, err, "source not found")
			return
		}
	}
	filter := gateway.NotificationFilter{
		From:        from,
		To:          to,
		SourceID:    sourceID,
		ChannelID:   strings.TrimSpace(c.Query("channel_id")),
		Status:      strings.TrimSpace(c.Query("status")),
		Error:       strings.TrimSpace(c.Query("error")),
		Search:      strings.TrimSpace(c.Query("search")),
		OwnerUserID: ownerUserID,
		Page:        page,
		PageSize:    pageSize,
	}
	items, total, err := service.ListNotifications(c.Request.Context(), filter)
	if err != nil {
		gatewayFailure(c, http.StatusInternalServerError, "notifications could not be loaded")
		return
	}
	gatewaySuccess(c, http.StatusOK, gatewayPageData(items, total, page, pageSize))
}

func GetGatewayNotification(c *gin.Context) {
	service, ok := gatewayService(c)
	if !ok {
		return
	}
	notification, err := gatewayGetScopedNotification(c, service, gatewayParseIDParam(c, "id"))
	if err != nil {
		gatewayLookupFailure(c, err, "notification not found")
		return
	}
	deliveries, err := service.ListDeliveries(c.Request.Context(), notification.Id)
	if err != nil {
		gatewayFailure(c, http.StatusInternalServerError, "notification deliveries could not be loaded")
		return
	}
	gatewaySuccess(c, http.StatusOK, gin.H{"notification": notification, "deliveries": deliveries})
}

func ListGatewayNotificationDeliveries(c *gin.Context) {
	service, ok := gatewayService(c)
	if !ok {
		return
	}
	notification, err := gatewayGetScopedNotification(c, service, gatewayParseIDParam(c, "id"))
	if err != nil {
		gatewayLookupFailure(c, err, "notification not found")
		return
	}
	deliveries, err := service.ListDeliveries(c.Request.Context(), notification.Id)
	if err != nil {
		gatewayFailure(c, http.StatusInternalServerError, "notification deliveries could not be loaded")
		return
	}
	gatewaySuccess(c, http.StatusOK, deliveries)
}

func ListGatewayDeliveryAttempts(c *gin.Context) {
	service, ok := gatewayService(c)
	if !ok {
		return
	}
	notification, err := gatewayGetScopedNotification(c, service, gatewayParseIDParam(c, "id"))
	if err != nil {
		gatewayLookupFailure(c, err, "notification not found")
		return
	}
	deliveries, err := service.ListDeliveries(c.Request.Context(), notification.Id)
	if err != nil {
		gatewayFailure(c, http.StatusInternalServerError, "notification deliveries could not be loaded")
		return
	}
	deliveryID := gatewayParseIDParam(c, "deliveryID")
	deliveryBelongsToNotification := false
	for _, delivery := range deliveries {
		if delivery.Id == deliveryID {
			deliveryBelongsToNotification = true
			break
		}
	}
	if !deliveryBelongsToNotification {
		gatewayFailure(c, http.StatusNotFound, "delivery not found")
		return
	}
	page, pageSize := gatewayPage(c)
	items, total, err := service.ListAttempts(c.Request.Context(), notification.SourceID, deliveryID, page, pageSize)
	if err != nil {
		gatewayFailure(c, http.StatusInternalServerError, "delivery attempts could not be loaded")
		return
	}
	gatewaySuccess(c, http.StatusOK, gatewayPageData(items, total, page, pageSize))
}

func RetryGatewayNotification(c *gin.Context) {
	updateGatewayNotification(c, "retry")
}

func CancelGatewayNotification(c *gin.Context) {
	updateGatewayNotification(c, "cancel")
}

func ResendGatewayNotification(c *gin.Context) {
	service, ok := gatewayService(c)
	if !ok {
		return
	}
	notification, err := gatewayGetScopedNotification(c, service, gatewayParseIDParam(c, "id"))
	if err != nil {
		gatewayLookupFailure(c, err, "notification not found")
		return
	}
	resend, err := service.Resend(c.Request.Context(), notification.SourceID, notification.Id, gatewayActorID(c))
	if err != nil {
		gatewayMutationFailure(c, err, http.StatusConflict, "notification cannot be resent in its current state", "notification not found", "notification could not be resent")
		return
	}
	gatewaySuccess(c, http.StatusAccepted, gin.H{"notification_id": resend.Id, "status": resend.Status})
}

func updateGatewayNotification(c *gin.Context, operation string) {
	service, ok := gatewayService(c)
	if !ok {
		return
	}
	notification, err := gatewayGetScopedNotification(c, service, gatewayParseIDParam(c, "id"))
	if err != nil {
		gatewayLookupFailure(c, err, "notification not found")
		return
	}
	if operation == "retry" {
		err = service.RetryNotification(c.Request.Context(), notification.SourceID, notification.Id, gatewayActorID(c))
	} else {
		err = service.CancelNotification(c.Request.Context(), notification.SourceID, notification.Id, gatewayActorID(c))
	}
	if err != nil {
		gatewayMutationFailure(c, err, http.StatusConflict, "notification cannot be "+operation+"ed in its current state", "notification not found", "notification could not be updated")
		return
	}
	gatewaySuccess(c, http.StatusOK, gin.H{"notification_id": notification.Id, "status": operation + "_requested"})
}

func BatchRetryGatewayNotifications(c *gin.Context) {
	service, ok := gatewayService(c)
	if !ok {
		return
	}
	var request gatewayBatchRetryRequest
	if err := bindGatewayJSON(c, &request); err != nil || len(request.IDs) == 0 || len(request.IDs) > 100 {
		gatewayFailure(c, http.StatusBadRequest, "ids must contain between 1 and 100 notification IDs")
		return
	}
	results := make([]gin.H, 0, len(request.IDs))
	var succeeded int
	for _, id := range request.IDs {
		notification, err := gatewayGetScopedNotification(c, service, strings.TrimSpace(id))
		if err == nil {
			err = service.RetryNotification(c.Request.Context(), notification.SourceID, notification.Id, gatewayActorID(c))
		}
		if err != nil {
			if !errors.Is(err, gateway.ErrNotFound) && !errors.Is(err, gateway.ErrInvalidRequest) {
				gatewayFailure(c, http.StatusInternalServerError, "notification retry could not be completed")
				return
			}
			results = append(results, gin.H{"notification_id": id, "success": false, "message": "notification could not be retried"})
			continue
		}
		succeeded++
		results = append(results, gin.H{"notification_id": id, "success": true, "status": "retry_requested"})
	}
	status := http.StatusOK
	if succeeded == 0 {
		status = http.StatusConflict
	} else if succeeded != len(request.IDs) {
		status = http.StatusMultiStatus
	}
	gatewaySuccess(c, status, gin.H{"succeeded": succeeded, "failed": len(request.IDs) - succeeded, "results": results})
}

func ListGatewayLogs(c *gin.Context) {
	service, ok := gatewayService(c)
	if !ok {
		return
	}
	sourceID, ownerUserID := gatewayNotificationScope(c)
	if keySource, isKey := gatewayKeySource(c); isKey {
		if requested := strings.TrimSpace(c.Query("source_id")); requested != "" && requested != keySource.Id {
			gatewayFailure(c, http.StatusForbidden, "API keys can only access their own source")
			return
		}
	}
	if ownerUserID != 0 && sourceID == "" {
		gatewayFailure(c, http.StatusBadRequest, "source_id is required for non-admin session log queries")
		return
	}
	if sourceID != "" && ownerUserID != 0 {
		if _, err := service.GetSource(c.Request.Context(), ownerUserID, sourceID); err != nil {
			gatewayLookupFailure(c, err, "source not found")
			return
		}
	}
	page, pageSize := gatewayPage(c)
	filters := []string{
		strings.TrimSpace(c.Query("notification_id")),
		strings.TrimSpace(c.Query("delivery_id")),
		strings.TrimSpace(c.Query("channel_id")),
	}
	for _, value := range filters {
		if len(value) > 191 {
			gatewayFailure(c, http.StatusBadRequest, "log filters may not exceed 191 characters")
			return
		}
	}
	type gatewayLogRow struct {
		ID             string    `json:"id"`
		CreatedAt      time.Time `json:"created_at"`
		NotificationID string    `json:"notification_id"`
		DeliveryID     string    `json:"delivery_id"`
		AttemptID      string    `json:"attempt_id"`
		SourceID       string    `json:"source_id"`
		ChannelID      string    `json:"channel_id"`
		Level          string    `json:"level"`
		Message        string    `json:"message"`
		ErrorCode      string    `json:"error_code"`
		Result         string    `json:"-"`
	}
	query := model.DB.WithContext(c.Request.Context()).Table("gateway_delivery_attempts AS a").
		Joins("JOIN gateway_deliveries AS d ON d.id = a.delivery_id").
		Joins("JOIN gateway_notifications AS n ON n.id = d.notification_id")
	if sourceID != "" {
		query = query.Where("n.source_id = ?", sourceID)
	}
	if ownerUserID != 0 {
		query = query.Joins("JOIN gateway_sources AS s ON s.id = n.source_id").Where("s.owner_user_id = ?", ownerUserID)
	}
	if filters[0] != "" {
		query = query.Where("n.id = ?", filters[0])
	}
	if filters[1] != "" {
		query = query.Where("d.id = ?", filters[1])
	}
	if filters[2] != "" {
		query = query.Where("d.channel_id = ?", filters[2])
	}
	var total int64
	if err := query.Distinct("a.id").Count(&total).Error; err != nil {
		gatewayFailure(c, http.StatusInternalServerError, "delivery logs could not be loaded")
		return
	}
	var items []gatewayLogRow
	if err := query.Select("a.id AS id, a.created_at AS created_at, n.id AS notification_id, d.id AS delivery_id, a.id AS attempt_id, n.source_id AS source_id, d.channel_id AS channel_id, a.result AS result, a.error_message AS message, a.provider_error_code AS error_code").
		Distinct().Order("a.created_at DESC").Limit(pageSize).Offset((page - 1) * pageSize).Scan(&items).Error; err != nil {
		gatewayFailure(c, http.StatusInternalServerError, "delivery logs could not be loaded")
		return
	}
	for i := range items {
		items[i].Level = "info"
		if items[i].Message != "" || items[i].Result == model.DeliveryFailed || items[i].Result == model.DeliveryDeadLetter {
			items[i].Level = "error"
		}
		if items[i].Message == "" {
			items[i].Message = "delivery " + items[i].Result
		}
	}
	gatewaySuccess(c, http.StatusOK, gatewayPageData(items, total, page, pageSize))
}

func gatewayGetScopedNotification(c *gin.Context, service *gateway.Service, id string) (*model.Notification, error) {
	if id == "" {
		return nil, gateway.ErrNotFound
	}
	if source, ok := gatewayKeySource(c); ok {
		return service.GetNotification(c.Request.Context(), source.Id, id)
	}
	ownerID := gatewayOwnerID(c)
	sourceID := strings.TrimSpace(c.Query("source_id"))
	return service.GetNotificationForOwner(c.Request.Context(), ownerID, sourceID, id)
}
