package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"message-pusher/channel"
	"message-pusher/common"
	"message-pusher/gateway"
	"message-pusher/model"
)

// GatewayQQBotCallback returns the verified callback handler for the QQBot v2
// channels configured in this installation.
func GatewayQQBotCallback() http.Handler {
	return channel.NewQQBotWebhookHandler(resolveGatewayQQBotChannel, persistGatewayQQBotEvent)
}

func resolveGatewayQQBotChannel(ctx context.Context, appID string) (*model.Channel, error) {
	if model.DB == nil {
		return nil, errors.New("database is unavailable")
	}
	query := model.DB.WithContext(ctx).Where("app_id = ? AND type = ? AND status = ?", appID, model.TypeQQBotV2, common.ChannelStatusEnabled)
	var count int64
	if err := query.Model(&model.Channel{}).Count(&count).Error; err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, nil
	}
	var configured model.Channel
	if err := query.First(&configured).Error; err != nil {
		return nil, err
	}
	return &configured, nil
}

func persistGatewayQQBotEvent(ctx context.Context, configured *model.Channel, event channel.QQBotCallbackEvent) error {
	service := gateway.DefaultService()
	if service == nil || configured == nil || configured.Id <= 0 {
		return errors.New("gateway callback dependencies are unavailable")
	}
	channelID := strconv.Itoa(configured.Id)
	if !gatewayBoundedString(event.EventID, 191) || len(event.EventType) > 64 || len(event.MessageID) > 191 || len(event.TargetType) > 64 || len(event.TargetOpenID) > 191 {
		return errors.New("QQBot event fields exceed the storage limits")
	}
	if event.ReceivedAt.IsZero() {
		event.ReceivedAt = time.Now().UTC()
	} else {
		event.ReceivedAt = event.ReceivedAt.UTC()
	}
	if event.TargetOpenID != "" && event.TargetType != "user" && event.TargetType != "group" {
		return errors.New("QQBot event has an unsupported target type")
	}
	_, err := service.ProcessQQBotEvent(ctx, channelID, event.EventID, func(tx *gorm.DB) error {
		if err := tx.Model(&model.GatewayEvent{}).
			Where("channel_id = ? AND event_id = ?", channelID, event.EventID).
			Updates(map[string]interface{}{
				"event_type": event.EventType, "message_id": event.MessageID,
				"message_sequence": event.MessageSequence, "target_type": event.TargetType,
				"target_open_id": event.TargetOpenID, "received_at": event.ReceivedAt,
			}).Error; err != nil {
			return err
		}
		if event.TargetOpenID == "" {
			return nil
		}
		if !event.TargetInteraction && !gatewayQQBotDiscoveryEvent(event.EventType) && !event.TargetRemoved {
			return nil
		}

		var sources []model.Source
		if err := tx.Where("owner_user_id = ?", configured.UserId).Find(&sources).Error; err != nil {
			return err
		}
		for _, source := range sources {
			targetIdentity := tx.Model(&model.Target{}).
				Where("source_id = ? AND channel_id = ? AND target_type = ? AND external_id = ?", source.Id, channelID, event.TargetType, event.TargetOpenID)
			if event.TargetRemoved {
				if err := targetIdentity.Updates(map[string]interface{}{
					"status": model.GatewayStatusDisabled, "updated_at": event.ReceivedAt,
				}).Error; err != nil {
					return err
				}
				continue
			}
			if source.Status != model.GatewayStatusActive {
				continue
			}
			var allowedChannelIDs []string
			if err := json.Unmarshal([]byte(source.AllowedChannelIDsJSON), &allowedChannelIDs); err != nil {
				return fmt.Errorf("decode source channel permissions: %w", err)
			}
			allowed := false
			for _, allowedID := range allowedChannelIDs {
				if strings.TrimSpace(allowedID) == channelID {
					allowed = true
					break
				}
			}
			if !allowed {
				continue
			}

			var lastInteractionAt *time.Time
			if event.TargetInteraction {
				lastInteractionAt = &event.ReceivedAt
			}
			target := model.Target{
				Id: uuid.NewString(), SourceID: source.Id, ChannelID: channelID,
				TargetType: event.TargetType, ExternalID: event.TargetOpenID,
				Name: event.TargetOpenID, Source: "qqbot_callback",
				Status: model.GatewayStatusActive, LastInteractionAt: lastInteractionAt,
				CreatedAt: event.ReceivedAt, UpdatedAt: event.ReceivedAt,
			}
			onConflict := clause.OnConflict{
				Columns:   []clause.Column{{Name: "source_id"}, {Name: "channel_id"}, {Name: "target_type"}, {Name: "external_id"}},
				DoNothing: true,
			}
			if event.TargetInteraction {
				// Refresh the interaction time while preserving a target that an
				// administrator manually disabled.
				onConflict.DoNothing = false
				onConflict.DoUpdates = clause.Assignments(map[string]interface{}{
					"last_interaction_at": event.ReceivedAt,
					"updated_at":          event.ReceivedAt,
				})
			}
			if err := tx.Clauses(onConflict).Create(&target).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

func gatewayQQBotDiscoveryEvent(eventType string) bool {
	switch strings.ToUpper(strings.TrimSpace(eventType)) {
	case "FRIEND_ADD", "GROUP_ADD_ROBOT":
		return true
	default:
		return false
	}
}
