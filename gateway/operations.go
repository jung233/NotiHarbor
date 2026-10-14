package gateway

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"message-pusher/model"
)

func (s *Service) GetNotificationForOwner(ctx context.Context, ownerUserID int, sourceID, id string) (*model.Notification, error) {
	query := s.db.WithContext(ctx).Model(&model.Notification{}).Where("gateway_notifications.id = ?", id)
	if sourceID != "" {
		query = query.Where("gateway_notifications.source_id = ?", sourceID)
	}
	if ownerUserID != 0 {
		query = query.Joins("JOIN gateway_sources ON gateway_sources.id = gateway_notifications.source_id").Where("gateway_sources.owner_user_id = ?", ownerUserID)
	}
	var notification model.Notification
	if err := query.First(&notification).Error; err != nil {
		return nil, notFound(err)
	}
	return &notification, nil
}

func (s *Service) ListDeliveriesForOwner(ctx context.Context, ownerUserID int, sourceID, notificationID string) ([]model.Delivery, error) {
	query := s.db.WithContext(ctx).Model(&model.Delivery{}).
		Joins("JOIN gateway_notifications ON gateway_notifications.id = gateway_deliveries.notification_id")
	if sourceID != "" {
		query = query.Where("gateway_notifications.source_id = ?", sourceID)
	}
	if ownerUserID != 0 {
		query = query.Joins("JOIN gateway_sources ON gateway_sources.id = gateway_notifications.source_id").Where("gateway_sources.owner_user_id = ?", ownerUserID)
	}
	var deliveries []model.Delivery
	err := query.Where("gateway_deliveries.notification_id = ?", notificationID).
		Select("gateway_deliveries.*").Order("gateway_deliveries.route_order ASC, gateway_deliveries.created_at ASC").Find(&deliveries).Error
	return deliveries, err
}

func (s *Service) RetryDelivery(ctx context.Context, sourceID, deliveryID, actorID string) error {
	return s.retryDelivery(ctx, 0, sourceID, deliveryID, actorID)
}

func (s *Service) RetryDeliveryForOwner(ctx context.Context, ownerUserID int, sourceID, deliveryID, actorID string) error {
	return s.retryDelivery(ctx, ownerUserID, sourceID, deliveryID, actorID)
}

func (s *Service) retryDelivery(ctx context.Context, ownerUserID int, sourceID, deliveryID, actorID string) error {
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var identity model.Delivery
		if err := tx.Select("notification_id").Where("id = ?", deliveryID).First(&identity).Error; err != nil {
			return notFound(err)
		}
		notificationQuery := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", identity.NotificationID)
		if sourceID != "" {
			notificationQuery = notificationQuery.Where("source_id = ?", sourceID)
		}
		if ownerUserID != 0 {
			notificationQuery = notificationQuery.Where("source_id IN (SELECT id FROM gateway_sources WHERE owner_user_id = ?)", ownerUserID)
		}
		var notification model.Notification
		if err := notificationQuery.First(&notification).Error; err != nil {
			return notFound(err)
		}
		var delivery model.Delivery
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND notification_id = ?", deliveryID, notification.Id).First(&delivery).Error; err != nil {
			return notFound(err)
		}
		if !retryableManualState(delivery.Status) {
			return fmt.Errorf("%w: delivery in state %q cannot be retried", ErrInvalidRequest, delivery.Status)
		}
		if err := enqueueManualRetry(tx, &delivery, now, s.opts.MaxAttempts); err != nil {
			return err
		}
		if err := s.refreshNotificationStatus(tx, notification.Id, now); err != nil {
			return err
		}
		return writeAuditTx(tx, "user", actorID, notification.SourceID, "delivery.retry", "delivery", deliveryID, "{\"manual\":true}", now)
	})
}

func (s *Service) RetryNotification(ctx context.Context, sourceID, notificationID, actorID string) error {
	return s.retryNotification(ctx, 0, sourceID, notificationID, actorID)
}

func (s *Service) RetryNotificationForOwner(ctx context.Context, ownerUserID int, sourceID, notificationID, actorID string) error {
	return s.retryNotification(ctx, ownerUserID, sourceID, notificationID, actorID)
}

func (s *Service) retryNotification(ctx context.Context, ownerUserID int, sourceID, notificationID, actorID string) error {
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var notification model.Notification
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", notificationID)
		if sourceID != "" {
			query = query.Where("source_id = ?", sourceID)
		}
		if ownerUserID != 0 {
			query = query.Where("source_id IN (SELECT id FROM gateway_sources WHERE owner_user_id = ?)", ownerUserID)
		}
		if err := query.First(&notification).Error; err != nil {
			return notFound(err)
		}
		var deliveries []model.Delivery
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("notification_id = ?", notificationID).Find(&deliveries).Error; err != nil {
			return err
		}
		retried := 0
		for i := range deliveries {
			if !retryableManualState(deliveries[i].Status) {
				continue
			}
			if err := enqueueManualRetry(tx, &deliveries[i], now, s.opts.MaxAttempts); err != nil {
				return err
			}
			retried++
		}
		if retried == 0 {
			return fmt.Errorf("%w: notification has no failed or unknown deliveries to retry", ErrInvalidRequest)
		}
		if err := s.refreshNotificationStatus(tx, notificationID, now); err != nil {
			return err
		}
		return writeAuditTx(tx, "user", actorID, notification.SourceID, "notification.retry", "notification", notificationID, "{\"manual\":true}", now)
	})
}

func (s *Service) CancelNotification(ctx context.Context, sourceID, notificationID, actorID string) error {
	return s.cancelNotification(ctx, 0, sourceID, notificationID, actorID)
}

func (s *Service) CancelNotificationForOwner(ctx context.Context, ownerUserID int, sourceID, notificationID, actorID string) error {
	return s.cancelNotification(ctx, ownerUserID, sourceID, notificationID, actorID)
}

func (s *Service) cancelNotification(ctx context.Context, ownerUserID int, sourceID, notificationID, actorID string) error {
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var notification model.Notification
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", notificationID)
		if sourceID != "" {
			query = query.Where("source_id = ?", sourceID)
		}
		if ownerUserID != 0 {
			query = query.Where("source_id IN (SELECT id FROM gateway_sources WHERE owner_user_id = ?)", ownerUserID)
		}
		if err := query.First(&notification).Error; err != nil {
			return notFound(err)
		}
		var processing int64
		if err := tx.Model(&model.Delivery{}).Where("notification_id = ? AND status = ?", notificationID, model.DeliveryProcessing).Count(&processing).Error; err != nil {
			return err
		}
		if processing > 0 {
			return fmt.Errorf("%w: a delivery is processing", ErrInvalidRequest)
		}
		result := tx.Model(&model.Delivery{}).Where("notification_id = ? AND status IN ?", notificationID,
			[]string{model.DeliveryHeld, model.DeliveryPendingDispatch, model.DeliveryQueued, model.DeliveryRetrying, model.DeliveryUnknown}).
			Updates(map[string]interface{}{"status": model.DeliveryCancelled, "next_retry_at": nil,
				"lease_owner": "", "lease_token": "", "lease_expires_at": nil,
				"completed_at": now, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("%w: notification has no cancellable deliveries", ErrInvalidRequest)
		}
		if err := s.refreshNotificationStatus(tx, notificationID, now); err != nil {
			return err
		}
		return writeAuditTx(tx, "user", actorID, notification.SourceID, "notification.cancel", "notification", notificationID, "{}", now)
	})
}

func enqueueManualRetry(tx *gorm.DB, delivery *model.Delivery, now time.Time, attemptBudget int) error {
	if attemptBudget <= 0 {
		attemptBudget = 5
	}
	delivery.MaxAttempts = delivery.AttemptCount + attemptBudget
	delivery.PreparationFailureCount = 0
	delivery.UnknownRetryCount = 0
	delivery.Status = model.DeliveryPendingDispatch
	delivery.NextRetryAt = nil
	delivery.CompletedAt = nil
	delivery.LastErrorCode = ""
	delivery.LastErrorCategory = ""
	delivery.LastErrorMessage = ""
	delivery.DispatchGeneration++
	delivery.UpdatedAt = now
	if err := tx.Model(delivery).Updates(map[string]interface{}{
		"max_attempts": delivery.MaxAttempts, "preparation_failure_count": 0, "unknown_retry_count": 0,
		"status": delivery.Status, "next_retry_at": nil, "completed_at": nil,
		"last_error_code": "", "last_error_category": "", "last_error_message": "",
		"dispatch_generation": delivery.DispatchGeneration,
		"lease_owner":         "", "lease_token": "", "lease_expires_at": nil, "updated_at": now,
	}).Error; err != nil {
		return err
	}
	return createOutbox(tx, delivery, now)
}

func retryableManualState(status string) bool {
	return status == model.DeliveryFailed || status == model.DeliveryDeadLetter || status == model.DeliveryUnknown
}

func (s *Service) ResendForOwner(ctx context.Context, ownerUserID int, sourceID, notificationID, actorID string) (*model.Notification, error) {
	return s.resendNotification(ctx, ownerUserID, sourceID, notificationID, actorID)
}
