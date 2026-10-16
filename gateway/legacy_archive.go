package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"message-pusher/common"
	"message-pusher/model"
)

const LegacyArchiveIDPrefix = "legacy-archive:"

type legacyArchiveMetadata struct {
	Archive         bool   `json:"archive"`
	LegacyMessageID int    `json:"legacy_message_id"`
	Channel         string `json:"channel,omitempty"`
	Description     string `json:"description,omitempty"`
	URL             string `json:"url,omitempty"`
	RenderMode      string `json:"render_mode,omitempty"`
}

// ParseLegacyArchiveID recognizes IDs that refer to retained legacy messages.
func ParseLegacyArchiveID(id string) (int, bool) {
	value, ok := strings.CutPrefix(id, LegacyArchiveIDPrefix)
	if !ok {
		return 0, false
	}
	messageID, err := strconv.Atoi(value)
	if err != nil || messageID < 1 || strconv.Itoa(messageID) != value {
		return 0, false
	}
	return messageID, true
}

// LegacyArchiveNotification converts a legacy message into the gateway
// notification shape used by the session history and detail endpoints.
func LegacyArchiveNotification(message *model.Message) model.Notification {
	if message == nil {
		return model.Notification{}
	}
	metadata, _ := json.Marshal(legacyArchiveMetadata{
		Archive: true, LegacyMessageID: message.Id, Channel: message.Channel,
		Description: message.Description, URL: message.URL, RenderMode: message.RenderMode,
	})
	createdAt := time.Unix(message.Timestamp, 0).UTC()
	return model.Notification{
		Id:             LegacyArchiveIDPrefix + strconv.Itoa(message.Id),
		SourceID:       common.LegacySourceID(message.UserId),
		IdempotencyKey: LegacyArchiveIDPrefix + strconv.Itoa(message.Id),
		Title:          message.Title,
		Content:        message.Content,
		MetadataJSON:   string(metadata),
		Priority:       "normal",
		DeliveryPolicy: PolicyBroadcast,
		Status:         legacyMessageStatus(message.Status),
		CreatedAt:      createdAt,
		UpdatedAt:      createdAt,
	}
}

func legacyMessageStatus(status int) string {
	switch status {
	case common.MessageSendStatusPending:
		return model.NotificationProcessing
	case common.MessageSendStatusSent:
		return model.NotificationAccepted
	case common.MessageSendStatusFailed:
		return model.NotificationFailed
	case common.MessageSendStatusAsyncPending:
		return model.NotificationQueued
	default:
		return model.NotificationUnknown
	}
}

type sessionNotificationRow struct {
	ID                 string          `gorm:"column:id"`
	SourceID           string          `gorm:"column:source_id"`
	IdempotencyKey     string          `gorm:"column:idempotency_key"`
	RequestID          string          `gorm:"column:request_id"`
	Title              string          `gorm:"column:title"`
	Content            string          `gorm:"column:content"`
	MetadataJSON       string          `gorm:"column:metadata"`
	Priority           string          `gorm:"column:priority"`
	DeliveryPolicy     string          `gorm:"column:delivery_policy"`
	Status             string          `gorm:"column:status"`
	IsTest             bool            `gorm:"column:is_test"`
	CreatedEpoch       float64         `gorm:"column:created_epoch"`
	UpdatedEpoch       float64         `gorm:"column:updated_epoch"`
	CompletedEpoch     sql.NullFloat64 `gorm:"column:completed_epoch"`
	ArchiveUserID      int             `gorm:"column:archive_user_id"`
	ArchiveChannel     string          `gorm:"column:archive_channel"`
	ArchiveDescription string          `gorm:"column:archive_description"`
	ArchiveURL         string          `gorm:"column:archive_url"`
	ArchiveRenderMode  string          `gorm:"column:archive_render_mode"`
}

// listNotificationsWithLegacyArchive returns one SQL-paginated history across
// gateway notifications and unbridged legacy messages. The caller enables this
// only for session-authenticated management requests.
func (s *Service) listNotificationsWithLegacyArchive(ctx context.Context, filter NotificationFilter) ([]model.Notification, int64, error) {
	if s == nil || s.db == nil {
		return nil, 0, errors.New("gateway requires a database")
	}

	archiveUserIDs, err := s.legacyArchiveUserIDs(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	dialect := s.db.Dialector.Name()
	createdEpoch := "((julianday(n.created_at) - 2440587.5) * 86400.0)"
	if dialect == "mysql" {
		createdEpoch = "UNIX_TIMESTAMP(n.created_at)"
	}
	textCast := "CAST(m.id AS TEXT)"
	if dialect == "mysql" {
		textCast = "CAST(m.id AS CHAR)"
	}
	archiveIDExpr := "('legacy-archive:' || " + textCast + ")"
	legacyKeyExpr := "('legacy:' || " + textCast + ")"
	if dialect == "mysql" {
		archiveIDExpr = "CONCAT('legacy-archive:', " + textCast + ")"
		legacyKeyExpr = "CONCAT('legacy:', " + textCast + ")"
	}

	var gatewayWhere []string
	var gatewayArgs []interface{}
	gatewayWhere = append(gatewayWhere, "1 = 1")
	if filter.SourceID != "" {
		gatewayWhere = append(gatewayWhere, "n.source_id = ?")
		gatewayArgs = append(gatewayArgs, filter.SourceID)
	}
	if filter.OwnerUserID != 0 {
		gatewayWhere = append(gatewayWhere, "EXISTS (SELECT 1 FROM gateway_sources s WHERE s.id = n.source_id AND s.owner_user_id = ?)")
		gatewayArgs = append(gatewayArgs, filter.OwnerUserID)
	}
	if filter.From != nil {
		gatewayWhere = append(gatewayWhere, "n.created_at >= ?")
		gatewayArgs = append(gatewayArgs, filter.From.UTC())
	}
	if filter.To != nil {
		gatewayWhere = append(gatewayWhere, "n.created_at <= ?")
		gatewayArgs = append(gatewayArgs, filter.To.UTC())
	}
	if filter.Status != "" {
		gatewayWhere = append(gatewayWhere, "n.status = ?")
		gatewayArgs = append(gatewayArgs, filter.Status)
	}
	if filter.ChannelID != "" {
		gatewayWhere = append(gatewayWhere, "EXISTS (SELECT 1 FROM gateway_deliveries d WHERE d.notification_id = n.id AND d.channel_id = ?)")
		gatewayArgs = append(gatewayArgs, filter.ChannelID)
	}
	if filter.Error != "" {
		gatewayWhere = append(gatewayWhere, "EXISTS (SELECT 1 FROM gateway_deliveries d WHERE d.notification_id = n.id AND (d.last_error_category = ? OR d.last_error_code = ?))")
		gatewayArgs = append(gatewayArgs, filter.Error, filter.Error)
	}
	if filter.Search != "" {
		needle := "%" + strings.TrimSpace(filter.Search) + "%"
		gatewayWhere = append(gatewayWhere, "(n.title LIKE ? OR n.content LIKE ?)")
		gatewayArgs = append(gatewayArgs, needle, needle)
	}

	gatewaySelect := `SELECT n.id AS id, n.source_id AS source_id,
 n.idempotency_key AS idempotency_key, n.request_id AS request_id,
	 n.title AS title, n.content AS content, n.metadata_json AS metadata,
 n.priority AS priority, n.delivery_policy AS delivery_policy, n.status AS status,
 n.is_test AS is_test, ` + createdEpoch + ` AS created_epoch,
 ` + func() string {
		if dialect == "mysql" {
			return "UNIX_TIMESTAMP(n.updated_at)"
		}
		return "((julianday(n.updated_at) - 2440587.5) * 86400.0)"
	}() + ` AS updated_epoch,
	 ` + func() string {
		if dialect == "mysql" {
			return "UNIX_TIMESTAMP(n.completed_at)"
		}
		return "((julianday(n.completed_at) - 2440587.5) * 86400.0)"
	}() + ` AS completed_epoch,
 0 AS archive_user_id, '' AS archive_channel, '' AS archive_description,
 '' AS archive_url, '' AS archive_render_mode
 FROM gateway_notifications n WHERE ` + strings.Join(gatewayWhere, " AND ")

	archiveSelect := `SELECT ` + archiveIDExpr + ` AS id, '' AS source_id,
 ` + archiveIDExpr + ` AS idempotency_key, '' AS request_id,
 m.title AS title, m.content AS content, '' AS metadata,
 'normal' AS priority, 'broadcast' AS delivery_policy,
 CASE m.status WHEN 1 THEN 'processing' WHEN 2 THEN 'accepted' WHEN 3 THEN 'failed' WHEN 4 THEN 'queued' ELSE 'delivery_unknown' END AS status,
	 0 AS is_test, m.timestamp AS created_epoch, m.timestamp AS updated_epoch, NULL AS completed_epoch,
 m.user_id AS archive_user_id, m.channel AS archive_channel,
 m.description AS archive_description, m.url AS archive_url, m.render_mode AS archive_render_mode
 FROM messages m WHERE `
	var archiveArgs []interface{}
	archiveConditions := []string{"1 = 1"}
	archiveUserFiltered := filter.OwnerUserID != 0 || filter.SourceID != ""
	if len(archiveUserIDs) == 0 {
		archiveConditions = append(archiveConditions, "1 = 0")
	} else if archiveUserFiltered {
		archiveConditions = append(archiveConditions, "m.user_id = ?")
		archiveArgs = append(archiveArgs, archiveUserIDs[0])
	}
	if filter.From != nil {
		archiveConditions = append(archiveConditions, "m.timestamp >= ?")
		archiveArgs = append(archiveArgs, legacyTimestampLowerBound(*filter.From))
	}
	if filter.To != nil {
		archiveConditions = append(archiveConditions, "m.timestamp <= ?")
		archiveArgs = append(archiveArgs, filter.To.UTC().Unix())
	}
	if filter.Status != "" {
		archiveConditions = append(archiveConditions, "(CASE m.status WHEN 1 THEN 'processing' WHEN 2 THEN 'accepted' WHEN 3 THEN 'failed' WHEN 4 THEN 'queued' ELSE 'delivery_unknown' END) = ?")
		archiveArgs = append(archiveArgs, filter.Status)
	}
	if filter.ChannelID != "" {
		archiveConditions = append(archiveConditions, "EXISTS (SELECT 1 FROM channels c WHERE c.user_id = m.user_id AND c.name = m.channel AND CAST(c.id AS CHAR) = ?)")
		archiveArgs = append(archiveArgs, filter.ChannelID)
	}
	if filter.Error != "" {
		archiveConditions = append(archiveConditions, "1 = 0")
	}
	if filter.Search != "" {
		needle := "%" + strings.TrimSpace(filter.Search) + "%"
		archiveConditions = append(archiveConditions, "(m.title LIKE ? OR m.content LIKE ?)")
		archiveArgs = append(archiveArgs, needle, needle)
	}
	if len(archiveUserIDs) != 0 {
		sourceCaseExpr := legacySourceCase(archiveUserIDs)
		archiveConditions = append(archiveConditions, "NOT EXISTS (SELECT 1 FROM gateway_notifications bridged WHERE bridged.source_id = "+sourceCaseExpr+" AND bridged.idempotency_key = "+legacyKeyExpr+")")
	}
	archiveSelect += strings.Join(archiveConditions, " AND ")
	archiveSelectArgs := append([]interface{}{}, archiveArgs...)

	union := gatewaySelect + " UNION ALL " + archiveSelect
	args := append(append([]interface{}{}, gatewayArgs...), archiveSelectArgs...)
	var countRow struct {
		Total int64 `gorm:"column:total"`
	}
	if err := s.db.WithContext(ctx).Raw("SELECT COUNT(*) AS total FROM ("+union+") AS session_notifications", args...).Scan(&countRow).Error; err != nil {
		return nil, 0, err
	}
	page, pageSize := normalizedPage(filter.Page, filter.PageSize)
	var rows []sessionNotificationRow
	if err := s.db.WithContext(ctx).Raw("SELECT * FROM ("+union+") AS session_notifications ORDER BY created_epoch DESC, id DESC LIMIT ? OFFSET ?", append(args, pageSize, (page-1)*pageSize)...).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	items := make([]model.Notification, 0, len(rows))
	for _, row := range rows {
		createdAt := time.Unix(0, int64(row.CreatedEpoch*float64(time.Second))).UTC()
		updatedAt := time.Unix(0, int64(row.UpdatedEpoch*float64(time.Second))).UTC()
		notification := model.Notification{
			Id: row.ID, SourceID: row.SourceID, IdempotencyKey: row.IdempotencyKey,
			RequestID: row.RequestID, Title: row.Title, Content: row.Content,
			MetadataJSON: row.MetadataJSON, Priority: row.Priority,
			DeliveryPolicy: row.DeliveryPolicy, Status: row.Status, IsTest: row.IsTest,
			CreatedAt: createdAt, UpdatedAt: updatedAt,
		}
		if row.ArchiveUserID != 0 {
			notification.SourceID = common.LegacySourceID(row.ArchiveUserID)
			notification.MetadataJSON = string(mustArchiveMetadata(row))
		}
		if row.CompletedEpoch.Valid {
			completedAt := time.Unix(0, int64(row.CompletedEpoch.Float64*float64(time.Second))).UTC()
			notification.CompletedAt = &completedAt
		}
		items = append(items, notification)
	}
	return items, countRow.Total, nil
}

func (s *Service) legacyArchiveUserIDs(ctx context.Context, filter NotificationFilter) ([]int, error) {
	if filter.OwnerUserID != 0 {
		if filter.SourceID != "" && filter.SourceID != common.LegacySourceID(filter.OwnerUserID) {
			return nil, nil
		}
		return []int{filter.OwnerUserID}, nil
	}
	var userIDs []int
	if err := s.db.WithContext(ctx).Model(&model.Message{}).Distinct("user_id").Order("user_id ASC").Pluck("user_id", &userIDs).Error; err != nil {
		return nil, err
	}
	if filter.SourceID == "" {
		return userIDs, nil
	}
	for _, userID := range userIDs {
		if common.LegacySourceID(userID) == filter.SourceID {
			return []int{userID}, nil
		}
	}
	return nil, nil
}

func legacySourceCase(userIDs []int) string {
	parts := make([]string, 0, len(userIDs))
	for _, userID := range userIDs {
		parts = append(parts, "WHEN "+strconv.Itoa(userID)+" THEN '"+common.LegacySourceID(userID)+"'")
	}
	return "CASE m.user_id " + strings.Join(parts, " ") + " ELSE '' END"
}

func mustArchiveMetadata(row sessionNotificationRow) []byte {
	encoded, err := json.Marshal(legacyArchiveMetadata{
		Archive: true, LegacyMessageID: archiveMessageID(row.ID), Channel: row.ArchiveChannel,
		Description: row.ArchiveDescription, URL: row.ArchiveURL, RenderMode: row.ArchiveRenderMode,
	})
	if err != nil {
		return []byte(fmt.Sprintf(`{"archive":true,"legacy_message_id":%d}`, archiveMessageID(row.ID)))
	}
	return encoded
}

func archiveMessageID(id string) int {
	messageID, _ := ParseLegacyArchiveID(id)
	return messageID
}

func legacyTimestampLowerBound(value time.Time) int64 {
	seconds := value.UTC().Unix()
	if value.UTC().Nanosecond() > 0 {
		seconds++
	}
	return seconds
}
