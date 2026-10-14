package gateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"message-pusher/common"
	"message-pusher/model"
)

func (s *Service) CreateSource(ctx context.Context, ownerUserID int, input SourceInput) (*model.Source, error) {
	if ownerUserID <= 0 || strings.TrimSpace(input.Name) == "" {
		return nil, fmt.Errorf("%w: owner and source name are required", ErrInvalidRequest)
	}
	status := input.Status
	if status == "" {
		status = model.GatewayStatusActive
	}
	if status != model.GatewayStatusActive && status != model.GatewayStatusDisabled {
		return nil, fmt.Errorf("%w: invalid source status", ErrInvalidRequest)
	}
	allowed, err := json.Marshal(uniqueStrings(input.AllowedChannelIDs))
	if err != nil {
		return nil, err
	}
	if input.RateLimitPerMinute < 0 {
		return nil, fmt.Errorf("%w: rate_limit_per_minute cannot be negative", ErrInvalidRequest)
	}
	var source model.Source
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := validateOwnedChannels(tx, ownerUserID, input.AllowedChannelIDs); err != nil {
			return err
		}
		now := time.Now().UTC()
		source = model.Source{
			Id: newID(), OwnerUserID: ownerUserID, Name: strings.TrimSpace(input.Name),
			Description: input.Description, Status: status, RateLimitPerMinute: input.RateLimitPerMinute,
			AllowedChannelIDsJSON: string(allowed), CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&source).Error; err != nil {
			return err
		}
		return writeAuditTx(tx, "user", fmt.Sprint(ownerUserID), source.Id, "source.create", "source", source.Id, "{}", now)
	})
	if err != nil {
		return nil, err
	}
	return &source, nil
}

func (s *Service) UpdateSource(ctx context.Context, ownerUserID int, sourceID string, input SourceInput) (*model.Source, error) {
	if ownerUserID <= 0 || strings.TrimSpace(input.Name) == "" {
		return nil, fmt.Errorf("%w: owner and source name are required", ErrInvalidRequest)
	}
	status := input.Status
	if status == "" {
		status = model.GatewayStatusActive
	}
	if status != model.GatewayStatusActive && status != model.GatewayStatusDisabled {
		return nil, fmt.Errorf("%w: invalid source status", ErrInvalidRequest)
	}
	if input.RateLimitPerMinute < 0 {
		return nil, fmt.Errorf("%w: rate_limit_per_minute cannot be negative", ErrInvalidRequest)
	}
	allowed, err := json.Marshal(uniqueStrings(input.AllowedChannelIDs))
	if err != nil {
		return nil, err
	}
	var source model.Source
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := validateOwnedChannels(tx, ownerUserID, input.AllowedChannelIDs); err != nil {
			return err
		}
		if err := tx.Where("id = ? AND owner_user_id = ?", sourceID, ownerUserID).First(&source).Error; err != nil {
			return notFound(err)
		}
		updates := map[string]interface{}{
			"name": strings.TrimSpace(input.Name), "description": input.Description,
			"status": status, "rate_limit_per_minute": input.RateLimitPerMinute,
			"allowed_channel_ids_json": string(allowed), "updated_at": time.Now().UTC(),
		}
		if err := tx.Model(&source).Updates(updates).Error; err != nil {
			return err
		}
		source.Name = strings.TrimSpace(input.Name)
		source.Description = input.Description
		source.Status = status
		source.RateLimitPerMinute = input.RateLimitPerMinute
		source.AllowedChannelIDsJSON = string(allowed)
		return writeAuditTx(tx, "user", fmt.Sprint(ownerUserID), sourceID, "source.update", "source", sourceID, "{}", time.Now().UTC())
	})
	if err != nil {
		return nil, err
	}
	return &source, nil
}

func (s *Service) DeleteSource(ctx context.Context, ownerUserID int, sourceID, actorID string) error {
	if ownerUserID <= 0 || strings.TrimSpace(sourceID) == "" {
		return fmt.Errorf("%w: owner and source are required", ErrInvalidRequest)
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var source model.Source
		if err := tx.Where("id = ? AND owner_user_id = ?", sourceID, ownerUserID).First(&source).Error; err != nil {
			return notFound(err)
		}
		now := time.Now().UTC()
		if err := tx.Model(&source).Updates(map[string]interface{}{"status": model.GatewayStatusDisabled, "updated_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.APIKey{}).Where("source_id = ? AND status = ?", sourceID, model.GatewayStatusActive).
			Updates(map[string]interface{}{"status": model.GatewayStatusRevoked, "updated_at": now}).Error; err != nil {
			return err
		}
		return writeAuditTx(tx, "user", actorID, sourceID, "source.disable", "source", sourceID, "{}", now)
	})
}

func (s *Service) ListSources(ctx context.Context, ownerUserID, limit, offset int) ([]model.Source, int64, error) {
	query := s.db.WithContext(ctx).Model(&model.Source{})
	if ownerUserID != 0 {
		query = query.Where("owner_user_id = ?", ownerUserID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var sources []model.Source
	err := query.Order("created_at DESC").Limit(limit).Offset(offset).Find(&sources).Error
	return sources, total, err
}

func (s *Service) CreateAPIKey(ctx context.Context, ownerUserID int, sourceID, name string, scopes []string, expiresAt *time.Time) (*model.APIKey, string, error) {
	return s.createAPIKey(ctx, ownerUserID, sourceID, name, scopes, expiresAt, "", "")
}

func (s *Service) RotateAPIKey(ctx context.Context, ownerUserID int, sourceID, keyID, actorID string, scopes []string, expiresAt *time.Time) (*model.APIKey, string, error) {
	return s.createAPIKey(ctx, ownerUserID, sourceID, "", scopes, expiresAt, keyID, actorID)
}

func (s *Service) createAPIKey(ctx context.Context, ownerUserID int, sourceID, name string, scopes []string, expiresAt *time.Time, rotateKeyID, actorID string) (*model.APIKey, string, error) {
	if ownerUserID <= 0 || strings.TrimSpace(sourceID) == "" {
		return nil, "", fmt.Errorf("%w: owner and source are required", ErrInvalidRequest)
	}
	scopes = uniqueStrings(scopes)
	secret, err := generateAPIKey()
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256([]byte(secret))
	key := model.APIKey{
		Id: newID(), SourceID: sourceID, Name: strings.TrimSpace(name),
		Prefix: secret[:12], KeyHash: hex.EncodeToString(digest[:]),
		Status:    model.GatewayStatusActive,
		ExpiresAt: expiresAt,
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var source model.Source
		if err := tx.Where("id = ? AND owner_user_id = ?", sourceID, ownerUserID).First(&source).Error; err != nil {
			return notFound(err)
		}
		if source.Status != model.GatewayStatusActive {
			return fmt.Errorf("%w: source is disabled", ErrInvalidRequest)
		}
		if rotateKeyID != "" {
			var previous model.APIKey
			if err := tx.Where("id = ? AND source_id = ?", rotateKeyID, sourceID).First(&previous).Error; err != nil {
				return notFound(err)
			}
			if err := tx.Model(&previous).Updates(map[string]interface{}{"status": model.GatewayStatusRevoked, "updated_at": time.Now().UTC()}).Error; err != nil {
				return err
			}
			if key.Name == "" {
				key.Name = previous.Name
			}
			if len(scopes) == 0 {
				key.ScopesJSON = previous.ScopesJSON
			} else {
				encodedScopes, err := json.Marshal(scopes)
				if err != nil {
					return err
				}
				key.ScopesJSON = string(encodedScopes)
			}
			if expiresAt == nil {
				key.ExpiresAt = previous.ExpiresAt
			}
		}
		if key.Name == "" {
			key.Name = "API Key"
		}
		if key.ScopesJSON == "" {
			if len(scopes) == 0 {
				scopes = []string{ScopeNotificationsWrite, ScopeNotificationsRead}
			}
			encodedScopes, err := json.Marshal(scopes)
			if err != nil {
				return err
			}
			key.ScopesJSON = string(encodedScopes)
		}
		if err := tx.Create(&key).Error; err != nil {
			return err
		}
		action := "api_key.create"
		if rotateKeyID != "" {
			action = "api_key.rotate"
		}
		return writeAuditTx(tx, "user", actorIDOr(actorID, fmt.Sprint(ownerUserID)), sourceID, action, "api_key", key.Id, "{\"scopes\":\"stored\"}", time.Now().UTC())
	})
	if err != nil {
		return nil, "", err
	}
	return &key, secret, nil
}

func (s *Service) RevokeAPIKey(ctx context.Context, ownerUserID int, sourceID, keyID, actorID string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var source model.Source
		if err := tx.Where("id = ? AND owner_user_id = ?", sourceID, ownerUserID).First(&source).Error; err != nil {
			return notFound(err)
		}
		var key model.APIKey
		if err := tx.Where("id = ? AND source_id = ?", keyID, sourceID).First(&key).Error; err != nil {
			return notFound(err)
		}
		now := time.Now().UTC()
		if err := tx.Model(&key).Updates(map[string]interface{}{"status": model.GatewayStatusRevoked, "updated_at": now}).Error; err != nil {
			return err
		}
		return writeAuditTx(tx, "user", actorIDOr(actorID, fmt.Sprint(ownerUserID)), sourceID, "api_key.revoke", "api_key", keyID, "{}", now)
	})
}

// DisableAPIKey temporarily blocks a key while preserving its ability to be
// re-enabled by the source owner. Revoked keys remain permanently unusable.
func (s *Service) DisableAPIKey(ctx context.Context, ownerUserID int, sourceID, keyID, actorID string) error {
	if ownerUserID <= 0 || sourceID == "" || keyID == "" {
		return fmt.Errorf("%w: owner, source and API key are required", ErrInvalidRequest)
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var source model.Source
		if err := tx.Where("id = ? AND owner_user_id = ?", sourceID, ownerUserID).First(&source).Error; err != nil {
			return notFound(err)
		}
		var key model.APIKey
		if err := tx.Where("id = ? AND source_id = ?", keyID, sourceID).First(&key).Error; err != nil {
			return notFound(err)
		}
		if key.Status == model.GatewayStatusRevoked {
			return fmt.Errorf("%w: revoked API keys cannot be reactivated", ErrInvalidRequest)
		}
		now := time.Now().UTC()
		if key.Status != model.GatewayStatusDisabled {
			if err := tx.Model(&key).Updates(map[string]interface{}{"status": model.GatewayStatusDisabled, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		return writeAuditTx(tx, "user", actorIDOr(actorID, fmt.Sprint(ownerUserID)), sourceID, "api_key.disable", "api_key", keyID, "{}", now)
	})
}

func (s *Service) EnableAPIKey(ctx context.Context, ownerUserID int, sourceID, keyID, actorID string) error {
	if ownerUserID <= 0 || sourceID == "" || keyID == "" {
		return fmt.Errorf("%w: owner, source and API key are required", ErrInvalidRequest)
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var source model.Source
		if err := tx.Where("id = ? AND owner_user_id = ?", sourceID, ownerUserID).First(&source).Error; err != nil {
			return notFound(err)
		}
		if source.Status != model.GatewayStatusActive {
			return fmt.Errorf("%w: source is disabled", ErrInvalidRequest)
		}
		var key model.APIKey
		if err := tx.Where("id = ? AND source_id = ?", keyID, sourceID).First(&key).Error; err != nil {
			return notFound(err)
		}
		if key.Status == model.GatewayStatusRevoked {
			return fmt.Errorf("%w: revoked API keys cannot be reactivated", ErrInvalidRequest)
		}
		now := time.Now().UTC()
		if key.Status != model.GatewayStatusActive {
			if err := tx.Model(&key).Updates(map[string]interface{}{"status": model.GatewayStatusActive, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		return writeAuditTx(tx, "user", actorIDOr(actorID, fmt.Sprint(ownerUserID)), sourceID, "api_key.enable", "api_key", keyID, "{}", now)
	})
}

func (s *Service) ListAPIKeys(ctx context.Context, ownerUserID int, sourceID string) ([]model.APIKey, error) {
	query := s.db.WithContext(ctx).Model(&model.APIKey{}).Joins("JOIN gateway_sources ON gateway_sources.id = gateway_api_keys.source_id")
	if ownerUserID != 0 {
		query = query.Where("gateway_sources.owner_user_id = ?", ownerUserID)
	}
	if sourceID != "" {
		query = query.Where("gateway_api_keys.source_id = ?", sourceID)
	}
	var keys []model.APIKey
	err := query.Select("gateway_api_keys.*").Order("gateway_api_keys.created_at DESC").Find(&keys).Error
	return keys, err
}

func (s *Service) LookupAPIKey(ctx context.Context, plaintext string) (*model.APIKey, *model.Source, error) {
	if strings.TrimSpace(plaintext) == "" {
		return nil, nil, ErrNotFound
	}
	digest := sha256.Sum256([]byte(plaintext))
	var key model.APIKey
	if err := s.db.WithContext(ctx).Where("key_hash = ?", hex.EncodeToString(digest[:])).First(&key).Error; err != nil {
		return nil, nil, notFound(err)
	}
	if key.Status != model.GatewayStatusActive || (key.ExpiresAt != nil && !key.ExpiresAt.After(time.Now().UTC())) {
		return nil, nil, ErrNotFound
	}
	var source model.Source
	if err := s.db.WithContext(ctx).Where("id = ? AND status = ?", key.SourceID, model.GatewayStatusActive).First(&source).Error; err != nil {
		return nil, nil, notFound(err)
	}
	var owner model.User
	if err := s.db.WithContext(ctx).Select("id", "status").Where("id = ?", source.OwnerUserID).First(&owner).Error; err != nil {
		return nil, nil, ErrNotFound
	}
	if owner.Status != common.UserStatusEnabled {
		return nil, nil, ErrNotFound
	}
	now := time.Now().UTC()
	if err := s.db.WithContext(ctx).Model(&key).Updates(map[string]interface{}{"last_used_at": now, "updated_at": now}).Error; err != nil {
		return nil, nil, err
	}
	key.LastUsedAt = &now
	return &key, &source, nil
}

func HasScope(key *model.APIKey, requested string) bool {
	if key == nil || key.Status != model.GatewayStatusActive {
		return false
	}
	var scopes []string
	if json.Unmarshal([]byte(key.ScopesJSON), &scopes) != nil {
		return false
	}
	for _, scope := range scopes {
		if scope == "*" || scope == requested {
			return true
		}
	}
	return false
}

func (s *Service) HasScope(key *model.APIKey, requested string) bool { return HasScope(key, requested) }

func (s *Service) AllowAPIKey(ctx context.Context, keyID string, limit int, window time.Duration) (bool, error) {
	if limit <= 0 {
		return true, nil
	}
	if s.rdb == nil {
		return false, errors.New("API key rate limit requires Redis")
	}
	if window <= 0 {
		window = time.Minute
	}
	bucket := time.Now().UTC().UnixNano() / int64(window)
	key := fmt.Sprintf("%s:%s:%d", s.opts.RateLimitPrefix, keyID, bucket)
	count, err := s.rdb.Incr(ctx, key).Result()
	if err != nil {
		return false, err
	}
	if count == 1 {
		if err := s.rdb.Expire(ctx, key, window*2).Err(); err != nil {
			return false, err
		}
	}
	return count <= int64(limit), nil
}

func (s *Service) UpsertTarget(ctx context.Context, ownerUserID int, input TargetInput) (*model.Target, error) {
	if ownerUserID <= 0 || input.SourceID == "" || input.ChannelID == "" || input.TargetType == "" || input.ExternalID == "" {
		return nil, fmt.Errorf("%w: source, channel, target type and external id are required", ErrInvalidRequest)
	}
	if input.Status == "" {
		input.Status = model.GatewayStatusActive
	}
	if input.Status != model.GatewayStatusActive && input.Status != model.GatewayStatusDisabled {
		return nil, fmt.Errorf("%w: invalid target status", ErrInvalidRequest)
	}
	var target model.Target
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var source model.Source
		if err := tx.Where("id = ? AND owner_user_id = ?", input.SourceID, ownerUserID).First(&source).Error; err != nil {
			return notFound(err)
		}
		allowed, err := decodeAllowedChannels(source.AllowedChannelIDsJSON)
		if err != nil {
			return err
		}
		if source.Status != model.GatewayStatusActive || !allowed[input.ChannelID] {
			return fmt.Errorf("%w: source is not permitted to use this channel", ErrInvalidRequest)
		}
		if input.Id != "" {
			if err := tx.Where("id = ? AND source_id = ?", input.Id, input.SourceID).First(&target).Error; err != nil {
				return notFound(err)
			}
		} else {
			err := tx.Where("source_id = ? AND channel_id = ? AND target_type = ? AND external_id = ?", input.SourceID, input.ChannelID, input.TargetType, input.ExternalID).First(&target).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if errors.Is(err, gorm.ErrRecordNotFound) {
				target = model.Target{Id: newID(), SourceID: input.SourceID, ChannelID: input.ChannelID, TargetType: input.TargetType, ExternalID: input.ExternalID}
			}
		}
		if input.IsDefault {
			if err := tx.Model(&model.Target{}).Where("source_id = ? AND channel_id = ?", input.SourceID, input.ChannelID).
				Update("is_default", false).Error; err != nil {
				return err
			}
		}
		target.ChannelID = input.ChannelID
		target.TargetType = input.TargetType
		target.ExternalID = input.ExternalID
		target.Name = input.Name
		target.Source = input.Source
		target.Status = input.Status
		target.LastInteractionAt = input.LastInteractionAt
		target.IsDefault = input.IsDefault
		if target.CreatedAt.IsZero() {
			target.CreatedAt = time.Now().UTC()
		}
		target.UpdatedAt = time.Now().UTC()
		if err := tx.Save(&target).Error; err != nil {
			return err
		}
		return writeAuditTx(tx, "user", fmt.Sprint(ownerUserID), input.SourceID, "target.upsert", "target", target.Id, "{}", target.UpdatedAt)
	})
	if err != nil {
		return nil, err
	}
	return &target, nil
}

func (s *Service) ListTargets(ctx context.Context, ownerUserID int, sourceID string) ([]model.Target, error) {
	var targets []model.Target
	err := s.db.WithContext(ctx).Model(&model.Target{}).
		Joins("JOIN gateway_sources ON gateway_sources.id = gateway_targets.source_id").
		Where("gateway_targets.source_id = ? AND gateway_sources.owner_user_id = ?", sourceID, ownerUserID).
		Select("gateway_targets.*").Order("gateway_targets.channel_id ASC, gateway_targets.is_default DESC, gateway_targets.name ASC").Find(&targets).Error
	return targets, err
}

func (s *Service) DeleteTarget(ctx context.Context, ownerUserID int, sourceID, targetID, actorID string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var source model.Source
		if err := tx.Where("id = ? AND owner_user_id = ?", sourceID, ownerUserID).First(&source).Error; err != nil {
			return notFound(err)
		}
		var target model.Target
		if err := tx.Where("id = ? AND source_id = ?", targetID, sourceID).First(&target).Error; err != nil {
			return notFound(err)
		}
		now := time.Now().UTC()
		if err := tx.Model(&target).Updates(map[string]interface{}{"status": model.GatewayStatusDisabled, "updated_at": now}).Error; err != nil {
			return err
		}
		return writeAuditTx(tx, "user", actorIDOr(actorID, fmt.Sprint(ownerUserID)), sourceID, "target.disable", "target", targetID, "{}", now)
	})
}

func (s *Service) TestSend(ctx context.Context, req TestRequest) (*model.DeliveryAttempt, error) {
	if s == nil || s.db == nil || s.sender == nil {
		return nil, errors.New("gateway sender is not configured")
	}
	if req.ChannelID == "" {
		return nil, fmt.Errorf("%w: channel_id is required", ErrInvalidRequest)
	}
	channelQuery := s.db.WithContext(ctx).Where("id = ? AND status = ?", req.ChannelID, common.ChannelStatusEnabled)
	if req.OwnerUserID > 0 {
		channelQuery = channelQuery.Where("user_id = ?", req.OwnerUserID)
	}
	var configured model.Channel
	if err := channelQuery.First(&configured).Error; err != nil {
		return nil, notFound(err)
	}
	req.ChannelName = configured.Name
	req.ChannelType = configured.Type
	if req.SourceID != "" {
		var source model.Source
		sourceQuery := s.db.WithContext(ctx).Where("id = ?", req.SourceID)
		if req.OwnerUserID > 0 {
			sourceQuery = sourceQuery.Where("owner_user_id = ?", req.OwnerUserID)
		}
		if err := sourceQuery.First(&source).Error; err != nil {
			return nil, notFound(err)
		}
		allowed, err := decodeAllowedChannels(source.AllowedChannelIDsJSON)
		if err != nil {
			return nil, err
		}
		if source.Status != model.GatewayStatusActive || !allowed[req.ChannelID] {
			return nil, fmt.Errorf("%w: source is not permitted to use this channel", ErrInvalidRequest)
		}
	} else if req.TargetID != "" {
		return nil, fmt.Errorf("%w: target requires a source", ErrInvalidRequest)
	}
	var target *model.Target
	if req.TargetID != "" {
		var found model.Target
		if err := s.db.WithContext(ctx).Where("id = ? AND source_id = ? AND channel_id = ? AND status = ?", req.TargetID, req.SourceID, req.ChannelID, model.GatewayStatusActive).First(&found).Error; err != nil {
			return nil, notFound(err)
		}
		target = &found
		req.Target = found.ExternalID
	}
	metadata := req.Metadata
	if configured.Type == model.TypeQQBotV2 {
		requestedMode := strings.ToLower(strings.TrimSpace(req.DeliveryMode))
		if requestedMode == "" {
			requestedMode = strings.ToLower(strings.TrimSpace(metadata.Mode))
		}
		if metadata.IsWakeUp {
			if requestedMode != "" && requestedMode != "wakeup" {
				return nil, fmt.Errorf("%w: is_wakeup conflicts with delivery mode", ErrInvalidRequest)
			}
			requestedMode = "wakeup"
		}
		metadata.Mode = effectiveQQBotMode(configured, requestedMode)
		if metadata.Mode != "active" && metadata.Mode != "passive" && metadata.Mode != "wakeup" {
			return nil, fmt.Errorf("%w: unsupported QQBot delivery mode", ErrInvalidRequest)
		}
		metadata.IsWakeUp = metadata.Mode == "wakeup"
		if req.MessageType != "" {
			metadata.MessageType = req.MessageType
		} else {
			req.MessageType = metadata.MessageType
		}
		if metadata.Mode == "passive" || metadata.Mode == "wakeup" {
			passive := metadata.Mode == "passive"
			if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				var err error
				metadata, err = resolveVerifiedQQBotMetadata(tx, req.ChannelID, metadata, time.Now().UTC(), passive)
				return err
			}); err != nil {
				return nil, err
			}
			if metadata.Mode == "wakeup" && metadata.TargetType != "user" {
				return nil, fmt.Errorf("%w: QQBot wakeup is available only for C2C users", ErrInvalidRequest)
			}
			if target != nil && (target.ExternalID != metadata.TargetOpenID || target.TargetType != metadata.TargetType) {
				return nil, fmt.Errorf("%w: selected target does not match the verified QQBot interaction", ErrInvalidRequest)
			}
			if req.Target != "" && req.Target != metadata.TargetOpenID {
				return nil, fmt.Errorf("%w: selected target does not match the verified QQBot interaction", ErrInvalidRequest)
			}
			req.Target = metadata.TargetOpenID
			metadata.MessageSeq = 0 // The callback envelope sequence is not an outbound msg_seq.
		} else {
			// Active messages do not inherit callback identities from caller metadata.
			metadata.EventID = ""
			metadata.MessageID = ""
			metadata.TargetType = ""
			metadata.TargetOpenID = ""
			metadata.ReceivedAt = time.Time{}
			metadata.MessageSeq = 0
		}
		if metadata.MessageType != "" && metadata.MessageType != "text" && metadata.MessageType != "markdown" {
			return nil, fmt.Errorf("%w: QQBot message_type must be text or markdown", ErrInvalidRequest)
		}
	}
	metadataJSON, err := json.Marshal(struct {
		EventID      string    `json:"event_id,omitempty"`
		MessageID    string    `json:"msg_id,omitempty"`
		Mode         string    `json:"mode,omitempty"`
		IsWakeUp     bool      `json:"is_wakeup,omitempty"`
		MessageType  string    `json:"message_type,omitempty"`
		TargetType   string    `json:"target_type,omitempty"`
		TargetOpenID string    `json:"target_openid,omitempty"`
		ReceivedAt   time.Time `json:"received_at,omitempty"`
	}{metadata.EventID, metadata.MessageID, metadata.Mode, metadata.IsWakeUp, metadata.MessageType, metadata.TargetType, metadata.TargetOpenID, metadata.ReceivedAt})
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	notification := model.Notification{
		Id: newID(), SourceID: req.SourceID, IdempotencyKey: "test:" + uuid.NewString(),
		RequestHash: "test", Title: req.Title, Content: req.Content, MetadataJSON: string(metadataJSON),
		DeliveryPolicy: PolicyBroadcast, Status: model.NotificationProcessing,
		IsTest: true, CreatedAt: now, UpdatedAt: now,
	}
	delivery := model.Delivery{
		Id: newID(), NotificationID: notification.Id, ChannelID: req.ChannelID,
		ChannelName: req.ChannelName, ChannelType: req.ChannelType, MessageType: req.MessageType,
		MetadataJSON: string(metadataJSON),
		TargetID:     req.TargetID, Target: req.Target, Status: model.DeliveryProcessing,
		AttemptCount: 1, MaxAttempts: 1, DispatchGeneration: 1, CreatedAt: now, UpdatedAt: now,
	}
	attempt := model.DeliveryAttempt{Id: newID(), DeliveryID: delivery.Id, AttemptNumber: 1, AttemptKind: "send", StartedAt: now, Result: "processing", CreatedAt: now}
	if target != nil {
		delivery.Target = target.ExternalID
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if configured.Type == model.TypeQQBotV2 && metadata.Mode == "passive" && !metadata.IsWakeUp {
			trackingID := metadata.MessageID
			withMessageID := trackingID != ""
			if trackingID == "" {
				trackingID = "event:" + metadata.EventID
			}
			sequence, err := allocateQQBotMessageSequence(tx, configured.AppId, trackingID, metadata.TargetType, now)
			if err != nil {
				return err
			}
			if withMessageID {
				delivery.MessageSequence = sequence
			}
		}
		if err := tx.Create(&notification).Error; err != nil {
			return err
		}
		if err := tx.Create(&delivery).Error; err != nil {
			return err
		}
		if err := tx.Create(&attempt).Error; err != nil {
			return err
		}
		return writeAuditTx(tx, actorType(req.ActorType), req.ActorID, req.SourceID, "channel.test_send", "delivery", delivery.Id, "{\"is_test\":true}", now)
	}); err != nil {
		return nil, err
	}

	sendCtx, cancel := context.WithTimeout(ctx, s.opts.SendTimeout)
	defer cancel()
	metadata.MessageSeq = delivery.MessageSequence
	sendCtx = WithDeliveryMetadata(sendCtx, metadata)
	result, sendErr := s.sender.Send(sendCtx, &notification, &delivery, target)
	finished := time.Now().UTC()
	attempt.FinishedAt = &finished
	attempt.DurationMS = finished.Sub(now).Milliseconds()
	attempt.HTTPStatus = result.HTTPStatus
	attempt.ProviderErrorCode = result.ProviderCode
	attempt.ProviderMessageID = result.ProviderMessageID
	status := model.DeliveryAccepted
	attemptCount := 1
	if sendErr != nil {
		classification := classifyError(sendErr)
		attempt.Result = classification.attemptResult
		if attempt.HTTPStatus == 0 {
			attempt.HTTPStatus = classification.httpStatus
		}
		attempt.ProviderErrorCode = firstNonEmpty(classification.providerCode, attempt.ProviderErrorCode)
		attempt.ErrorCategory = classification.category
		attempt.ErrorMessage = safeErrorMessage(sendErr)
		status = classification.deliveryState
		if !classification.attempted && !classification.unknown {
			attempt.AttemptKind = "preflight"
			attemptCount = 0
		}
	} else {
		attempt.Result = "accepted"
	}
	notificationStatus := status
	if status == model.DeliveryUnknown {
		notificationStatus = model.NotificationUnknown
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var lockedNotification model.Notification
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", notification.Id).First(&lockedNotification).Error; err != nil {
			return err
		}
		var lockedDelivery model.Delivery
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", delivery.Id).First(&lockedDelivery).Error; err != nil {
			return err
		}
		if err := tx.Model(&attempt).Updates(map[string]interface{}{
			"finished_at": finished, "duration_ms": attempt.DurationMS, "http_status": attempt.HTTPStatus,
			"attempt_kind":        attempt.AttemptKind,
			"provider_error_code": attempt.ProviderErrorCode, "provider_message_id": attempt.ProviderMessageID,
			"error_category": attempt.ErrorCategory, "error_message": attempt.ErrorMessage, "result": attempt.Result,
		}).Error; err != nil {
			return err
		}
		if err := tx.Model(&delivery).Updates(map[string]interface{}{"status": status, "attempt_count": attemptCount, "completed_at": finished, "updated_at": finished,
			"last_error_code": attempt.ProviderErrorCode, "last_error_category": attempt.ErrorCategory, "last_error_message": attempt.ErrorMessage}).Error; err != nil {
			return err
		}
		return tx.Model(&notification).Updates(map[string]interface{}{"status": notificationStatus, "completed_at": finished, "updated_at": finished}).Error
	}); err != nil {
		return nil, err
	}
	if sendErr != nil {
		return &attempt, sendErr
	}
	return &attempt, nil
}

func validateOwnedChannels(tx *gorm.DB, ownerUserID int, channelIDs []string) error {
	for _, channelID := range uniqueStrings(channelIDs) {
		var channel model.Channel
		if err := tx.Where("id = ? AND user_id = ? AND status = ?", channelID, ownerUserID, common.ChannelStatusEnabled).First(&channel).Error; err != nil {
			return fmt.Errorf("%w: channel %q is not an enabled channel owned by this user", ErrInvalidRequest, channelID)
		}
	}
	return nil
}

func (s *Service) ListAuditLogs(ctx context.Context, filter AuditFilter) ([]model.AuditLog, int64, error) {
	query := s.db.WithContext(ctx).Model(&model.AuditLog{})
	if filter.OwnerUserID != 0 {
		query = query.Joins("JOIN gateway_sources ON gateway_sources.id = gateway_audit_logs.source_id").Where("gateway_sources.owner_user_id = ?", filter.OwnerUserID)
	}
	if filter.SourceID != "" {
		query = query.Where("gateway_audit_logs.source_id = ?", filter.SourceID)
	}
	if filter.ActorID != "" {
		query = query.Where("gateway_audit_logs.actor_id = ?", filter.ActorID)
	}
	if filter.Action != "" {
		query = query.Where("gateway_audit_logs.action = ?", filter.Action)
	}
	if filter.ResourceType != "" {
		query = query.Where("gateway_audit_logs.resource_type = ?", filter.ResourceType)
	}
	if filter.From != nil {
		query = query.Where("gateway_audit_logs.created_at >= ?", *filter.From)
	}
	if filter.To != nil {
		query = query.Where("gateway_audit_logs.created_at <= ?", *filter.To)
	}
	var total int64
	if err := query.Distinct("gateway_audit_logs.id").Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page, size := normalizedPage(filter.Page, filter.PageSize)
	var logs []model.AuditLog
	err := query.Select("gateway_audit_logs.*").Distinct().Order("gateway_audit_logs.created_at DESC").Limit(size).Offset((page - 1) * size).Find(&logs).Error
	return logs, total, err
}

func (s *Service) Stats(ctx context.Context, sourceID string, ownerUserID int) (*Stats, error) {
	stats := &Stats{}
	notifications := s.db.WithContext(ctx).Model(&model.Notification{}).Where("gateway_notifications.is_test = ?", false)
	deliveries := s.db.WithContext(ctx).Model(&model.Delivery{}).
		Joins("JOIN gateway_notifications ON gateway_notifications.id = gateway_deliveries.notification_id").
		Where("gateway_notifications.is_test = ?", false)
	if sourceID != "" {
		notifications = notifications.Where("source_id = ?", sourceID)
		deliveries = deliveries.Where("gateway_notifications.source_id = ?", sourceID)
	}
	if ownerUserID != 0 {
		notifications = notifications.Joins("JOIN gateway_sources ON gateway_sources.id = gateway_notifications.source_id").Where("gateway_sources.owner_user_id = ?", ownerUserID)
		deliveries = deliveries.Joins("JOIN gateway_sources ON gateway_sources.id = gateway_notifications.source_id").Where("gateway_sources.owner_user_id = ?", ownerUserID)
	}
	if err := notifications.Count(&stats.Notifications).Error; err != nil {
		return nil, err
	}
	dayStart := time.Now().UTC()
	dayStart = time.Date(dayStart.Year(), dayStart.Month(), dayStart.Day(), 0, 0, 0, 0, time.UTC)
	countNotifications := func(start, end time.Time, status string) (int64, error) {
		query := notifications.Where("gateway_notifications.created_at >= ? AND gateway_notifications.created_at < ?", start, end)
		if status != "" {
			query = query.Where("gateway_notifications.status = ?", status)
		}
		var count int64
		err := query.Count(&count).Error
		return count, err
	}
	var err error
	stats.TodayReceived, err = countNotifications(dayStart, dayStart.Add(24*time.Hour), "")
	if err != nil {
		return nil, err
	}
	stats.TodayAccepted, err = countNotifications(dayStart, dayStart.Add(24*time.Hour), model.NotificationAccepted)
	if err != nil {
		return nil, err
	}
	stats.TodayFailed, err = countNotifications(dayStart, dayStart.Add(24*time.Hour), model.NotificationFailed)
	if err != nil {
		return nil, err
	}
	stats.TodayRetrying, err = countNotifications(dayStart, dayStart.Add(24*time.Hour), model.NotificationRetrying)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Status string
		Count  int64
	}
	if err := deliveries.Select("gateway_deliveries.status, COUNT(*) AS count").Group("gateway_deliveries.status").Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		switch row.Status {
		case model.DeliveryAccepted:
			stats.Accepted = row.Count
		case model.DeliveryFailed:
			stats.Failed = row.Count
		case model.DeliveryRetrying:
			stats.Retrying = row.Count
		case model.DeliveryUnknown:
			stats.Unknown = row.Count
		case model.DeliveryQueued, model.DeliveryPendingDispatch:
			stats.Queued += row.Count
		case model.DeliveryDeadLetter:
			stats.DeadLetter = row.Count
		}
	}
	var byChannel []ChannelStats
	err = deliveries.Select(`gateway_deliveries.channel_id, gateway_deliveries.channel_name,
		COUNT(DISTINCT gateway_deliveries.id) AS total,
		COUNT(DISTINCT CASE WHEN gateway_deliveries.status = ? THEN gateway_deliveries.id END) AS accepted,
		COUNT(DISTINCT CASE WHEN gateway_deliveries.status IN (?, ?) THEN gateway_deliveries.id END) AS failed,
		COUNT(DISTINCT CASE WHEN gateway_deliveries.status = ? THEN gateway_deliveries.id END) AS retrying,
		AVG(CASE WHEN gateway_delivery_attempts.attempt_kind = ? THEN gateway_delivery_attempts.duration_ms END) AS average_duration_ms,
		AVG(CASE WHEN gateway_delivery_attempts.attempt_kind = ? THEN gateway_delivery_attempts.duration_ms END) AS avg_duration_ms`,
		model.DeliveryAccepted, model.DeliveryFailed, model.DeliveryDeadLetter, model.DeliveryRetrying, "send", "send").
		Joins("LEFT JOIN gateway_delivery_attempts ON gateway_delivery_attempts.delivery_id = gateway_deliveries.id").
		Group("gateway_deliveries.channel_id, gateway_deliveries.channel_name").Scan(&byChannel).Error
	if err != nil {
		return nil, err
	}
	for i := range byChannel {
		if byChannel[i].Total > 0 {
			byChannel[i].SuccessRate = float64(byChannel[i].Accepted) / float64(byChannel[i].Total)
		}
		byChannel[i].AvgDurationMS = byChannel[i].AverageDurationMS
	}
	stats.ByChannel = byChannel

	stats.Last24Hours = make([]TrendBucket, 0, 24)
	now := time.Now().UTC().Truncate(time.Hour)
	for i := 23; i >= 0; i-- {
		start := now.Add(-time.Duration(i) * time.Hour)
		var hourly []struct {
			Status string
			Count  int64
		}
		query := notifications.Where("gateway_notifications.created_at >= ? AND gateway_notifications.created_at < ?", start, start.Add(time.Hour))
		if err := query.Select("gateway_notifications.status AS status, COUNT(*) AS count").Group("gateway_notifications.status").Scan(&hourly).Error; err != nil {
			return nil, err
		}
		bucket := TrendBucket{Hour: start}
		for _, row := range hourly {
			bucket.Received += row.Count
			switch row.Status {
			case model.NotificationAccepted:
				bucket.Accepted = row.Count
			case model.NotificationFailed:
				bucket.Failed = row.Count
			}
		}
		stats.Last24Hours = append(stats.Last24Hours, bucket)
	}

	stats.RecentErrors = make([]StatsRecentError, 0, 10)
	errorQuery := deliveries.Select(`gateway_deliveries.id AS delivery_id, gateway_notifications.source_id AS source_id,
		gateway_deliveries.channel_id AS channel_id, gateway_deliveries.channel_name AS channel_name,
		gateway_deliveries.status AS status, gateway_deliveries.last_error_code AS error_code,
		gateway_deliveries.last_error_message AS error, gateway_deliveries.updated_at AS updated_at`).
		Where("gateway_deliveries.status IN ?", []string{model.DeliveryFailed, model.DeliveryDeadLetter, model.DeliveryUnknown}).
		Order("gateway_deliveries.updated_at DESC").Limit(10).Scan(&stats.RecentErrors).Error
	if errorQuery != nil {
		return nil, errorQuery
	}
	if s.rdb != nil {
		if length, queueErr := s.rdb.XLen(ctx, s.opts.StreamName).Result(); queueErr == nil {
			stats.QueueBacklog = length
			stats.QueueBacklogAvailable = true
			if pending, pendingErr := s.rdb.XPending(ctx, s.opts.StreamName, s.opts.ConsumerGroup).Result(); pendingErr == nil && pending != nil {
				stats.PendingMessages = pending.Count
			}
		}
	}
	return stats, nil
}

func (s *Service) writeAudit(ctx context.Context, actorType, actorID, sourceID, action, resourceType, resourceID, details string) error {
	return s.db.WithContext(ctx).Create(&model.AuditLog{
		Id: newID(), ActorType: actorType, ActorID: actorID, SourceID: sourceID,
		Action: action, ResourceType: resourceType, ResourceID: resourceID,
		DetailsJSON: details, CreatedAt: time.Now().UTC(),
	}).Error
}

func writeAuditTx(tx *gorm.DB, actorType, actorID, sourceID, action, resourceType, resourceID, details string, now time.Time) error {
	return tx.Create(&model.AuditLog{
		Id: newID(), ActorType: actorType, ActorID: actorID, SourceID: sourceID,
		Action: action, ResourceType: resourceType, ResourceID: resourceID,
		DetailsJSON: details, CreatedAt: now,
	}).Error
}

func generateAPIKey() (string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	return "mp_" + base64.RawURLEncoding.EncodeToString(secret), nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func notFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}

func actorIDOr(id, fallback string) string {
	if strings.TrimSpace(id) == "" {
		return fallback
	}
	return id
}

func actorType(value string) string {
	if value == "" {
		return "user"
	}
	return value
}

func (s *Service) Resend(ctx context.Context, sourceID, notificationID, actorID string) (*model.Notification, error) {
	return s.resendNotification(ctx, 0, sourceID, notificationID, actorID)
}

func (s *Service) resendNotification(ctx context.Context, ownerUserID int, sourceID, notificationID, actorID string) (*model.Notification, error) {
	var resend *model.Notification
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Where("id = ?", notificationID)
		if sourceID != "" {
			query = query.Where("source_id = ?", sourceID)
		}
		if ownerUserID != 0 {
			query = query.Where("source_id IN (SELECT id FROM gateway_sources WHERE owner_user_id = ?)", ownerUserID)
		}
		var original model.Notification
		if err := query.First(&original).Error; err != nil {
			return notFound(err)
		}
		var deliveries []model.Delivery
		if err := tx.Where("notification_id = ?", original.Id).Order("route_order ASC, created_at ASC").Find(&deliveries).Error; err != nil {
			return err
		}
		specs := make([]DeliverySpec, 0, len(deliveries))
		for _, delivery := range deliveries {
			var backoff []int
			if delivery.RetryBackoffJSON != "" {
				if err := json.Unmarshal([]byte(delivery.RetryBackoffJSON), &backoff); err != nil {
					return fmt.Errorf("decode delivery retry policy: %w", err)
				}
			}
			specs = append(specs, DeliverySpec{
				ChannelID: delivery.ChannelID, ChannelName: delivery.ChannelName,
				ChannelType: delivery.ChannelType, MessageType: delivery.MessageType,
				Target: delivery.Target, RouteOrder: delivery.RouteOrder,
				MaxAttempts: delivery.MaxAttempts, RetryBackoffSeconds: backoff,
				DeliveryMetadata: json.RawMessage(delivery.MetadataJSON),
			})
		}
		req := EnqueueRequest{
			SourceID: original.SourceID, IdempotencyKey: "manual-resend:" + uuid.NewString(),
			RequestID: original.RequestID, Title: original.Title, Content: original.Content,
			Priority: original.Priority, DeliveryPolicy: original.DeliveryPolicy,
			Metadata: []byte(original.MetadataJSON), Deliveries: specs,
		}
		req, hash, err := prepareEnqueueRequest(req)
		if err != nil {
			return err
		}
		created, _, err := s.enqueueTx(tx, req, hash)
		if err != nil {
			return err
		}
		resend = created
		return writeAuditTx(tx, "user", actorID, original.SourceID, "notification.resend", "notification", created.Id, "{\"is_resend\":true}", time.Now().UTC())
	})
	if err != nil {
		return nil, err
	}
	return resend, nil
}

func (s *Service) Migrate(db *gorm.DB) error { return model.MigrateGateway(db) }

func Migrate(db *gorm.DB) error { return model.MigrateGateway(db) }
