package gateway

import (
	"context"
	"errors"
	"time"

	"message-pusher/common"
	"message-pusher/model"
)

// PruneLogsOnce removes old audit entries and superseded completed attempts.
// Notification/idempotency records, delivery states, callback deduplication and
// the latest attempt of every delivery remain intact. Unknown/dead-letter
// deliveries retain their complete evidence for manual investigation.
func (s *Service) PruneLogsOnce(ctx context.Context, attemptDays, auditDays int) error {
	if s == nil || s.db == nil {
		return errors.New("log retention requires a database")
	}
	now := time.Now().UTC()
	if attemptDays > 0 {
		var ids []string
		cutoff := now.AddDate(0, 0, -attemptDays)
		err := s.db.WithContext(ctx).Table("gateway_delivery_attempts AS a").
			Joins("JOIN gateway_deliveries AS d ON d.id = a.delivery_id").
			Where("d.status IN ? AND a.finished_at < ? AND a.result <> ?", []string{model.DeliveryAccepted, model.DeliveryCancelled, model.DeliveryFailed}, cutoff, "processing").
			Where("EXISTS (SELECT 1 FROM gateway_delivery_attempts AS newer WHERE newer.delivery_id = a.delivery_id AND newer.attempt_number > a.attempt_number)").
			Order("a.finished_at ASC").Limit(500).Pluck("a.id", &ids).Error
		if err != nil {
			return err
		}
		if len(ids) > 0 {
			if err := s.db.WithContext(ctx).Where("id IN ? AND finished_at < ? AND result <> ?", ids, cutoff, "processing").Delete(&model.DeliveryAttempt{}).Error; err != nil {
				return err
			}
		}
	}
	if auditDays > 0 {
		var ids []string
		cutoff := now.AddDate(0, 0, -auditDays)
		if err := s.db.WithContext(ctx).Model(&model.AuditLog{}).Where("created_at < ?", cutoff).Order("created_at ASC").Limit(500).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) > 0 {
			return s.db.WithContext(ctx).Where("id IN ? AND created_at < ?", ids, cutoff).Delete(&model.AuditLog{}).Error
		}
	}
	return nil
}

func (s *Service) RunLogRetention(ctx context.Context, attemptDays, auditDays int) {
	if attemptDays == 0 && auditDays == 0 {
		return
	}
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		operationCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := s.PruneLogsOnce(operationCtx, attemptDays, auditDays)
		cancel()
		if err != nil && ctx.Err() == nil {
			common.SysError("gateway log retention failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
