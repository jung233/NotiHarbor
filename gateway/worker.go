package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"message-pusher/common"
	"message-pusher/model"
)

type deliveryFailure struct {
	category      string
	providerCode  string
	httpStatus    int
	retryable     bool
	unknown       bool
	attempted     bool
	retryAfter    time.Duration
	deliveryState string
	attemptResult string
}

type deliveryErrorContract interface {
	error
	DeliveryCategory() string
	Retryable() bool
	DeliveryUnknown() bool
	ProviderCode() string
	RetryAfter() time.Duration
	HTTPStatus() int
}

type deliveryAttemptContract interface {
	SendAttempted() bool
}

func (s *Service) workerLoop(ctx context.Context, consumer string) error {
	if err := s.ensureConsumerGroup(ctx); err != nil {
		return err
	}
	for {
		if err := s.claimPendingMessages(ctx, consumer); err != nil && ctx.Err() == nil {
			return err
		}
		streams, err := s.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group: s.opts.ConsumerGroup, Consumer: consumer,
			Streams: []string{s.opts.StreamName, ">"}, Count: s.opts.BatchSize,
			Block: s.opts.BlockDuration,
		}).Result()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		for _, stream := range streams {
			for _, message := range stream.Messages {
				if err := s.processStreamMessage(ctx, consumer, message); err != nil {
					if errors.Is(err, ErrLeaseLost) || errors.Is(err, ErrLeaseActive) {
						continue
					}
					if ctx.Err() != nil {
						return ctx.Err()
					}
					// Leave failed messages pending. The next pass or another consumer
					// reclaims them after ClaimIdle.
					continue
				}
			}
		}
	}
}

func (s *Service) claimPendingMessages(ctx context.Context, consumer string) error {
	pending, err := s.rdb.XPendingExt(ctx, &redis.XPendingExtArgs{
		Stream: s.opts.StreamName, Group: s.opts.ConsumerGroup,
		Start: "-", End: "+", Count: s.opts.BatchSize,
	}).Result()
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such key") {
			return s.ensureConsumerGroup(ctx)
		}
		return err
	}
	for _, item := range pending {
		if item.Idle < s.opts.ClaimIdle {
			continue
		}
		messages, err := s.rdb.XClaim(ctx, &redis.XClaimArgs{
			Stream: s.opts.StreamName, Group: s.opts.ConsumerGroup,
			Consumer: consumer, MinIdle: s.opts.ClaimIdle,
			Messages: []string{item.ID},
		}).Result()
		if err != nil {
			return err
		}
		for _, message := range messages {
			if err := s.processStreamMessage(ctx, consumer, message); err != nil && !errors.Is(err, ErrLeaseLost) && !errors.Is(err, ErrLeaseActive) {
				return err
			}
		}
	}
	return nil
}

func (s *Service) processStreamMessage(ctx context.Context, consumer string, message redis.XMessage) error {
	deliveryID := fmt.Sprint(message.Values["delivery_id"])
	if deliveryID == "" || deliveryID == "<nil>" {
		return s.ackMessage(ctx, message.ID)
	}
	claim, err := s.claimDelivery(ctx, deliveryID, consumer)
	if err != nil {
		return err
	}
	if claim == nil {
		return s.ackMessage(ctx, message.ID)
	}
	if !claim.shouldSend {
		if claim.deferAck {
			return ErrLeaseActive
		}
		return s.ackMessage(ctx, message.ID)
	}

	sendCtx, cancel := context.WithTimeout(ctx, s.opts.SendTimeout)
	metadata := deliveryMetadata(claim.notification, claim.delivery)
	sendCtx = WithDeliveryMetadata(sendCtx, metadata)
	leaseLost := make(chan struct{}, 1)
	stopLease := make(chan struct{})
	go s.renewLease(sendCtx, claim.delivery.Id, claim.leaseToken, stopLease, cancel, leaseLost)
	result, sendErr := s.sender.Send(sendCtx, claim.notification, claim.delivery, claim.target)
	close(stopLease)
	cancel()
	select {
	case <-leaseLost:
		return ErrLeaseLost
	default:
	}

	finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer finishCancel()
	if err := s.finishDelivery(finishCtx, claim, result, sendErr); err != nil {
		return err
	}
	return s.ackMessage(ctx, message.ID)
}

type deliveryClaim struct {
	delivery     *model.Delivery
	notification *model.Notification
	target       *model.Target
	attempt      *model.DeliveryAttempt
	leaseToken   string
	shouldSend   bool
	deferAck     bool
}

func (s *Service) claimDelivery(ctx context.Context, deliveryID, consumer string) (*deliveryClaim, error) {
	var claim deliveryClaim
	claim.delivery = &model.Delivery{}
	claim.notification = &model.Notification{}
	now := time.Now().UTC()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var identity model.Delivery
		if err := tx.Select("notification_id").Where("id = ?", deliveryID).First(&identity).Error; err != nil {
			return notFound(err)
		}
		var notification model.Notification
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", identity.NotificationID).First(&notification).Error; err != nil {
			return err
		}
		var delivery model.Delivery
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", deliveryID).First(&delivery).Error; err != nil {
			return notFound(err)
		}
		claim.delivery = &delivery
		claim.notification = &notification
		if isTerminal(delivery.Status) || delivery.Status == model.DeliveryHeld {
			return nil
		}
		if delivery.Status == model.DeliveryProcessing && delivery.LeaseExpiresAt != nil && delivery.LeaseExpiresAt.After(now) {
			claim.deferAck = true
			return nil
		}
		if delivery.Status == model.DeliveryProcessing {
			if err := s.recoverLostAttempt(tx, &delivery, now); err != nil {
				return err
			}
			claim.delivery = &delivery
			return s.refreshNotificationStatus(tx, notification.Id, now)
		}
		if delivery.Status != model.DeliveryQueued && delivery.Status != model.DeliveryPendingDispatch && delivery.Status != model.DeliveryRetrying {
			return nil
		}
		if delivery.AttemptCount >= delivery.MaxAttempts {
			delivery.Status = model.DeliveryDeadLetter
			delivery.CompletedAt = &now
			delivery.LastErrorCategory = "max_attempts"
			delivery.LastErrorMessage = "maximum delivery attempts reached"
			if err := tx.Save(&delivery).Error; err != nil {
				return err
			}
			if err := s.activateFallback(tx, &delivery, now); err != nil {
				return err
			}
			return s.refreshNotificationStatus(tx, delivery.NotificationID, now)
		}
		attemptNumber := 1
		var previousAttempt model.DeliveryAttempt
		attemptErr := tx.Select("attempt_number").Where("delivery_id = ?", delivery.Id).Order("attempt_number DESC").First(&previousAttempt).Error
		if attemptErr != nil && !errors.Is(attemptErr, gorm.ErrRecordNotFound) {
			return attemptErr
		}
		if attemptErr == nil {
			attemptNumber = previousAttempt.AttemptNumber + 1
		}

		token := newID()
		expires := now.Add(s.opts.LeaseDuration)
		result := tx.Model(&model.Delivery{}).
			Where("id = ? AND status = ? AND (lease_expires_at IS NULL OR lease_expires_at <= ?)", delivery.Id, delivery.Status, now).
			Updates(map[string]interface{}{
				"status": model.DeliveryProcessing, "lease_owner": consumer,
				"lease_token": token, "lease_expires_at": expires,
				"attempt_count": gorm.Expr("attempt_count + 1"), "updated_at": now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		delivery.AttemptCount++
		delivery.Status = model.DeliveryProcessing
		delivery.LeaseOwner = consumer
		delivery.LeaseToken = token
		delivery.LeaseExpiresAt = &expires
		delivery.UpdatedAt = now
		attempt := model.DeliveryAttempt{
			Id: newID(), DeliveryID: delivery.Id, AttemptNumber: attemptNumber,
			AttemptKind: "send", StartedAt: now, Result: "processing", CreatedAt: now,
		}
		if err := tx.Create(&attempt).Error; err != nil {
			return err
		}
		claim.delivery = &delivery
		claim.attempt = &attempt
		claim.leaseToken = token
		claim.shouldSend = true
		if delivery.TargetID != "" {
			var target model.Target
			if err := tx.Where("id = ? AND source_id = ?", delivery.TargetID, notification.SourceID).First(&target).Error; err != nil {
				return err
			}
			claim.target = &target
		}
		return tx.Model(&notification).Updates(map[string]interface{}{"status": model.NotificationProcessing, "updated_at": now}).Error
	})
	if err != nil {
		return nil, err
	}
	if !claim.shouldSend {
		return &claim, nil
	}
	return &claim, nil
}

func (s *Service) recoverLostAttempt(tx *gorm.DB, delivery *model.Delivery, now time.Time) error {
	var attempt model.DeliveryAttempt
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("delivery_id = ? AND result = ?", delivery.Id, "processing").Order("attempt_number DESC").First(&attempt).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			delivery.Status = model.DeliveryUnknown
			delivery.LastErrorCategory = "worker_lost"
			delivery.LastErrorMessage = "worker lease expired during a send attempt"
			delivery.LeaseToken = ""
			delivery.LeaseOwner = ""
			delivery.LeaseExpiresAt = nil
			return tx.Save(delivery).Error
		}
		return err
	}
	finished := now
	attempt.Result = model.DeliveryUnknown
	attempt.ErrorCategory = "worker_lost"
	attempt.ErrorMessage = "worker lease expired during a send attempt; platform acceptance is unknown"
	attempt.FinishedAt = &finished
	attempt.DurationMS = finished.Sub(attempt.StartedAt).Milliseconds()
	if err := tx.Model(&attempt).Updates(map[string]interface{}{
		"result": attempt.Result, "error_category": attempt.ErrorCategory,
		"error_message": attempt.ErrorMessage, "finished_at": finished,
		"duration_ms": attempt.DurationMS,
	}).Error; err != nil {
		return err
	}
	delivery.Status = model.DeliveryUnknown
	delivery.LastErrorCategory = attempt.ErrorCategory
	delivery.LastErrorMessage = attempt.ErrorMessage
	delivery.LeaseToken = ""
	delivery.LeaseOwner = ""
	delivery.LeaseExpiresAt = nil
	delivery.CompletedAt = &finished
	if s.opts.UnknownPolicy == UnknownRetryOnce && delivery.UnknownRetryCount == 0 && delivery.AttemptCount < delivery.MaxAttempts {
		delivery.UnknownRetryCount++
		due := now.Add(s.retryDelayFor(delivery, delivery.AttemptCount, 0))
		delivery.NextRetryAt = &due
	}
	return tx.Save(delivery).Error
}

func deliveryMetadata(notification *model.Notification, delivery *model.Delivery) DeliveryMetadata {
	metadata := DeliveryMetadata{MessageType: delivery.MessageType}
	var stored struct {
		EventID      string    `json:"event_id"`
		MessageID    string    `json:"msg_id"`
		MessageSeq   int       `json:"msg_seq"`
		IsWakeUp     bool      `json:"is_wakeup"`
		Mode         string    `json:"mode"`
		TargetType   string    `json:"target_type"`
		TargetOpenID string    `json:"target_openid"`
		ReceivedAt   time.Time `json:"received_at"`
	}
	storedJSON := notification.MetadataJSON
	if delivery.MetadataJSON != "" {
		storedJSON = delivery.MetadataJSON
	}
	if json.Unmarshal([]byte(storedJSON), &stored) == nil {
		metadata.EventID = stored.EventID
		metadata.MessageID = stored.MessageID
		metadata.MessageSeq = stored.MessageSeq
		metadata.IsWakeUp = stored.IsWakeUp
		metadata.Mode = stored.Mode
		metadata.TargetType = stored.TargetType
		metadata.TargetOpenID = stored.TargetOpenID
		metadata.ReceivedAt = stored.ReceivedAt
	}
	if delivery.MessageSequence > 0 {
		metadata.MessageSeq = delivery.MessageSequence
	}
	return metadata
}

func (s *Service) renewLease(ctx context.Context, deliveryID, token string, stop <-chan struct{}, cancel context.CancelFunc, lost chan<- struct{}) {
	interval := s.opts.LeaseDuration / 3
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			renewCtx, renewCancel := context.WithTimeout(context.Background(), 5*time.Second)
			expires := time.Now().UTC().Add(s.opts.LeaseDuration)
			result := s.db.WithContext(renewCtx).Model(&model.Delivery{}).
				Where("id = ? AND lease_token = ? AND status = ?", deliveryID, token, model.DeliveryProcessing).
				Update("lease_expires_at", expires)
			renewCancel()
			if result.Error != nil || result.RowsAffected == 0 {
				select {
				case lost <- struct{}{}:
				default:
				}
				cancel()
				return
			}
		}
	}
}

func (s *Service) finishDelivery(ctx context.Context, claim *deliveryClaim, result SendResult, sendErr error) error {
	now := time.Now().UTC()
	failure := deliveryFailure{deliveryState: model.DeliveryAccepted, attemptResult: model.DeliveryAccepted, attempted: true}
	if sendErr != nil {
		failure = classifyError(sendErr)
	}
	attempt := claim.attempt
	attempt.FinishedAt = &now
	attempt.DurationMS = now.Sub(attempt.StartedAt).Milliseconds()
	attempt.HTTPStatus = result.HTTPStatus
	if attempt.HTTPStatus == 0 {
		attempt.HTTPStatus = failure.httpStatus
	}
	attempt.ProviderErrorCode = firstNonEmpty(failure.providerCode, result.ProviderCode)
	attempt.ProviderMessageID = result.ProviderMessageID
	attempt.Result = failure.attemptResult
	attempt.AttemptKind = "send"
	attempt.ErrorCategory = failure.category
	if sendErr != nil {
		attempt.ErrorMessage = safeErrorMessage(sendErr)
	}
	var nextRetryAt *time.Time
	deliveryStatus := failure.deliveryState
	unknownRetryCount := claim.delivery.UnknownRetryCount
	preparationFailureCount := claim.delivery.PreparationFailureCount
	budgetAttemptCount := claim.delivery.AttemptCount
	if !failure.attempted && !failure.unknown {
		budgetAttemptCount--
		if budgetAttemptCount < 0 {
			budgetAttemptCount = 0
		}
	}
	if failure.unknown {
		// Unknown means the provider may have accepted the request, so it consumes budget.
		budgetAttemptCount = claim.delivery.AttemptCount
	}
	preflightFailure := sendErr != nil && !failure.attempted && !failure.unknown
	if preflightFailure {
		attempt.AttemptKind = "preflight"
		attempt.Result = "preflight_failed"
		if failure.retryable {
			deliveryStatus = model.DeliveryRetrying
			due := now.Add(s.retryDelayFor(claim.delivery, claim.delivery.AttemptCount, failure.retryAfter))
			nextRetryAt = &due
			if failure.category != "rate_limit" {
				preparationFailureCount++
				if preparationFailureCount >= s.opts.MaxPreparationFailures {
					deliveryStatus = model.DeliveryFailed
					nextRetryAt = nil
					attempt.Result = "preflight_exhausted"
				}
			}
		}
	} else if sendErr != nil {
		preparationFailureCount = 0
		if failure.unknown {
			if s.opts.UnknownPolicy == UnknownRetryOnce && unknownRetryCount == 0 && budgetAttemptCount < claim.delivery.MaxAttempts {
				unknownRetryCount++
				due := now.Add(s.retryDelayFor(claim.delivery, claim.delivery.AttemptCount, failure.retryAfter))
				nextRetryAt = &due
			}
		} else if failure.retryable && budgetAttemptCount < claim.delivery.MaxAttempts {
			deliveryStatus = model.DeliveryRetrying
			attempt.Result = model.DeliveryRetrying
			due := now.Add(s.retryDelayFor(claim.delivery, claim.delivery.AttemptCount, failure.retryAfter))
			nextRetryAt = &due
		} else if failure.retryable && failure.attempted {
			deliveryStatus = model.DeliveryDeadLetter
			attempt.Result = model.DeliveryDeadLetter
		}
	} else {
		preparationFailureCount = 0
	}
	attempt.NextRetryAt = nextRetryAt

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var notification model.Notification
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", claim.notification.Id).First(&notification).Error; err != nil {
			return err
		}
		var delivery model.Delivery
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", claim.delivery.Id).First(&delivery).Error; err != nil {
			return err
		}
		update := tx.Model(&model.Delivery{}).Where("id = ? AND lease_token = ? AND status = ?", claim.delivery.Id, claim.leaseToken, model.DeliveryProcessing).
			Updates(map[string]interface{}{
				"status": deliveryStatus, "attempt_count": budgetAttemptCount,
				"preparation_failure_count": preparationFailureCount,
				"unknown_retry_count":       unknownRetryCount, "next_retry_at": nextRetryAt,
				"lease_owner": "", "lease_token": "", "lease_expires_at": nil,
				"last_error_code": attempt.ProviderErrorCode, "last_error_category": attempt.ErrorCategory,
				"last_error_message": attempt.ErrorMessage,
				"completed_at":       completedAt(deliveryStatus, now), "updated_at": now,
			})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected == 0 {
			return ErrLeaseLost
		}
		if err := tx.Model(&model.DeliveryAttempt{}).Where("id = ?", attempt.Id).Updates(map[string]interface{}{
			"finished_at": now, "duration_ms": attempt.DurationMS, "http_status": attempt.HTTPStatus,
			"attempt_kind":        attempt.AttemptKind,
			"provider_error_code": attempt.ProviderErrorCode, "provider_message_id": attempt.ProviderMessageID,
			"error_category": attempt.ErrorCategory, "error_message": attempt.ErrorMessage,
			"result": attempt.Result, "next_retry_at": nextRetryAt,
		}).Error; err != nil {
			return err
		}
		if deliveryStatus == model.DeliveryAccepted {
			if err := tx.Model(&model.Delivery{}).Where("notification_id = ? AND status = ?", notification.Id, model.DeliveryHeld).
				Updates(map[string]interface{}{"status": model.DeliveryCancelled, "completed_at": now, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		if (deliveryStatus == model.DeliveryFailed || deliveryStatus == model.DeliveryDeadLetter) && notification.DeliveryPolicy == PolicyFallback {
			if err := s.activateFallback(tx, &delivery, now); err != nil {
				return err
			}
		}
		return s.refreshNotificationStatus(tx, notification.Id, now)
	})
}

func classifyError(err error) deliveryFailure {
	failure := deliveryFailure{
		category: "unclassified", deliveryState: model.DeliveryUnknown,
		attemptResult: model.DeliveryUnknown, unknown: true, attempted: true,
	}
	var classified *common.DeliveryError
	if !errors.As(err, &classified) || classified == nil {
		return failure
	}
	failure.category = classified.DeliveryCategory()
	failure.providerCode = classified.ProviderCode()
	failure.httpStatus = classified.HTTPStatus()
	failure.retryable = classified.Retryable()
	failure.unknown = classified.DeliveryUnknown()
	failure.retryAfter = classified.RetryAfter()
	if failure.unknown {
		failure.attempted = true
		failure.deliveryState = model.DeliveryUnknown
		failure.attemptResult = model.DeliveryUnknown
	} else {
		var attemptInfo deliveryAttemptContract
		if errors.As(err, &attemptInfo) {
			failure.attempted = attemptInfo.SendAttempted()
		} else {
			failure.attempted = true
		}
		failure.deliveryState = model.DeliveryFailed
		failure.attemptResult = model.DeliveryFailed
	}
	return failure
}

func (s *Service) retryDelay(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return retryAfter
	}
	if len(s.opts.RetryBackoff) == 0 {
		return time.Minute
	}
	index := attempt - 1
	if index < 0 {
		index = 0
	}
	if index >= len(s.opts.RetryBackoff) {
		index = len(s.opts.RetryBackoff) - 1
	}
	delay := s.opts.RetryBackoff[index]
	if s.opts.JitterFraction <= 0 {
		return delay
	}
	jitter := s.opts.JitterFraction
	if jitter > 1 {
		jitter = 1
	}
	spread := time.Duration(float64(delay) * jitter)
	if spread <= 0 {
		return delay
	}
	return delay - spread + time.Duration(time.Now().UnixNano()%int64(2*spread+1))
}

func (s *Service) retryDelayFor(delivery *model.Delivery, attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return retryAfter
	}
	var seconds []int
	if delivery != nil && delivery.RetryBackoffJSON != "" && json.Unmarshal([]byte(delivery.RetryBackoffJSON), &seconds) == nil && len(seconds) > 0 {
		index := attempt - 1
		if index < 0 {
			index = 0
		}
		if index >= len(seconds) {
			index = len(seconds) - 1
		}
		return s.retryDelay(attempt, time.Duration(seconds[index])*time.Second)
	}
	return s.retryDelay(attempt, 0)
}

func (s *Service) activateFallback(tx *gorm.DB, failed *model.Delivery, now time.Time) error {
	var next model.Delivery
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("notification_id = ? AND status = ? AND route_order > ?", failed.NotificationID, model.DeliveryHeld, failed.RouteOrder).
		Order("route_order ASC").First(&next).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return tx.Model(&model.Delivery{}).Where("notification_id = ? AND status = ?", failed.NotificationID, model.DeliveryHeld).
			Updates(map[string]interface{}{"status": model.DeliveryCancelled, "completed_at": now, "updated_at": now}).Error
	}
	result := tx.Model(&model.Delivery{}).Where("id = ? AND status = ?", next.Id, model.DeliveryHeld).
		Updates(map[string]interface{}{"status": model.DeliveryPendingDispatch, "dispatch_generation": gorm.Expr("dispatch_generation + 1"), "updated_at": now})
	if result.Error != nil || result.RowsAffected == 0 {
		return result.Error
	}
	if err := tx.Where("id = ?", next.Id).First(&next).Error; err != nil {
		return err
	}
	return createOutbox(tx, &next, now)
}

func (s *Service) refreshNotificationStatus(tx *gorm.DB, notificationID string, now time.Time) error {
	var deliveries []model.Delivery
	if err := tx.Select("status").Where("notification_id = ?", notificationID).Find(&deliveries).Error; err != nil {
		return err
	}
	if len(deliveries) == 0 {
		return nil
	}
	status := model.NotificationFailed
	hasActive := false
	hasAccepted := false
	hasUnknown := false
	hasRetry := false
	hasQueued := false
	hasPendingDispatch := false
	allCancelled := true
	for _, delivery := range deliveries {
		switch delivery.Status {
		case model.DeliveryAccepted:
			hasAccepted = true
			allCancelled = false
		case model.DeliveryUnknown:
			hasUnknown = true
			hasActive = true
			allCancelled = false
		case model.DeliveryRetrying:
			hasRetry = true
			hasActive = true
			allCancelled = false
		case model.DeliveryQueued, model.DeliveryPendingDispatch:
			hasQueued = true
			if delivery.Status == model.DeliveryPendingDispatch {
				hasPendingDispatch = true
			}
			hasActive = true
			allCancelled = false
		case model.DeliveryProcessing:
			hasActive = true
			allCancelled = false
		case model.DeliveryHeld:
			hasActive = true
			allCancelled = false
		case model.DeliveryCancelled:
		default:
			allCancelled = false
		}
	}
	if allCancelled {
		status = model.NotificationCancelled
	} else if hasActive {
		switch {
		case hasUnknown:
			status = model.NotificationUnknown
		case hasRetry:
			status = model.NotificationRetrying
		case hasQueued:
			if hasPendingDispatch {
				status = model.NotificationPendingDispatch
			} else {
				status = model.NotificationQueued
			}
		default:
			status = model.NotificationProcessing
		}
	} else if hasAccepted {
		status = model.NotificationAccepted
	}
	var completedAt interface{}
	if status == model.NotificationAccepted || status == model.NotificationFailed || status == model.NotificationCancelled {
		completedAt = now
	}
	return tx.Model(&model.Notification{}).Where("id = ?", notificationID).
		Updates(map[string]interface{}{"status": status, "completed_at": completedAt, "updated_at": now}).Error
}

func (s *Service) ackMessage(ctx context.Context, messageID string) error {
	if err := s.rdb.XAck(ctx, s.opts.StreamName, s.opts.ConsumerGroup, messageID).Err(); err != nil {
		return err
	}
	return s.rdb.XDel(ctx, s.opts.StreamName, messageID).Err()
}

func isTerminal(status string) bool {
	switch status {
	case model.DeliveryAccepted, model.DeliveryFailed, model.DeliveryCancelled, model.DeliveryUnknown, model.DeliveryDeadLetter:
		return true
	default:
		return false
	}
}

func completedAt(status string, now time.Time) interface{} {
	if status == model.DeliveryAccepted || status == model.DeliveryFailed || status == model.DeliveryCancelled || status == model.DeliveryDeadLetter {
		return now
	}
	if status == model.DeliveryUnknown {
		return now
	}
	return nil
}

func safeErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	var classified deliveryErrorContract
	if errors.As(err, &classified) {
		parts := []string{"delivery failed", classified.DeliveryCategory()}
		if code := classified.ProviderCode(); code != "" {
			parts = append(parts, code)
		}
		return strings.Join(parts, ": ")
	}
	return "delivery failed with an unclassified error"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
