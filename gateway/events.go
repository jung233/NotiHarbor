package gateway

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"message-pusher/model"
)

// ClaimQQBotEvent durably claims one callback event. Call CompleteQQBotEvent after
// all idempotent database side effects are committed, or FailQQBotEvent to release it.
func (s *Service) ClaimQQBotEvent(ctx context.Context, channelID, eventID string) (string, bool, error) {
	if channelID == "" || eventID == "" {
		return "", false, ErrInvalidRequest
	}
	now := time.Now().UTC()
	token := newID()
	claimed := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var event model.GatewayEvent
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("channel_id = ? AND event_id = ?", channelID, eventID).First(&event).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			event = model.GatewayEvent{
				Id: newID(), ChannelID: channelID, EventID: eventID,
				Status: "processing", AttemptCount: 1, LeaseOwner: s.opts.ConsumerName,
				LeaseToken: token, LeaseExpiresAt: timePtr(now.Add(s.opts.LeaseDuration)),
				ReceivedAt: now, CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.Create(&event).Error; err != nil {
				return err
			}
			claimed = true
			return nil
		}
		if err != nil {
			return err
		}
		if event.Status == "processed" || (event.Status == "processing" && event.LeaseExpiresAt != nil && event.LeaseExpiresAt.After(now)) {
			return nil
		}
		result := tx.Model(&event).Updates(map[string]interface{}{
			"status": "processing", "attempt_count": gorm.Expr("attempt_count + 1"),
			"lease_owner": s.opts.ConsumerName, "lease_token": token,
			"lease_expires_at": now.Add(s.opts.LeaseDuration), "last_error": "", "updated_at": now,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 1 {
			claimed = true
		}
		return nil
	})
	return token, claimed, err
}

func (s *Service) CompleteQQBotEvent(ctx context.Context, channelID, eventID, token string) error {
	now := time.Now().UTC()
	result := s.db.WithContext(ctx).Model(&model.GatewayEvent{}).
		Where("channel_id = ? AND event_id = ? AND status = ? AND lease_token = ?", channelID, eventID, "processing", token).
		Updates(map[string]interface{}{"status": "processed", "processed_at": now, "lease_owner": "", "lease_token": "", "lease_expires_at": nil, "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrLeaseLost
	}
	return nil
}

func (s *Service) FailQQBotEvent(ctx context.Context, channelID, eventID, token string, cause error) error {
	now := time.Now().UTC()
	result := s.db.WithContext(ctx).Model(&model.GatewayEvent{}).
		Where("channel_id = ? AND event_id = ? AND status = ? AND lease_token = ?", channelID, eventID, "processing", token).
		Updates(map[string]interface{}{"status": "pending", "lease_owner": "", "lease_token": "", "lease_expires_at": nil, "last_error": safeErrorMessage(cause), "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrLeaseLost
	}
	return nil
}

// ProcessQQBotEvent commits the callback's database effects and deduplication marker atomically.
func (s *Service) ProcessQQBotEvent(ctx context.Context, channelID, eventID string, apply func(*gorm.DB) error) (bool, error) {
	if channelID == "" || eventID == "" || apply == nil {
		return false, ErrInvalidRequest
	}
	now := time.Now().UTC()
	processed := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var event model.GatewayEvent
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("channel_id = ? AND event_id = ?", channelID, eventID).First(&event).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			event = model.GatewayEvent{Id: newID(), ChannelID: channelID, EventID: eventID, Status: "processing", AttemptCount: 1, ReceivedAt: now, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&event).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if event.Status == "processed" {
			return nil
		} else if event.Status == "processing" && event.LeaseExpiresAt != nil && event.LeaseExpiresAt.After(now) {
			return nil
		} else {
			if err := tx.Model(&event).Updates(map[string]interface{}{"status": "processing", "attempt_count": gorm.Expr("attempt_count + 1"), "updated_at": now}).Error; err != nil {
				return err
			}
		}
		if err := apply(tx); err != nil {
			return err
		}
		if err := tx.Model(&event).Updates(map[string]interface{}{"status": "processed", "processed_at": now, "lease_owner": "", "lease_token": "", "lease_expires_at": nil, "updated_at": now}).Error; err != nil {
			return err
		}
		processed = true
		return nil
	})
	return processed, err
}

func timePtr(value time.Time) *time.Time { return &value }
