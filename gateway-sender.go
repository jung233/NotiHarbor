package main

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"strconv"
	"time"

	"message-pusher/channel"
	"message-pusher/common"
	"message-pusher/gateway"
	"message-pusher/model"
)

func gatewaySender(ctx context.Context, notification *model.Notification, delivery *model.Delivery, target *model.Target) (gateway.SendResult, error) {
	var source model.Source
	if err := model.DB.WithContext(ctx).Where("id = ? AND status = ?", notification.SourceID, model.GatewayStatusActive).First(&source).Error; err != nil {
		return gateway.SendResult{}, gatewayPreflightError(err, "source unavailable")
	}
	user, err := model.GetUserById(source.OwnerUserID, false)
	if err != nil {
		return gateway.SendResult{}, gatewayPreflightError(err, "source owner unavailable")
	}
	if user.Status != common.UserStatusEnabled {
		return gateway.SendResult{}, common.NewDeliveryError("permission", "owner_disabled", false, false, 0, 0, errors.New("source owner unavailable"))
	}
	id, err := strconv.Atoi(delivery.ChannelID)
	if err != nil {
		return gateway.SendResult{}, common.NewDeliveryError("parameters", "invalid_channel", false, false, 0, 0, errors.New("invalid channel identifier"))
	}
	configured, err := model.GetChannelById(id, user.Id, true)
	if err != nil {
		return gateway.SendResult{}, gatewayPreflightError(err, "channel unavailable")
	}
	if configured.Status != common.ChannelStatusEnabled {
		return gateway.SendResult{}, common.NewDeliveryError("permission", "channel_disabled", false, false, 0, 0, errors.New("channel unavailable"))
	}
	message := &model.Message{UserId: user.Id, Title: notification.Title, Content: notification.Content, Channel: configured.Name, Timestamp: time.Now().Unix(), To: delivery.Target, Link: notification.Id}
	if target != nil {
		message.To = target.ExternalID
	}
	metadata, _ := gateway.DeliveryMetadataFromContext(ctx)
	if target != nil {
		metadata.TargetType, metadata.TargetOpenID = target.TargetType, target.ExternalID
	}
	if metadata.TargetOpenID == "" {
		metadata.TargetOpenID = message.To
	}
	ctx = channel.WithQQBotDeliveryMetadata(ctx, channel.QQBotDeliveryMetadata{TargetType: metadata.TargetType, TargetOpenID: metadata.TargetOpenID, Mode: metadata.Mode, MessageType: metadata.MessageType, EventID: metadata.EventID, MessageID: metadata.MessageID, MessageSeq: int(metadata.MessageSeq), IsWakeup: metadata.IsWakeUp, ReceivedAt: metadata.ReceivedAt})
	var legacy struct {
		MessageID   int    `json:"legacy_message_id"`
		URL         string `json:"url"`
		Description string `json:"description"`
		RenderMode  string `json:"render_mode"`
	}
	if notification.MetadataJSON != "" {
		if err := json.Unmarshal([]byte(notification.MetadataJSON), &legacy); err != nil {
			return gateway.SendResult{}, common.NewDeliveryError("parameters", "invalid_metadata", false, false, 0, 0, errors.New("invalid persisted metadata"))
		}
	}
	if legacy.URL != "" {
		message.URL = legacy.URL
	}
	if legacy.Description != "" {
		message.Description = legacy.Description
	}
	message.RenderMode = legacy.RenderMode
	if message.URL == "" {
		message.URL = common.ServerAddress + "/gateway/history?notification_id=" + notification.Id
	}
	result, err := channel.Dispatch(ctx, message, user, configured)
	if legacy.MessageID > 0 {
		status := common.MessageSendStatusSent
		if err != nil {
			status = common.MessageSendStatusFailed
			if typed, ok := common.AsDeliveryError(err); ok {
				if typed.DeliveryUnknown() {
					status = common.MessageSendStatusUnknown
				} else if typed.Retryable() && delivery.AttemptCount < delivery.MaxAttempts {
					status = common.MessageSendStatusAsyncPending
				}
			}
		}
		updateCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		updateErr := model.DB.WithContext(updateCtx).Model(&model.Message{}).Where("id = ? AND user_id = ?", legacy.MessageID, user.Id).Update("status", status).Error
		cancel()
		if updateErr != nil {
			common.SysError("legacy status update failed; notification_id=" + notification.Id)
		}
	}
	return gateway.SendResult{HTTPStatus: result.HTTPStatus, ProviderCode: result.ProviderCode, ProviderMessageID: result.ProviderMessageID}, err
}

func gatewayPreflightError(err error, message string) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return common.NewDeliveryError("configuration", "resource_missing", false, false, 0, 0, errors.New(message))
	}
	return common.NewDeliveryError("database", "preflight_database", true, false, 0, 0, errors.New(message))
}
