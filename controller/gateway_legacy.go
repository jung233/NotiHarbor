package controller

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"message-pusher/common"
	"message-pusher/gateway"
	"message-pusher/model"
)

func submitLegacyGateway(ctx context.Context, user *model.User, message *model.Message, configured *model.Channel) error {
	service := gateway.DefaultService()
	sourceID := common.LegacySourceID(user.Id)
	channelID := strconv.Itoa(configured.Id)
	metadata, err := json.Marshal(map[string]interface{}{"url": message.URL, "description": message.Description, "render_mode": message.RenderMode})
	if err != nil {
		return err
	}
	return model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		initialAllowed, err := json.Marshal([]string{channelID})
		if err != nil {
			return err
		}
		source := model.Source{Id: sourceID, OwnerUserID: user.Id, Name: "Legacy /push", AllowedChannelIDsJSON: string(initialAllowed), Status: model.GatewayStatusActive, CreatedAt: time.Now().UTC()}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&source).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", sourceID).First(&source).Error; err != nil {
			return err
		}
		{
			var allowed []string
			if err := json.Unmarshal([]byte(source.AllowedChannelIDsJSON), &allowed); err != nil {
				return err
			}
			found := false
			for _, id := range allowed {
				if id == channelID {
					found = true
				}
			}
			if !found {
				allowed = append(allowed, channelID)
				encoded, err := json.Marshal(allowed)
				if err != nil {
					return err
				}
				if err := tx.Model(&source).Update("allowed_channel_ids_json", string(encoded)).Error; err != nil {
					return err
				}
			}
		}
		message.UserId = user.Id
		message.Timestamp = time.Now().Unix()
		message.Status = common.MessageSendStatusAsyncPending
		message.Id = 0 // Resend creates a new legacy message and a new logical notification.
		if err := tx.Create(message).Error; err != nil {
			return err
		}
		var config struct {
			MaxAttempts         int   `json:"max_attempts"`
			RetryBackoffSeconds []int `json:"retry_backoff_seconds"`
		}
		if strings.HasPrefix(strings.TrimSpace(configured.Other), "{") {
			if err := json.Unmarshal([]byte(configured.Other), &config); err != nil {
				return err
			}
		} // Older channels also use non-JSON Other formats.
		_, err = service.SubmitLegacyTx(ctx, tx, message, gateway.EnqueueRequest{SourceID: sourceID, Title: message.Title, Content: message.Content, Priority: "normal", DeliveryPolicy: gateway.PolicyBroadcast, Metadata: metadata, Deliveries: []gateway.DeliverySpec{{ChannelID: channelID, ChannelName: configured.Name, ChannelType: configured.Type, Target: message.To, MaxAttempts: config.MaxAttempts, RetryBackoffSeconds: config.RetryBackoffSeconds}}})
		return err
	})
}
