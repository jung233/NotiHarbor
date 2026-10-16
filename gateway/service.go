package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"message-pusher/common"
	"message-pusher/model"
)

func newID() string { return uuid.New().String() }

func (s *Service) Validate() error {
	if s == nil || s.db == nil {
		return errors.New("gateway requires a database")
	}
	if s.sender == nil {
		return errors.New("gateway requires a sender")
	}
	if s.rdb == nil {
		return errors.New("gateway requires Redis Streams")
	}
	return nil
}

func requestHash(req EnqueueRequest) (string, error) {
	metadata := req.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	if !json.Valid(metadata) {
		return "", fmt.Errorf("%w: metadata must be valid JSON", ErrInvalidRequest)
	}
	metadata, err := canonicalJSON(metadata)
	if err != nil {
		return "", err
	}
	canonical := struct {
		Title          string          `json:"title"`
		Content        string          `json:"content"`
		Priority       string          `json:"priority"`
		DeliveryPolicy string          `json:"delivery_policy"`
		Metadata       json.RawMessage `json:"metadata"`
		Deliveries     []DeliverySpec  `json:"deliveries"`
	}{req.Title, req.Content, req.Priority, req.DeliveryPolicy, metadata, req.Deliveries}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (s *Service) Enqueue(ctx context.Context, req EnqueueRequest) (*model.Notification, bool, error) {
	if s == nil || s.db == nil {
		return nil, false, errors.New("gateway requires a database")
	}
	req, hash, err := prepareEnqueueRequest(req)
	if err != nil {
		return nil, false, err
	}

	var notification model.Notification
	var idempotent bool
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		created, hit, err := s.enqueueTx(tx, req, hash)
		if err != nil {
			return err
		}
		notification = *created
		idempotent = hit
		return nil
	})
	if err != nil {
		// A concurrent insert may have won the unique (source_id,idempotency_key) race.
		var existing model.Notification
		lookupErr := s.db.WithContext(ctx).Where("source_id = ? AND idempotency_key = ?", req.SourceID, req.IdempotencyKey).First(&existing).Error
		if lookupErr == nil {
			if existing.RequestHash != hash {
				return nil, false, ErrIdempotencyConflict
			}
			return &existing, true, nil
		}
		return nil, false, err
	}
	return &notification, idempotent, nil
}

func prepareEnqueueRequest(req EnqueueRequest) (EnqueueRequest, string, error) {
	req.SourceID = strings.TrimSpace(req.SourceID)
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.SourceID == "" {
		return req, "", fmt.Errorf("%w: source_id is required", ErrInvalidRequest)
	}
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = "auto:" + uuid.NewString()
	}
	if len(req.IdempotencyKey) > 191 {
		return req, "", fmt.Errorf("%w: idempotency_key exceeds 191 characters", ErrInvalidRequest)
	}
	if len(req.Deliveries) == 0 {
		return req, "", fmt.Errorf("%w: at least one delivery is required", ErrInvalidRequest)
	}
	if req.DeliveryPolicy == "" {
		req.DeliveryPolicy = PolicyBroadcast
	}
	if req.DeliveryPolicy != PolicyBroadcast && req.DeliveryPolicy != PolicyFallback {
		return req, "", fmt.Errorf("%w: unsupported delivery policy", ErrInvalidRequest)
	}
	if len(req.Metadata) == 0 {
		req.Metadata = json.RawMessage(`{}`)
	}
	var metadataObject map[string]json.RawMessage
	if err := json.Unmarshal(req.Metadata, &metadataObject); err != nil || metadataObject == nil {
		return req, "", fmt.Errorf("%w: metadata must be a JSON object", ErrInvalidRequest)
	}
	hash, err := requestHash(req)
	return req, hash, err
}

func canonicalJSON(raw []byte) (json.RawMessage, error) {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var value interface{}
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("%w: metadata must be valid JSON", ErrInvalidRequest)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func (s *Service) enqueueTx(tx *gorm.DB, req EnqueueRequest, hash string) (*model.Notification, bool, error) {
	var existing model.Notification
	findErr := tx.Where("source_id = ? AND idempotency_key = ?", req.SourceID, req.IdempotencyKey).First(&existing).Error
	if findErr == nil {
		if existing.RequestHash != hash {
			return nil, false, ErrIdempotencyConflict
		}
		return &existing, true, nil
	}
	if !errors.Is(findErr, gorm.ErrRecordNotFound) {
		return nil, false, findErr
	}
	var source model.Source
	if err := tx.Where("id = ? AND status = ?", req.SourceID, model.GatewayStatusActive).First(&source).Error; err != nil {
		return nil, false, err
	}
	allowed, err := decodeAllowedChannels(source.AllowedChannelIDsJSON)
	if err != nil {
		return nil, false, err
	}
	if len(allowed) == 0 {
		return nil, false, fmt.Errorf("%w: source has no permitted channels", ErrInvalidRequest)
	}
	now := time.Now().UTC()
	var qqMetadata struct {
		EventID      string    `json:"event_id"`
		MessageID    string    `json:"msg_id"`
		Mode         string    `json:"mode"`
		IsWakeup     bool      `json:"is_wakeup"`
		ReceivedAt   time.Time `json:"received_at"`
		TargetType   string    `json:"target_type"`
		TargetOpenID string    `json:"target_openid"`
		MessageType  string    `json:"message_type"`
	}
	if err := json.Unmarshal(req.Metadata, &qqMetadata); err != nil {
		return nil, false, fmt.Errorf("%w: metadata must be a JSON object", ErrInvalidRequest)
	}
	notification := &model.Notification{
		Id: newID(), SourceID: req.SourceID, IdempotencyKey: req.IdempotencyKey,
		RequestHash: hash, RequestID: req.RequestID, Title: req.Title,
		Content: req.Content, MetadataJSON: string(req.Metadata), Priority: req.Priority,
		DeliveryPolicy: req.DeliveryPolicy, Status: model.NotificationPendingDispatch,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := tx.Create(notification).Error; err != nil {
		return nil, false, err
	}
	deliveries := append([]DeliverySpec(nil), req.Deliveries...)
	if req.DeliveryPolicy == PolicyFallback {
		for i := range deliveries {
			if deliveries[i].RouteOrder == 0 && i > 0 {
				deliveries[i].RouteOrder = i
			}
		}
		sort.SliceStable(deliveries, func(i, j int) bool { return deliveries[i].RouteOrder < deliveries[j].RouteOrder })
	}
	for i, spec := range deliveries {
		channelID := strings.TrimSpace(spec.ChannelID)
		if channelID == "" || !allowed[channelID] {
			return nil, false, fmt.Errorf("%w: source is not permitted to use channel %q", ErrInvalidRequest, channelID)
		}
		var configured model.Channel
		if err := tx.Where("id = ? AND user_id = ? AND status = ?", channelID, source.OwnerUserID, common.ChannelStatusEnabled).First(&configured).Error; err != nil {
			return nil, false, fmt.Errorf("%w: selected channel is unavailable", ErrInvalidRequest)
		}
		channelQQMetadata := qqMetadata
		if len(spec.DeliveryMetadata) > 0 {
			if err := json.Unmarshal(spec.DeliveryMetadata, &channelQQMetadata); err != nil {
				return nil, false, fmt.Errorf("%w: delivery metadata must be a JSON object", ErrInvalidRequest)
			}
		}
		targetValue := spec.Target
		selectedTargetType := ""
		if spec.TargetID != "" {
			var target model.Target
			if err := tx.Where("id = ? AND source_id = ? AND channel_id = ? AND status = ?", spec.TargetID, req.SourceID, channelID, model.GatewayStatusActive).First(&target).Error; err != nil {
				return nil, false, fmt.Errorf("target %q: %w", spec.TargetID, err)
			}
			targetValue = target.ExternalID
			selectedTargetType = target.TargetType
		}
		deliveryMessageType := spec.MessageType
		deliveryMetadataJSON := ""
		messageSequence := 0
		if configured.Type == model.TypeQQBotV2 {
			requestedMode := strings.ToLower(strings.TrimSpace(channelQQMetadata.Mode))
			if channelQQMetadata.IsWakeup {
				if requestedMode != "" && requestedMode != "wakeup" {
					return nil, false, fmt.Errorf("%w: is_wakeup conflicts with delivery mode", ErrInvalidRequest)
				}
				requestedMode = "wakeup"
			}
			mode := effectiveQQBotMode(configured, requestedMode)
			if mode != "active" && mode != "passive" && mode != "wakeup" {
				return nil, false, fmt.Errorf("%w: unsupported QQBot delivery mode", ErrInvalidRequest)
			}
			if mode == "wakeup" {
				channelQQMetadata.IsWakeup = true
			} else {
				channelQQMetadata.IsWakeup = false
			}
			metadata := DeliveryMetadata{Mode: mode, IsWakeUp: channelQQMetadata.IsWakeup, MessageType: spec.MessageType}
			if metadata.MessageType == "" {
				metadata.MessageType = channelQQMetadata.MessageType
			}
			deliveryMessageType = metadata.MessageType
			if mode == "passive" || mode == "wakeup" {
				metadata.EventID = channelQQMetadata.EventID
				metadata.MessageID = channelQQMetadata.MessageID
				metadata.TargetType = channelQQMetadata.TargetType
				metadata.TargetOpenID = channelQQMetadata.TargetOpenID
				metadata.ReceivedAt = channelQQMetadata.ReceivedAt
				metadata, err = resolveVerifiedQQBotMetadata(tx, channelID, metadata, now, mode == "passive")
				if err != nil {
					return nil, false, err
				}
				if mode == "wakeup" && metadata.TargetType != "user" {
					return nil, false, fmt.Errorf("%w: QQBot wakeup is available only for C2C users", ErrInvalidRequest)
				}
				if targetValue != "" && targetValue != metadata.TargetOpenID {
					return nil, false, fmt.Errorf("%w: selected target does not match the verified QQBot interaction", ErrInvalidRequest)
				}
				if spec.TargetID != "" {
					var target model.Target
					if err := tx.Where("id = ?", spec.TargetID).First(&target).Error; err != nil {
						return nil, false, err
					}
					if target.TargetType != metadata.TargetType {
						return nil, false, fmt.Errorf("%w: target type does not match the verified QQBot interaction", ErrInvalidRequest)
					}
				}
				targetValue = metadata.TargetOpenID
				if mode == "passive" {
					trackingID := metadata.MessageID
					withMessageID := trackingID != ""
					if trackingID == "" {
						trackingID = "event:" + metadata.EventID
					}
					sequence, err := allocateQQBotMessageSequence(tx, configured.AppId, trackingID, metadata.TargetType, now)
					if err != nil {
						return nil, false, err
					}
					if withMessageID {
						messageSequence = sequence
					}
				}
			} else {
				metadata.TargetType = selectedTargetType
				if metadata.TargetType == "" {
					metadata.TargetType = channelQQMetadata.TargetType
				}
				if metadata.TargetType == "" {
					metadata.TargetType = qqBotConfiguredTargetType(configured)
				}
				metadata.TargetOpenID = targetValue
				if metadata.TargetOpenID == "" {
					metadata.TargetOpenID = channelQQMetadata.TargetOpenID
				}
			}
			encoded, err := mergeQQBotMetadata(req.Metadata, metadata)
			if err != nil {
				return nil, false, err
			}
			deliveryMetadataJSON = string(encoded)
		}
		status := model.DeliveryPendingDispatch
		if req.DeliveryPolicy == PolicyFallback && i > 0 {
			status = model.DeliveryHeld
		}
		maxAttempts := spec.MaxAttempts
		if maxAttempts <= 0 {
			maxAttempts = s.opts.MaxAttempts
		}
		if maxAttempts < 1 || maxAttempts > 20 {
			return nil, false, fmt.Errorf("%w: max_attempts must be between 1 and 20", ErrInvalidRequest)
		}
		if len(spec.RetryBackoffSeconds) > 6 {
			return nil, false, fmt.Errorf("%w: retry_backoff_seconds accepts at most 6 entries", ErrInvalidRequest)
		}
		for _, seconds := range spec.RetryBackoffSeconds {
			if seconds < 1 || seconds > 86400 {
				return nil, false, fmt.Errorf("%w: retry backoff must be between 1 and 86400 seconds", ErrInvalidRequest)
			}
		}
		backoffJSON, err := json.Marshal(spec.RetryBackoffSeconds)
		if err != nil {
			return nil, false, err
		}
		delivery := model.Delivery{
			Id: newID(), NotificationID: notification.Id, ChannelID: channelID,
			ChannelName: configured.Name, ChannelType: configured.Type,
			MessageType: deliveryMessageType, MessageSequence: messageSequence, TargetID: spec.TargetID, Target: targetValue,
			MetadataJSON: deliveryMetadataJSON,
			RouteOrder:   spec.RouteOrder, Status: status, MaxAttempts: maxAttempts,
			RetryBackoffJSON: string(backoffJSON), DispatchGeneration: 1,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&delivery).Error; err != nil {
			return nil, false, err
		}
		if status == model.DeliveryPendingDispatch {
			if err := createOutbox(tx, &delivery, now); err != nil {
				return nil, false, err
			}
		}
	}
	return notification, false, nil
}

// SubmitLegacyTx stores the legacy message and its gateway notification through
// the caller's transaction. The caller must insert/update the legacy row before
// invoking this method and commit only after it succeeds.
func (s *Service) SubmitLegacyTx(ctx context.Context, tx *gorm.DB, legacy *model.Message, req EnqueueRequest) (*model.Notification, error) {
	if tx == nil || legacy == nil || legacy.Id == 0 {
		return nil, fmt.Errorf("%w: transaction and persisted legacy message are required", ErrInvalidRequest)
	}
	var metadata map[string]interface{}
	if len(req.Metadata) == 0 {
		metadata = make(map[string]interface{})
	} else if err := json.Unmarshal(req.Metadata, &metadata); err != nil || metadata == nil {
		return nil, fmt.Errorf("%w: legacy metadata must be a JSON object", ErrInvalidRequest)
	}
	metadata["legacy_message_id"] = legacy.Id
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	req.Metadata = encoded
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = "legacy:" + fmt.Sprint(legacy.Id)
	}
	req, hash, err := prepareEnqueueRequest(req)
	if err != nil {
		return nil, err
	}
	notification, _, err := s.enqueueTx(tx.WithContext(ctx), req, hash)
	return notification, err
}

func decodeAllowedChannels(encoded string) (map[string]bool, error) {
	var channels []string
	if encoded == "" {
		return map[string]bool{}, nil
	}
	if err := json.Unmarshal([]byte(encoded), &channels); err != nil {
		return nil, fmt.Errorf("decode source channel permissions: %w", err)
	}
	allowed := make(map[string]bool, len(channels))
	for _, channelID := range channels {
		channelID = strings.TrimSpace(channelID)
		if channelID != "" {
			allowed[channelID] = true
		}
	}
	return allowed, nil
}

func effectiveQQBotMode(channel model.Channel, requested string) string {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested != "" {
		return requested
	}
	var config struct {
		DeliveryMode string `json:"delivery_mode"`
	}
	if json.Unmarshal([]byte(channel.Other), &config) == nil {
		if mode := strings.ToLower(strings.TrimSpace(config.DeliveryMode)); mode != "" {
			return mode
		}
	}
	return "active"
}

func qqBotConfiguredTargetType(channel model.Channel) string {
	var config struct {
		TargetType string `json:"target_type"`
	}
	if json.Unmarshal([]byte(channel.Other), &config) == nil {
		if targetType := strings.ToLower(strings.TrimSpace(config.TargetType)); targetType == "user" || targetType == "group" {
			return targetType
		}
	}
	return "user"
}

func resolveVerifiedQQBotMetadata(tx *gorm.DB, channelID string, metadata DeliveryMetadata, now time.Time, passive bool) (DeliveryMetadata, error) {
	if metadata.EventID == "" && metadata.MessageID == "" {
		return metadata, fmt.Errorf("%w: QQBot delivery requires a verified callback interaction", ErrInvalidRequest)
	}
	query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("channel_id = ? AND status = ?", channelID, "processed")
	if metadata.EventID != "" {
		query = query.Where("event_id = ?", metadata.EventID)
	}
	if metadata.MessageID != "" {
		query = query.Where("message_id = ?", metadata.MessageID)
	}
	var event model.GatewayEvent
	if err := query.Order("received_at DESC").First(&event).Error; err != nil {
		return metadata, fmt.Errorf("%w: QQBot callback interaction is not verified", ErrInvalidRequest)
	}
	if event.TargetOpenID == "" || (event.TargetType != "user" && event.TargetType != "group") {
		return metadata, fmt.Errorf("%w: QQBot callback interaction has no reply target", ErrInvalidRequest)
	}
	if !validQQBotReplyEvent(event.TargetType, event.EventType, metadata.Mode) {
		return metadata, fmt.Errorf("%w: QQBot callback event does not permit this reply mode", ErrInvalidRequest)
	}
	if metadata.TargetType != "" && metadata.TargetType != event.TargetType {
		return metadata, fmt.Errorf("%w: QQBot callback target type does not match", ErrInvalidRequest)
	}
	if metadata.TargetOpenID != "" && metadata.TargetOpenID != event.TargetOpenID {
		return metadata, fmt.Errorf("%w: QQBot callback target does not match", ErrInvalidRequest)
	}
	metadata.EventID = event.EventID
	metadata.MessageID = event.MessageID
	metadata.TargetType = event.TargetType
	metadata.TargetOpenID = event.TargetOpenID
	metadata.ReceivedAt = event.ReceivedAt
	metadata.MessageSeq = 0
	if metadata.Mode == "wakeup" || !qqBotEventSupportsMessageID(event.TargetType, event.EventType) {
		metadata.MessageID = ""
	}
	if passive {
		window := 5 * time.Minute
		if now.Sub(event.ReceivedAt) > window || event.ReceivedAt.Sub(now) > 5*time.Minute {
			return metadata, fmt.Errorf("%w: QQBot passive reply window has expired", ErrInvalidRequest)
		}
	}
	if metadata.Mode == "wakeup" {
		if now.Sub(event.ReceivedAt) > 30*24*time.Hour || event.ReceivedAt.Sub(now) > 5*time.Minute {
			return metadata, fmt.Errorf("%w: QQBot wakeup interaction has expired", ErrInvalidRequest)
		}
	}
	return metadata, nil
}

func validQQBotReplyEvent(targetType, eventType, mode string) bool {
	eventType = strings.ToUpper(strings.TrimSpace(eventType))
	if mode == "wakeup" {
		return targetType == "user" && (eventType == "C2C_MESSAGE_CREATE" || eventType == "INTERACTION_CREATE")
	}
	if targetType == "user" {
		if eventType == "C2C_MESSAGE_CREATE" {
			return true
		}
		return eventType == "INTERACTION_CREATE" || eventType == "C2C_INTERACTION_CREATE" || eventType == "C2C_MSG_RECEIVE" || eventType == "FRIEND_ADD"
	}
	if targetType == "group" {
		if eventType == "GROUP_AT_MESSAGE_CREATE" || eventType == "GROUP_MESSAGE_CREATE" {
			return true
		}
		return eventType == "INTERACTION_CREATE" || eventType == "GROUP_ADD_ROBOT" || eventType == "GROUP_MSG_RECEIVE"
	}
	return false
}

func qqBotEventSupportsMessageID(targetType, eventType string) bool {
	eventType = strings.ToUpper(strings.TrimSpace(eventType))
	if targetType == "user" {
		return eventType == "C2C_MESSAGE_CREATE"
	}
	if targetType == "group" {
		return eventType == "GROUP_AT_MESSAGE_CREATE" || eventType == "GROUP_MESSAGE_CREATE"
	}
	return false
}

func mergeQQBotMetadata(raw json.RawMessage, metadata DeliveryMetadata) (json.RawMessage, error) {
	values := make(map[string]json.RawMessage)
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &values); err != nil || values == nil {
			return nil, fmt.Errorf("%w: metadata must be a JSON object", ErrInvalidRequest)
		}
	}
	encodedValues := map[string]interface{}{"mode": metadata.Mode, "is_wakeup": metadata.IsWakeUp}
	optionalValues := map[string]string{
		"event_id": metadata.EventID, "msg_id": metadata.MessageID,
		"message_type": metadata.MessageType, "target_type": metadata.TargetType,
		"target_openid": metadata.TargetOpenID,
	}
	for key, value := range optionalValues {
		if value == "" {
			delete(values, key)
			continue
		}
		encodedValues[key] = value
	}
	if metadata.ReceivedAt.IsZero() {
		delete(values, "received_at")
	} else {
		encodedValues["received_at"] = metadata.ReceivedAt
	}
	for key, value := range encodedValues {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		values[key] = encoded
	}
	delete(values, "msg_seq") // Callback envelope sequence is not the outbound reply sequence.
	encoded, err := json.Marshal(values)
	return encoded, err
}

func allocateQQBotMessageSequence(tx *gorm.DB, botAppID, messageID, targetType string, now time.Time) (int, error) {
	botAppID = strings.TrimSpace(botAppID)
	messageID = strings.TrimSpace(messageID)
	targetType = strings.TrimSpace(targetType)
	if botAppID == "" || len(botAppID) > 191 || messageID == "" || len(messageID) > 191 || (targetType != "user" && targetType != "group") {
		return 0, fmt.Errorf("%w: QQBot app_id, message_id and target type are required and must fit storage limits", ErrInvalidRequest)
	}
	seed := model.QQBotMessageSequence{BotAppID: botAppID, MessageID: messageID, TargetType: targetType, CreatedAt: now, UpdatedAt: now}
	if err := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "bot_app_id"}, {Name: "message_id"}, {Name: "target_type"}},
		DoNothing: true,
	}).Create(&seed).Error; err != nil {
		return 0, err
	}
	var sequence model.QQBotMessageSequence
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("bot_app_id = ? AND message_id = ? AND target_type = ?", botAppID, messageID, targetType).First(&sequence).Error; err != nil {
		return 0, err
	}
	maxSequence := 4
	if targetType == "group" {
		maxSequence = 5
	}
	if sequence.LastSequence >= maxSequence {
		return 0, fmt.Errorf("%w: QQBot passive message reply limit has been reached", ErrInvalidRequest)
	}
	next := sequence.LastSequence + 1
	result := tx.Model(&sequence).Where("last_sequence = ?", sequence.LastSequence).
		Updates(map[string]interface{}{"last_sequence": next, "updated_at": now})
	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected != 1 {
		return 0, ErrLeaseLost
	}
	return next, nil
}

func createOutbox(tx *gorm.DB, delivery *model.Delivery, at time.Time) error {
	return tx.Create(&model.OutboxEvent{
		Id: newID(), DeliveryID: delivery.Id, Generation: delivery.DispatchGeneration,
		EventType: "delivery.dispatch", Status: model.OutboxPending,
		AvailableAt: at, CreatedAt: at, UpdatedAt: at,
	}).Error
}

func (s *Service) GetNotification(ctx context.Context, sourceID, id string) (*model.Notification, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("gateway requires a database")
	}
	query := s.db.WithContext(ctx).Where("id = ?", id)
	if sourceID != "" {
		query = query.Where("source_id = ?", sourceID)
	}
	var notification model.Notification
	if err := query.First(&notification).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &notification, nil
}

func (s *Service) ListDeliveries(ctx context.Context, notificationID string) ([]model.Delivery, error) {
	var deliveries []model.Delivery
	err := s.db.WithContext(ctx).Where("notification_id = ?", notificationID).Order("route_order ASC, created_at ASC").Find(&deliveries).Error
	return deliveries, err
}

func (s *Service) ListNotifications(ctx context.Context, filter NotificationFilter) ([]model.Notification, int64, error) {
	if s == nil || s.db == nil {
		return nil, 0, errors.New("gateway requires a database")
	}
	if filter.IncludeLegacyArchive {
		return s.listNotificationsWithLegacyArchive(ctx, filter)
	}
	query := s.db.WithContext(ctx).Model(&model.Notification{})
	if filter.SourceID != "" {
		query = query.Where("gateway_notifications.source_id = ?", filter.SourceID)
	}
	if filter.OwnerUserID != 0 {
		query = query.Joins("JOIN gateway_sources ON gateway_sources.id = gateway_notifications.source_id").Where("gateway_sources.owner_user_id = ?", filter.OwnerUserID)
	}
	if filter.From != nil {
		query = query.Where("gateway_notifications.created_at >= ?", *filter.From)
	}
	if filter.To != nil {
		query = query.Where("gateway_notifications.created_at <= ?", *filter.To)
	}
	if filter.Status != "" {
		query = query.Where("gateway_notifications.status = ?", filter.Status)
	}
	if filter.ChannelID != "" {
		query = query.Joins("JOIN gateway_deliveries ON gateway_deliveries.notification_id = gateway_notifications.id").Where("gateway_deliveries.channel_id = ?", filter.ChannelID)
	}
	if filter.Error != "" {
		query = query.Joins("JOIN gateway_deliveries AS error_deliveries ON error_deliveries.notification_id = gateway_notifications.id").Where("(error_deliveries.last_error_category = ? OR error_deliveries.last_error_code = ?)", filter.Error, filter.Error)
	}
	if filter.Search != "" {
		needle := "%" + strings.TrimSpace(filter.Search) + "%"
		query = query.Where("(gateway_notifications.title LIKE ? OR gateway_notifications.content LIKE ?)", needle, needle)
	}
	var total int64
	if err := query.Distinct("gateway_notifications.id").Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page, size := normalizedPage(filter.Page, filter.PageSize)
	var notifications []model.Notification
	err := query.Select("gateway_notifications.*").Distinct().Order("gateway_notifications.created_at DESC").Limit(size).Offset((page - 1) * size).Find(&notifications).Error
	return notifications, total, err
}

func (s *Service) ListAttempts(ctx context.Context, sourceID, deliveryID string, page, pageSize int) ([]model.DeliveryAttempt, int64, error) {
	return s.listAttempts(ctx, 0, sourceID, deliveryID, page, pageSize)
}

func (s *Service) ListAttemptsForOwner(ctx context.Context, ownerUserID int, sourceID, deliveryID string, page, pageSize int) ([]model.DeliveryAttempt, int64, error) {
	return s.listAttempts(ctx, ownerUserID, sourceID, deliveryID, page, pageSize)
}

func (s *Service) listAttempts(ctx context.Context, ownerUserID int, sourceID, deliveryID string, page, pageSize int) ([]model.DeliveryAttempt, int64, error) {
	query := s.db.WithContext(ctx).Table("gateway_delivery_attempts").Model(&model.DeliveryAttempt{}).
		Joins("JOIN gateway_deliveries ON gateway_deliveries.id = gateway_delivery_attempts.delivery_id").
		Joins("JOIN gateway_notifications ON gateway_notifications.id = gateway_deliveries.notification_id")
	if sourceID != "" {
		query = query.Where("gateway_notifications.source_id = ?", sourceID)
	}
	if deliveryID != "" {
		query = query.Where("gateway_delivery_attempts.delivery_id = ?", deliveryID)
	}
	if ownerUserID != 0 {
		query = query.Joins("JOIN gateway_sources ON gateway_sources.id = gateway_notifications.source_id").Where("gateway_sources.owner_user_id = ?", ownerUserID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page, pageSize = normalizedPage(page, pageSize)
	var attempts []model.DeliveryAttempt
	err := query.Order("gateway_delivery_attempts.started_at DESC").Limit(pageSize).Offset((page - 1) * pageSize).Find(&attempts).Error
	return attempts, total, err
}

func normalizedPage(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}
	return page, pageSize
}

func (s *Service) GetSource(ctx context.Context, ownerUserID int, sourceID string) (*model.Source, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("gateway requires a database")
	}
	query := s.db.WithContext(ctx).Where("id = ?", sourceID)
	if ownerUserID != 0 {
		query = query.Where("owner_user_id = ?", ownerUserID)
	}
	var source model.Source
	if err := query.First(&source).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &source, nil
}

func (s *Service) SourceAllowsChannel(ctx context.Context, sourceID, channelID string) (bool, error) {
	var source model.Source
	if err := s.db.WithContext(ctx).Select("id", "status", "allowed_channel_ids_json").Where("id = ?", sourceID).First(&source).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	if source.Status != model.GatewayStatusActive {
		return false, nil
	}
	allowed, err := decodeAllowedChannels(source.AllowedChannelIDsJSON)
	return allowed[channelID], err
}
