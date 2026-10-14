package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"message-pusher/model"
)

func (s *Service) Run(ctx context.Context) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if err := model.MigrateGateway(s.db); err != nil {
		return err
	}
	if err := s.ensureConsumerGroup(ctx); err != nil {
		return err
	}

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	workerCount := s.opts.Concurrency
	if workerCount < 1 {
		workerCount = 1
	}
	errCh := make(chan error, workerCount+2)
	componentCount := workerCount + 2
	for i := 0; i < workerCount; i++ {
		consumer := fmt.Sprintf("%s-%d", s.opts.ConsumerName, i)
		go func(name string) { errCh <- s.workerSupervisor(workerCtx, name) }(consumer)
	}
	go func() { errCh <- s.outboxLoop(workerCtx) }()
	go func() { errCh <- s.retrySchedulerLoop(workerCtx) }()

	var firstErr error
	for i := 0; i < componentCount; i++ {
		err := <-errCh
		if err != nil && !errors.Is(err, context.Canceled) && firstErr == nil {
			firstErr = err
			cancel()
		}
	}
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

func (s *Service) ensureConsumerGroup(ctx context.Context) error {
	err := s.rdb.XGroupCreateMkStream(ctx, s.opts.StreamName, s.opts.ConsumerGroup, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return err
	}
	return nil
}

func (s *Service) outboxLoop(ctx context.Context) error {
	ticker := time.NewTicker(s.opts.OutboxPoll)
	defer ticker.Stop()
	for {
		if _, err := s.DispatchOutboxBatch(ctx); err != nil && ctx.Err() == nil {
			// A transient Redis or database failure leaves the outbox row recoverable.
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) DispatchOutboxBatch(ctx context.Context) (int, error) {
	if s == nil || s.db == nil || s.rdb == nil {
		return 0, errors.New("outbox requires database and Redis")
	}
	processed := 0
	for processed < int(s.opts.BatchSize) {
		event, claimed, err := s.claimOutbox(ctx)
		if err != nil {
			return processed, err
		}
		if !claimed {
			return processed, nil
		}
		streamID, err := s.rdb.XAdd(ctx, &redis.XAddArgs{
			Stream: s.opts.StreamName,
			Values: map[string]interface{}{
				"delivery_id": event.DeliveryID,
				"outbox_id":   event.Id,
				"generation":  event.Generation,
			},
		}).Result()
		if err != nil {
			if releaseErr := s.releaseOutbox(ctx, event, err); releaseErr != nil {
				return processed, fmt.Errorf("publish outbox: %v; release lease: %w", err, releaseErr)
			}
			return processed, err
		}
		if err := s.markOutboxPublished(ctx, event, streamID); err != nil {
			// If Redis accepted the item but this write failed, the lease expiry permits
			// a duplicate XADD. Delivery leases and status checks make that safe.
			return processed, err
		}
		processed++
	}
	return processed, nil
}

func (s *Service) workerSupervisor(ctx context.Context, consumer string) error {
	for {
		if err := s.workerLoop(ctx, consumer); err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (s *Service) claimOutbox(ctx context.Context) (*model.OutboxEvent, bool, error) {
	now := time.Now().UTC()
	var candidate model.OutboxEvent
	err := s.db.WithContext(ctx).
		Table("gateway_outbox_events").
		Joins("JOIN gateway_deliveries ON gateway_deliveries.id = gateway_outbox_events.delivery_id").
		Joins("JOIN gateway_notifications ON gateway_notifications.id = gateway_deliveries.notification_id").
		Where("(gateway_outbox_events.status = ? AND gateway_outbox_events.available_at <= ?) OR (gateway_outbox_events.status = ? AND gateway_outbox_events.lease_expires_at <= ?)", model.OutboxPending, now, model.OutboxPublishing, now).
		Order("CASE WHEN gateway_notifications.priority IN ('critical', 'urgent') THEN 0 WHEN gateway_notifications.priority = 'high' THEN 1 WHEN gateway_notifications.priority = 'low' THEN 3 ELSE 2 END ASC, gateway_outbox_events.available_at ASC, gateway_outbox_events.created_at ASC").
		Select("gateway_outbox_events.*").First(&candidate).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	token := newID()
	expires := now.Add(s.opts.OutboxLease)
	updated := s.db.WithContext(ctx).Model(&model.OutboxEvent{}).
		Where("id = ? AND ((status = ? AND available_at <= ?) OR (status = ? AND lease_expires_at <= ?))", candidate.Id, model.OutboxPending, now, model.OutboxPublishing, now).
		Updates(map[string]interface{}{
			"status": model.OutboxPublishing, "lease_owner": s.opts.ConsumerName,
			"lease_token": token, "lease_expires_at": expires,
			"attempt_count": gorm.Expr("attempt_count + 1"), "updated_at": now,
		})
	if updated.Error != nil {
		return nil, false, updated.Error
	}
	if updated.RowsAffected == 0 {
		return nil, false, nil
	}
	candidate.LeaseOwner = s.opts.ConsumerName
	candidate.LeaseToken = token
	candidate.LeaseExpiresAt = &expires
	candidate.Status = model.OutboxPublishing
	candidate.AttemptCount++
	return &candidate, true, nil
}

func (s *Service) releaseOutbox(ctx context.Context, event *model.OutboxEvent, publishErr error) error {
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Model(&model.OutboxEvent{}).
		Where("id = ? AND lease_token = ? AND status = ?", event.Id, event.LeaseToken, model.OutboxPublishing).
		Updates(map[string]interface{}{
			"status": model.OutboxPending, "lease_owner": "", "lease_token": "",
			"lease_expires_at": nil, "available_at": now.Add(time.Second),
			"last_error": safeErrorMessage(publishErr), "updated_at": now,
		}).Error
}

func (s *Service) markOutboxPublished(ctx context.Context, event *model.OutboxEvent, streamID string) error {
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var delivery model.Delivery
		if err := tx.Select("id", "notification_id").Where("id = ?", event.DeliveryID).First(&delivery).Error; err != nil {
			return err
		}
		var notification model.Notification
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", delivery.NotificationID).First(&notification).Error; err != nil {
			return err
		}
		var lockedDelivery model.Delivery
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", event.DeliveryID).First(&lockedDelivery).Error; err != nil {
			return err
		}
		var outbox model.OutboxEvent
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", event.Id).First(&outbox).Error; err != nil {
			return err
		}
		result := tx.Model(&model.OutboxEvent{}).
			Where("id = ? AND lease_token = ? AND status = ?", event.Id, event.LeaseToken, model.OutboxPublishing).
			Updates(map[string]interface{}{
				"status": model.OutboxPublished, "stream_message_id": streamID,
				"lease_owner": "", "lease_token": "", "lease_expires_at": nil,
				"last_error": "", "updated_at": now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errors.New("outbox publisher lease was lost after Redis accepted the event")
		}
		if err := tx.Model(&model.Delivery{}).
			Where("id = ? AND status = ?", event.DeliveryID, model.DeliveryPendingDispatch).
			Updates(map[string]interface{}{"status": model.DeliveryQueued, "updated_at": now}).Error; err != nil {
			return err
		}
		return s.refreshNotificationStatus(tx, notification.Id, now)
	})
}

func (s *Service) retrySchedulerLoop(ctx context.Context) error {
	ticker := time.NewTicker(s.opts.RetryPoll)
	defer ticker.Stop()
	for {
		if _, err := s.RequeueDueRetries(ctx, int(s.opts.BatchSize)); err != nil && ctx.Err() == nil {
			// Keep the durable retry state intact and retry after the next poll interval.
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) RequeueDueRetries(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	now := time.Now().UTC()
	var due []model.Delivery
	err := s.db.WithContext(ctx).Where("((status = ? AND next_retry_at <= ?) OR (status = ? AND unknown_retry_count > 0 AND next_retry_at <= ?)) AND (lease_expires_at IS NULL OR lease_expires_at <= ?)", model.DeliveryRetrying, now, model.DeliveryUnknown, now, now).
		Order("next_retry_at ASC").Limit(limit).Find(&due).Error
	if err != nil {
		return 0, err
	}
	count := 0
	for _, candidate := range due {
		err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var notification model.Notification
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", candidate.NotificationID).First(&notification).Error; err != nil {
				return err
			}
			var delivery model.Delivery
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", candidate.Id).First(&delivery).Error; err != nil {
				return err
			}
			result := tx.Model(&model.Delivery{}).
				Where("id = ? AND status = ? AND next_retry_at <= ? AND (lease_expires_at IS NULL OR lease_expires_at <= ?)", candidate.Id, candidate.Status, now, now).
				Updates(map[string]interface{}{
					"status": model.DeliveryPendingDispatch, "next_retry_at": nil,
					"completed_at":        nil,
					"dispatch_generation": gorm.Expr("dispatch_generation + 1"), "updated_at": now,
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return nil
			}
			delivery.Status = model.DeliveryPendingDispatch
			delivery.NextRetryAt = nil
			delivery.CompletedAt = nil
			delivery.DispatchGeneration++
			delivery.UpdatedAt = now
			if err := createOutbox(tx, &delivery, now); err != nil {
				return err
			}
			if err := s.refreshNotificationStatus(tx, delivery.NotificationID, now); err != nil {
				return err
			}
			count++
			return nil
		})
		if err != nil {
			return count, err
		}
	}
	return count, nil
}
