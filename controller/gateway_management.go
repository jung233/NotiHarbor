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

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"message-pusher/common"
	"message-pusher/gateway"
	"message-pusher/model"
)

type gatewaySourceResponse struct {
	ID                 string    `json:"id"`
	OwnerUserID        int       `json:"owner_user_id,omitempty"`
	Name               string    `json:"name"`
	Description        string    `json:"description,omitempty"`
	Status             string    `json:"status"`
	RateLimitPerMinute int       `json:"rate_limit_per_minute"`
	AllowedChannelIDs  []string  `json:"allowed_channel_ids"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type gatewayAPIKeyResponse struct {
	ID         string     `json:"id"`
	SourceID   string     `json:"source_id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	Status     string     `json:"status"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

type gatewayAPIKeyRequest struct {
	Name      string     `json:"name"`
	Scopes    []string   `json:"scopes"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type gatewaySourcePatchRequest struct {
	Name               *string   `json:"name,omitempty"`
	Description        *string   `json:"description,omitempty"`
	Status             *string   `json:"status,omitempty"`
	RateLimitPerMinute *int      `json:"rate_limit_per_minute,omitempty"`
	AllowedChannelIDs  *[]string `json:"allowed_channel_ids,omitempty"`
}

type gatewayTargetPatchRequest struct {
	ChannelID  *string `json:"channel_id,omitempty"`
	TargetType *string `json:"target_type,omitempty"`
	ExternalID *string `json:"external_id,omitempty"`
	Name       *string `json:"name,omitempty"`
	Source     *string `json:"source,omitempty"`
	Status     *string `json:"status,omitempty"`
	IsDefault  *bool   `json:"is_default,omitempty"`
}

type gatewayChannelResponse struct {
	ID          int    `json:"id"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      int    `json:"status"`
}

type gatewayTestRequest struct {
	SourceID     string `json:"source_id"`
	TargetID     string `json:"target_id,omitempty"`
	Title        string `json:"title,omitempty"`
	Content      string `json:"content"`
	MessageType  string `json:"message_type,omitempty"`
	DeliveryMode string `json:"delivery_mode,omitempty"`
	IsTest       bool   `json:"is_test,omitempty"`
}

var errGatewayInteractionRequired = errors.New("interact with this QQBot target before testing a passive reply")

func sourceResponse(source *model.Source) (gatewaySourceResponse, error) {
	var allowed []string
	if source.AllowedChannelIDsJSON != "" {
		if err := json.Unmarshal([]byte(source.AllowedChannelIDsJSON), &allowed); err != nil {
			return gatewaySourceResponse{}, err
		}
	}
	return gatewaySourceResponse{
		ID: source.Id, OwnerUserID: source.OwnerUserID, Name: source.Name,
		Description: source.Description, Status: source.Status,
		RateLimitPerMinute: source.RateLimitPerMinute, AllowedChannelIDs: allowed,
		CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt,
	}, nil
}

func apiKeyResponse(key *model.APIKey) (gatewayAPIKeyResponse, error) {
	var scopes []string
	if key.ScopesJSON != "" {
		if err := json.Unmarshal([]byte(key.ScopesJSON), &scopes); err != nil {
			return gatewayAPIKeyResponse{}, err
		}
	}
	return gatewayAPIKeyResponse{
		ID: key.Id, SourceID: key.SourceID, Name: key.Name, Prefix: key.Prefix,
		Scopes: scopes, Status: key.Status, ExpiresAt: key.ExpiresAt,
		LastUsedAt: key.LastUsedAt, CreatedAt: key.CreatedAt,
	}, nil
}

func ListGatewaySources(c *gin.Context) {
	service, ok := gatewayService(c)
	if !ok {
		return
	}
	page, pageSize := gatewayPage(c)
	sources, total, err := service.ListSources(c.Request.Context(), gatewayOwnerID(c), pageSize, (page-1)*pageSize)
	if err != nil {
		gatewayFailure(c, http.StatusInternalServerError, "sources could not be loaded")
		return
	}
	items := make([]gatewaySourceResponse, 0, len(sources))
	for i := range sources {
		item, err := sourceResponse(&sources[i])
		if err != nil {
			gatewayFailure(c, http.StatusInternalServerError, "source permissions could not be decoded")
			return
		}
		items = append(items, item)
	}
	gatewaySuccess(c, http.StatusOK, gatewayPageData(items, total, page, pageSize))
}

func CreateGatewaySource(c *gin.Context) {
	service, ok := gatewayService(c)
	if !ok {
		return
	}
	var input gateway.SourceInput
	if err := bindGatewayJSON(c, &input); err != nil || !gatewayBoundedString(input.Name, 191) {
		gatewayFailure(c, http.StatusBadRequest, "name is required and must not exceed 191 characters")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.RateLimitPerMinute < 0 {
		gatewayFailure(c, http.StatusBadRequest, "rate_limit_per_minute cannot be negative")
		return
	}
	if err := validateSourceChannels(c.Request.Context(), input.AllowedChannelIDs, c.GetInt("id")); err != nil {
		if errors.Is(err, errGatewayDataUnavailable) {
			gatewayFailure(c, http.StatusServiceUnavailable, "channel ownership could not be verified")
		} else {
			gatewayFailure(c, http.StatusBadRequest, err.Error())
		}
		return
	}
	source, err := service.CreateSource(c.Request.Context(), c.GetInt("id"), input)
	if err != nil {
		gatewayMutationFailure(c, err, http.StatusBadRequest, "source request is invalid", "source not found", "source could not be created")
		return
	}
	response, err := sourceResponse(source)
	if err != nil {
		gatewayFailure(c, http.StatusInternalServerError, "source was created but its permissions could not be decoded")
		return
	}
	gatewaySuccess(c, http.StatusCreated, response)
}

func GetGatewaySource(c *gin.Context) {
	service, ok := gatewayService(c)
	if !ok {
		return
	}
	source, err := service.GetSource(c.Request.Context(), gatewayOwnerID(c), gatewayParseIDParam(c, "sourceID"))
	if err != nil {
		gatewayLookupFailure(c, err, "source not found")
		return
	}
	response, err := sourceResponse(source)
	if err != nil {
		gatewayFailure(c, http.StatusInternalServerError, "source permissions could not be decoded")
		return
	}
	gatewaySuccess(c, http.StatusOK, response)
}

func UpdateGatewaySource(c *gin.Context) {
	service, ok := gatewayService(c)
	if !ok {
		return
	}
	var patch gatewaySourcePatchRequest
	if err := bindGatewayJSON(c, &patch); err != nil {
		gatewayFailure(c, http.StatusBadRequest, "invalid JSON request")
		return
	}
	ownerID := gatewayOwnerID(c)
	existing, err := service.GetSource(c.Request.Context(), ownerID, gatewayParseIDParam(c, "sourceID"))
	if err != nil {
		gatewayLookupFailure(c, err, "source not found")
		return
	}
	input := gateway.SourceInput{
		Name: existing.Name, Description: existing.Description, Status: existing.Status,
		RateLimitPerMinute: existing.RateLimitPerMinute,
	}
	if existing.AllowedChannelIDsJSON != "" {
		if err := json.Unmarshal([]byte(existing.AllowedChannelIDsJSON), &input.AllowedChannelIDs); err != nil {
			gatewayFailure(c, http.StatusInternalServerError, "source permissions could not be decoded")
			return
		}
	}
	if patch.Name != nil {
		input.Name = *patch.Name
	}
	if patch.Description != nil {
		input.Description = *patch.Description
	}
	if patch.Status != nil {
		input.Status = *patch.Status
	}
	if patch.RateLimitPerMinute != nil {
		input.RateLimitPerMinute = *patch.RateLimitPerMinute
	}
	if patch.AllowedChannelIDs != nil {
		input.AllowedChannelIDs = *patch.AllowedChannelIDs
	}
	if !gatewayBoundedString(input.Name, 191) {
		gatewayFailure(c, http.StatusBadRequest, "name is required and must not exceed 191 characters")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.RateLimitPerMinute < 0 {
		gatewayFailure(c, http.StatusBadRequest, "rate_limit_per_minute cannot be negative")
		return
	}
	if input.Status != model.GatewayStatusActive && input.Status != model.GatewayStatusDisabled {
		gatewayFailure(c, http.StatusBadRequest, "source status must be active or disabled")
		return
	}
	if err := validateSourceChannels(c.Request.Context(), input.AllowedChannelIDs, existing.OwnerUserID); err != nil {
		if errors.Is(err, errGatewayDataUnavailable) {
			gatewayFailure(c, http.StatusServiceUnavailable, "channel ownership could not be verified")
		} else {
			gatewayFailure(c, http.StatusBadRequest, err.Error())
		}
		return
	}
	source, err := service.UpdateSource(c.Request.Context(), existing.OwnerUserID, gatewayParseIDParam(c, "sourceID"), input)
	if err != nil {
		gatewayMutationFailure(c, err, http.StatusBadRequest, "source request is invalid", "source not found", "source could not be updated")
		return
	}
	response, err := sourceResponse(source)
	if err != nil {
		gatewayFailure(c, http.StatusInternalServerError, "source permissions could not be decoded")
		return
	}
	gatewaySuccess(c, http.StatusOK, response)
}

func DeleteGatewaySource(c *gin.Context) {
	service, ok := gatewayService(c)
	if !ok {
		return
	}
	sourceID := gatewayParseIDParam(c, "sourceID")
	source, err := service.GetSource(c.Request.Context(), gatewayOwnerID(c), sourceID)
	if err != nil {
		gatewayLookupFailure(c, err, "source not found")
		return
	}
	err = service.DeleteSource(c.Request.Context(), source.OwnerUserID, sourceID, gatewayActorID(c))
	if err != nil {
		gatewayMutationFailure(c, err, http.StatusBadRequest, "source request is invalid", "source not found", "source could not be deleted")
		return
	}
	gatewaySuccess(c, http.StatusOK, gin.H{"deleted": true})
}

func validateSourceChannels(ctx context.Context, channelIDs []string, ownerUserID int) error {
	if len(channelIDs) > 128 {
		return errors.New("a source may authorize at most 128 channels")
	}
	seen := make(map[string]bool, len(channelIDs))
	for _, rawID := range channelIDs {
		id := strings.TrimSpace(rawID)
		numericID, err := strconv.Atoi(id)
		if err != nil || numericID <= 0 {
			return errors.New("allowed_channel_ids must contain positive channel IDs")
		}
		if seen[id] {
			return errors.New("allowed_channel_ids contains a duplicate")
		}
		seen[id] = true
		query := model.DB.WithContext(ctx).Select("id", "user_id", "status").Where("id = ?", numericID)
		if ownerUserID != 0 {
			query = query.Where("user_id = ?", ownerUserID)
		}
		var channel model.Channel
		err := query.First(&channel).Error
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return errGatewayDataUnavailable
			}
			return errors.New("allowed_channel_ids contains an unavailable channel")
		}
		if channel.Status != common.ChannelStatusEnabled {
			return errors.New("allowed_channel_ids contains an unavailable channel")
		}
	}
	return nil
}

func ListGatewayAPIKeys(c *gin.Context) {
	service, ok := gatewayService(c)
	if !ok {
		return
	}
	sourceID := gatewayParseIDParam(c, "sourceID")
	source, err := service.GetSource(c.Request.Context(), gatewayOwnerID(c), sourceID)
	if err != nil {
		gatewayLookupFailure(c, err, "source not found")
		return
	}
	keys, err := service.ListAPIKeys(c.Request.Context(), source.OwnerUserID, sourceID)
	if err != nil {
		gatewayFailure(c, http.StatusInternalServerError, "API keys could not be loaded")
		return
	}
	items := make([]gatewayAPIKeyResponse, 0, len(keys))
	for i := range keys {
		item, err := apiKeyResponse(&keys[i])
		if err != nil {
			gatewayFailure(c, http.StatusInternalServerError, "API key scopes could not be decoded")
			return
		}
		items = append(items, item)
	}
	gatewaySuccess(c, http.StatusOK, gin.H{"items": items})
}

func CreateGatewayAPIKey(c *gin.Context) {
	service, ok := gatewayService(c)
	if !ok {
		return
	}
	var request gatewayAPIKeyRequest
	if err := bindGatewayJSON(c, &request); err != nil || !gatewayBoundedString(request.Name, 191) {
		gatewayFailure(c, http.StatusBadRequest, "name is required and must not exceed 191 characters")
		return
	}
	if len(request.Scopes) == 0 {
		request.Scopes = []string{gateway.ScopeNotificationsWrite, gateway.ScopeNotificationsRead}
	}
	if err := validateGatewayScopes(request.Scopes); err != nil {
		gatewayFailure(c, http.StatusBadRequest, err.Error())
		return
	}
	sourceID := gatewayParseIDParam(c, "sourceID")
	source, err := service.GetSource(c.Request.Context(), gatewayOwnerID(c), sourceID)
	if err != nil {
		gatewayLookupFailure(c, err, "source not found")
		return
	}
	key, plaintext, err := service.CreateAPIKey(c.Request.Context(), source.OwnerUserID, sourceID, strings.TrimSpace(request.Name), request.Scopes, request.ExpiresAt)
	if err != nil {
		gatewayMutationFailure(c, err, http.StatusBadRequest, "API key request is invalid", "source not found", "API key could not be created")
		return
	}
	response, err := apiKeyResponse(key)
	if err != nil {
		gatewayFailure(c, http.StatusInternalServerError, "API key was created but its scopes could not be decoded")
		return
	}
	// This is the only response containing the API key plaintext.
	gatewaySuccess(c, http.StatusCreated, gin.H{"key": response, "api_key": plaintext})
}

func RotateGatewayAPIKey(c *gin.Context) {
	service, ok := gatewayService(c)
	if !ok {
		return
	}
	var request gatewayAPIKeyRequest
	if err := bindGatewayJSON(c, &request); err != nil {
		gatewayFailure(c, http.StatusBadRequest, "invalid JSON request")
		return
	}
	if len(request.Scopes) > 0 {
		if err := validateGatewayScopes(request.Scopes); err != nil {
			gatewayFailure(c, http.StatusBadRequest, err.Error())
			return
		}
	}
	sourceID := gatewayParseIDParam(c, "sourceID")
	source, err := service.GetSource(c.Request.Context(), gatewayOwnerID(c), sourceID)
	if err != nil {
		gatewayLookupFailure(c, err, "source not found")
		return
	}
	key, plaintext, err := service.RotateAPIKey(c.Request.Context(), source.OwnerUserID, sourceID, gatewayParseIDParam(c, "keyID"), gatewayActorID(c), request.Scopes, request.ExpiresAt)
	if err != nil {
		gatewayMutationFailure(c, err, http.StatusBadRequest, "API key request is invalid", "API key or source not found", "API key could not be rotated")
		return
	}
	response, err := apiKeyResponse(key)
	if err != nil {
		gatewayFailure(c, http.StatusInternalServerError, "API key was rotated but its scopes could not be decoded")
		return
	}
	gatewaySuccess(c, http.StatusOK, gin.H{"key": response, "api_key": plaintext})
}

func RevokeGatewayAPIKey(c *gin.Context) {
	service, ok := gatewayService(c)
	if !ok {
		return
	}
	sourceID := gatewayParseIDParam(c, "sourceID")
	source, err := service.GetSource(c.Request.Context(), gatewayOwnerID(c), sourceID)
	if err != nil {
		gatewayLookupFailure(c, err, "source not found")
		return
	}
	err = service.RevokeAPIKey(c.Request.Context(), source.OwnerUserID, sourceID, gatewayParseIDParam(c, "keyID"), gatewayActorID(c))
	if err != nil {
		gatewayMutationFailure(c, err, http.StatusBadRequest, "API key request is invalid", "API key or source not found", "API key could not be revoked")
		return
	}
	gatewaySuccess(c, http.StatusOK, gin.H{"revoked": true})
}

func validateGatewayScopes(scopes []string) error {
	if len(scopes) == 0 || len(scopes) > 16 {
		return errors.New("one to sixteen API key scopes are required")
	}
	allowed := map[string]bool{
		gateway.ScopeNotificationsWrite: true,
		gateway.ScopeNotificationsRead:  true,
		"notifications:retry":           true,
		"notifications:cancel":          true,
		"channels:read":                 true,
		"channels:test":                 true,
		"logs:read":                     true,
	}
	seen := make(map[string]bool, len(scopes))
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if !allowed[scope] || seen[scope] {
			return fmt.Errorf("unsupported or duplicate API key scope %q", scope)
		}
		seen[scope] = true
	}
	return nil
}

func ListGatewayTargets(c *gin.Context) {
	service, ok := gatewayService(c)
	if !ok {
		return
	}
	sourceID := gatewayParseIDParam(c, "sourceID")
	source, err := service.GetSource(c.Request.Context(), gatewayOwnerID(c), sourceID)
	if err != nil {
		gatewayLookupFailure(c, err, "source not found")
		return
	}
	targets, err := service.ListTargets(c.Request.Context(), source.OwnerUserID, sourceID)
	if err != nil {
		gatewayFailure(c, http.StatusInternalServerError, "targets could not be loaded")
		return
	}
	gatewaySuccess(c, http.StatusOK, targets)
}

func CreateGatewayTarget(c *gin.Context) {
	upsertGatewayTarget(c, "")
}

func UpdateGatewayTarget(c *gin.Context) {
	upsertGatewayTarget(c, gatewayParseIDParam(c, "targetID"))
}

func upsertGatewayTarget(c *gin.Context, targetID string) {
	service, ok := gatewayService(c)
	if !ok {
		return
	}
	sourceID := gatewayParseIDParam(c, "sourceID")
	source, err := service.GetSource(c.Request.Context(), gatewayOwnerID(c), sourceID)
	if err != nil {
		gatewayLookupFailure(c, err, "source not found")
		return
	}
	ownerID := source.OwnerUserID
	var patch gatewayTargetPatchRequest
	if err := bindGatewayJSON(c, &patch); err != nil {
		gatewayFailure(c, http.StatusBadRequest, "invalid JSON request")
		return
	}
	input := gateway.TargetInput{Id: targetID, SourceID: sourceID}
	if targetID != "" {
		targets, err := service.ListTargets(c.Request.Context(), ownerID, sourceID)
		if err != nil {
			gatewayFailure(c, http.StatusInternalServerError, "targets could not be loaded")
			return
		}
		found := false
		for _, target := range targets {
			if target.Id == targetID {
				input.ChannelID = target.ChannelID
				input.TargetType = target.TargetType
				input.ExternalID = target.ExternalID
				input.Name = target.Name
				input.Source = target.Source
				input.Status = target.Status
				input.LastInteractionAt = target.LastInteractionAt
				input.IsDefault = target.IsDefault
				found = true
				break
			}
		}
		if !found {
			gatewayFailure(c, http.StatusNotFound, "target not found")
			return
		}
	}
	if patch.ChannelID != nil {
		input.ChannelID = *patch.ChannelID
	}
	if patch.TargetType != nil {
		input.TargetType = *patch.TargetType
	}
	if patch.ExternalID != nil {
		input.ExternalID = *patch.ExternalID
	}
	if patch.Name != nil {
		input.Name = *patch.Name
	}
	if patch.Source != nil {
		input.Source = *patch.Source
	}
	if patch.Status != nil {
		input.Status = *patch.Status
	}
	if patch.IsDefault != nil {
		input.IsDefault = *patch.IsDefault
	}
	if input.Status == "" {
		input.Status = model.GatewayStatusActive
	}
	input.ChannelID = strings.TrimSpace(input.ChannelID)
	input.TargetType = strings.TrimSpace(input.TargetType)
	input.ExternalID = strings.TrimSpace(input.ExternalID)
	input.Name = strings.TrimSpace(input.Name)
	input.Source = strings.TrimSpace(input.Source)
	if input.Status != model.GatewayStatusActive && input.Status != model.GatewayStatusDisabled {
		gatewayFailure(c, http.StatusBadRequest, "target status must be active or disabled")
		return
	}
	if !gatewayBoundedString(input.ChannelID, 191) || !gatewayBoundedString(input.TargetType, 64) || !gatewayBoundedString(input.ExternalID, 191) {
		gatewayFailure(c, http.StatusBadRequest, "channel_id, target_type, and external_id are required")
		return
	}
	channelID, err := strconv.Atoi(input.ChannelID)
	if err != nil || channelID <= 0 {
		gatewayFailure(c, http.StatusBadRequest, "channel_id must identify an existing channel")
		return
	}
	channel, err := ownedGatewayChannel(c.Request.Context(), channelID, ownerID)
	if err != nil {
		gatewayLookupFailure(c, err, "channel not found")
		return
	}
	allowed, err := service.SourceAllowsChannel(c.Request.Context(), sourceID, input.ChannelID)
	if err != nil {
		gatewayFailure(c, http.StatusServiceUnavailable, "source channel permissions could not be checked")
		return
	}
	if !allowed || channel.Status != common.ChannelStatusEnabled {
		gatewayFailure(c, http.StatusForbidden, "source is not authorized to manage targets for this channel")
		return
	}
	if len(input.Name) > 191 || len(input.Source) > 64 {
		gatewayFailure(c, http.StatusBadRequest, "target name or source is too long")
		return
	}
	target, err := service.UpsertTarget(c.Request.Context(), ownerID, input)
	if err != nil {
		gatewayMutationFailure(c, err, http.StatusBadRequest, "target request is invalid", "target or source not found", "target could not be saved")
		return
	}
	gatewaySuccess(c, http.StatusOK, target)
}

func DeleteGatewayTarget(c *gin.Context) {
	service, ok := gatewayService(c)
	if !ok {
		return
	}
	sourceID := gatewayParseIDParam(c, "sourceID")
	source, err := service.GetSource(c.Request.Context(), gatewayOwnerID(c), sourceID)
	if err != nil {
		gatewayLookupFailure(c, err, "source not found")
		return
	}
	err = service.DeleteTarget(c.Request.Context(), source.OwnerUserID, sourceID, gatewayParseIDParam(c, "targetID"), gatewayActorID(c))
	if err != nil {
		gatewayMutationFailure(c, err, http.StatusBadRequest, "target request is invalid", "target or source not found", "target could not be deleted")
		return
	}
	gatewaySuccess(c, http.StatusOK, gin.H{"deleted": true})
}

func ListGatewayChannels(c *gin.Context) {
	var ownerID int
	var sourceID string
	if source, ok := gatewayKeySource(c); ok {
		ownerID = source.OwnerUserID
		sourceID = source.Id
	} else {
		ownerID = gatewayOwnerID(c)
		sourceID = strings.TrimSpace(c.Query("source_id"))
		if sourceID != "" {
			service, ok := gatewayService(c)
			if !ok {
				return
			}
			source, err := service.GetSource(c.Request.Context(), gatewayOwnerID(c), sourceID)
			if err != nil {
				gatewayLookupFailure(c, err, "source not found")
				return
			}
			ownerID = source.OwnerUserID
		}
	}
	query := model.DB.WithContext(c.Request.Context()).Select("id", "type", "name", "description", "status", "user_id").Where("status = ?", common.ChannelStatusEnabled)
	if ownerID != 0 {
		query = query.Where("user_id = ?", ownerID)
	}
	var channels []model.Channel
	if err := query.Order("id DESC").Limit(500).Find(&channels).Error; err != nil {
		gatewayFailure(c, http.StatusInternalServerError, "channels could not be loaded")
		return
	}
	allowedIDs := map[string]bool(nil)
	if sourceID != "" {
		service, ok := gatewayService(c)
		if !ok {
			return
		}
		allowedIDs = make(map[string]bool)
		for _, channel := range channels {
			id := strconv.Itoa(channel.Id)
			allowed, err := service.SourceAllowsChannel(c.Request.Context(), sourceID, id)
			if err != nil {
				gatewayFailure(c, http.StatusServiceUnavailable, "source channel permissions could not be checked")
				return
			}
			if allowed {
				allowedIDs[id] = true
			}
		}
	}
	items := make([]gatewayChannelResponse, 0, len(channels))
	for _, channel := range channels {
		if allowedIDs != nil && !allowedIDs[strconv.Itoa(channel.Id)] {
			continue
		}
		items = append(items, gatewayChannelResponse{ID: channel.Id, Type: channel.Type, Name: channel.Name, Description: channel.Description, Status: channel.Status})
	}
	gatewaySuccess(c, http.StatusOK, gin.H{"items": items})
}

func TestGatewayChannel(c *gin.Context) {
	service, ok := gatewayService(c)
	if !ok {
		return
	}
	var request gatewayTestRequest
	if err := bindGatewayJSON(c, &request); err != nil {
		gatewayFailure(c, http.StatusBadRequest, "invalid JSON request")
		return
	}
	request.SourceID = strings.TrimSpace(request.SourceID)
	ownerID := gatewayOwnerID(c)
	if keySource, isAPIKey := gatewayKeySource(c); isAPIKey {
		if request.SourceID != "" && request.SourceID != keySource.Id {
			gatewayFailure(c, http.StatusForbidden, "API key is restricted to its own source")
			return
		}
		request.SourceID = keySource.Id
		ownerID = keySource.OwnerUserID
	}
	if request.SourceID == "" || strings.TrimSpace(request.Content) == "" || len(request.Content) > gatewayRequestLimit {
		gatewayFailure(c, http.StatusBadRequest, "source_id and content are required")
		return
	}
	source, err := service.GetSource(c.Request.Context(), ownerID, request.SourceID)
	if err != nil {
		gatewayLookupFailure(c, err, "source not found")
		return
	}
	channelID, err := strconv.Atoi(gatewayParseIDParam(c, "channelID"))
	if err != nil || channelID <= 0 {
		gatewayFailure(c, http.StatusBadRequest, "invalid channel ID")
		return
	}
	channel, err := ownedGatewayChannel(c.Request.Context(), channelID, source.OwnerUserID)
	if err != nil {
		gatewayLookupFailure(c, err, "channel not found")
		return
	}
	if channel.Status != common.ChannelStatusEnabled {
		gatewayFailure(c, http.StatusNotFound, "channel not found")
		return
	}
	channelIDText := strconv.Itoa(channel.Id)
	allowed, err := service.SourceAllowsChannel(c.Request.Context(), source.Id, channelIDText)
	if err != nil {
		gatewayFailure(c, http.StatusServiceUnavailable, "source channel permissions could not be checked")
		return
	}
	if !allowed {
		gatewayFailure(c, http.StatusForbidden, "source is not authorized to use this channel")
		return
	}
	messageType := strings.TrimSpace(request.MessageType)
	if messageType == "" {
		messageType = "text"
	}
	if messageType != "text" && messageType != "markdown" {
		gatewayFailure(c, http.StatusBadRequest, "message_type must be text or markdown")
		return
	}
	qqSettings := gatewayQQBotSettings{}
	fallbackTarget := channel.AccountId
	if channel.Type == model.TypeQQBotV2 {
		qqSettings, err = parseGatewayQQBotSettings(channel.Other)
		if err != nil {
			gatewayFailure(c, http.StatusBadRequest, "QQBot channel configuration is invalid")
			return
		}
		if strings.TrimSpace(qqSettings.DefaultTarget) != "" {
			fallbackTarget = qqSettings.DefaultTarget
		}
	}
	targetID, targetValue, targetType, err := selectGatewayTarget(c.Request.Context(), service, source.OwnerUserID, source.Id, channelIDText, request.TargetID, fallbackTarget)
	if err != nil {
		if errors.Is(err, errGatewayDataUnavailable) {
			gatewayFailure(c, http.StatusServiceUnavailable, "targets could not be loaded")
		} else {
			gatewayFailure(c, http.StatusBadRequest, err.Error())
		}
		return
	}
	deliveryMode := strings.TrimSpace(request.DeliveryMode)
	if deliveryMode == "" {
		deliveryMode = "active"
	}
	if deliveryMode != "active" && deliveryMode != "passive" && deliveryMode != "wakeup" {
		gatewayFailure(c, http.StatusBadRequest, "delivery_mode must be active, passive, or wakeup")
		return
	}
	var testMetadata gateway.DeliveryMetadata
	if channel.Type == model.TypeQQBotV2 {
		if targetType == "" {
			targetType = qqSettings.TargetType
			if targetType == "" {
				targetType = "user"
			}
		}
		if deliveryMode == "passive" {
			testMetadata, err = latestVerifiedQQBotInteraction(c.Request.Context(), channel, targetType, targetValue, deliveryMode)
			if err != nil {
				if errors.Is(err, errGatewayDataUnavailable) {
					gatewayFailure(c, http.StatusServiceUnavailable, "verified QQBot interactions could not be loaded")
				} else if errors.Is(err, errGatewayInteractionRequired) {
					gatewayFailure(c, http.StatusConflict, err.Error())
				} else {
					gatewayFailure(c, http.StatusBadRequest, err.Error())
				}
				return
			}
		} else if deliveryMode == "wakeup" {
			if targetType != "user" {
				gatewayFailure(c, http.StatusBadRequest, "QQBot interaction recall is supported only for C2C user targets")
				return
			}
			testMetadata, err = latestVerifiedQQBotInteraction(c.Request.Context(), channel, targetType, targetValue, deliveryMode)
			if err != nil {
				if errors.Is(err, errGatewayDataUnavailable) {
					gatewayFailure(c, http.StatusServiceUnavailable, "verified QQBot interactions could not be loaded")
				} else if errors.Is(err, errGatewayInteractionRequired) {
					gatewayFailure(c, http.StatusConflict, "interact with this QQBot user before testing interaction recall")
				} else {
					gatewayFailure(c, http.StatusBadRequest, err.Error())
				}
				return
			}
		} else {
			testMetadata = gateway.DeliveryMetadata{Mode: "active", TargetType: targetType, TargetOpenID: targetValue}
		}
		testMetadata.MessageType = messageType
	} else if deliveryMode != "active" {
		gatewayFailure(c, http.StatusBadRequest, "delivery_mode is only supported for QQBot channels")
		return
	}
	title := strings.TrimSpace(request.Title)
	if title == "" {
		title = channel.Name
	}
	attempt, err := service.TestSend(c.Request.Context(), gateway.TestRequest{
		OwnerUserID: source.OwnerUserID,
		ActorID:     gatewayActorID(c), ActorType: gatewayActorType(c), SourceID: source.Id,
		ChannelID: channelIDText, ChannelName: channel.Name, ChannelType: channel.Type,
		MessageType: messageType, DeliveryMode: deliveryMode, Metadata: testMetadata, TargetID: targetID, Target: targetValue,
		Title: title, Content: request.Content,
	})
	if err != nil && attempt == nil {
		if errors.Is(err, gateway.ErrNotFound) {
			gatewayLookupFailure(c, err, "channel or target not found")
		} else if errors.Is(err, gateway.ErrInvalidRequest) {
			gatewayFailure(c, http.StatusBadRequest, "channel test request is invalid")
		} else {
			gatewayFailure(c, http.StatusInternalServerError, "channel test could not be recorded")
		}
		return
	}
	response := gin.H{"attempt": attempt, "is_test": true, "delivery_id": attempt.DeliveryID}
	var notificationID string
	if err := model.DB.WithContext(c.Request.Context()).Model(&model.Delivery{}).
		Select("notification_id").Where("id = ?", attempt.DeliveryID).Scan(&notificationID).Error; err == nil && notificationID != "" {
		response["notification_id"] = notificationID
	}
	if err != nil {
		response["send_error"] = attempt.ErrorMessage
	}
	gatewaySuccess(c, http.StatusOK, response)
}

func ownedGatewayChannel(ctx context.Context, id, ownerID int) (*model.Channel, error) {
	query := model.DB.WithContext(ctx).Select("id", "type", "user_id", "name", "description", "status", "app_id", "account_id", "other").Where("id = ?", id)
	if ownerID != 0 {
		query = query.Where("user_id = ?", ownerID)
	}
	var channel model.Channel
	err := query.First(&channel).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, gateway.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &channel, nil
}

func selectGatewayTarget(ctx context.Context, service *gateway.Service, ownerUserID int, sourceID, channelID, requestedID, fallback string) (string, string, string, error) {
	targets, err := service.ListTargets(ctx, ownerUserID, sourceID)
	if err != nil {
		return "", "", "", errGatewayDataUnavailable
	}
	var defaultTarget *model.Target
	for i := range targets {
		target := &targets[i]
		if target.ChannelID != channelID || target.Status != model.GatewayStatusActive {
			continue
		}
		if requestedID != "" && target.Id == requestedID {
			return target.Id, target.ExternalID, target.TargetType, nil
		}
		if requestedID == "" && target.IsDefault {
			defaultTarget = target
		}
	}
	if requestedID != "" {
		return "", "", "", errors.New("selected target is unavailable for this channel")
	}
	if defaultTarget != nil {
		return defaultTarget.Id, defaultTarget.ExternalID, defaultTarget.TargetType, nil
	}
	if strings.TrimSpace(fallback) != "" {
		return "", fallback, "", nil
	}
	return "", "", "", errors.New("select an active target before sending a test")
}

type gatewayQQBotSettings struct {
	DefaultTarget string `json:"default_target"`
	TargetType    string `json:"target_type"`
}

func parseGatewayQQBotSettings(encoded string) (gatewayQQBotSettings, error) {
	var settings gatewayQQBotSettings
	if strings.TrimSpace(encoded) == "" {
		return settings, nil
	}
	if err := json.Unmarshal([]byte(encoded), &settings); err != nil {
		return settings, err
	}
	if settings.TargetType != "" && settings.TargetType != "user" && settings.TargetType != "group" {
		return settings, errors.New("target_type must be user or group")
	}
	return settings, nil
}

func latestVerifiedQQBotInteraction(ctx context.Context, configured *model.Channel, targetType, targetOpenID, mode string) (gateway.DeliveryMetadata, error) {
	if configured == nil || configured.Type != model.TypeQQBotV2 {
		return gateway.DeliveryMetadata{}, errors.New("passive replies require a QQBot channel")
	}
	if model.DB == nil {
		return gateway.DeliveryMetadata{}, errGatewayDataUnavailable
	}
	targetType = strings.TrimSpace(targetType)
	targetOpenID = strings.TrimSpace(targetOpenID)
	if targetType != "user" && targetType != "group" {
		return gateway.DeliveryMetadata{}, errors.New("select a target with a user or group target type")
	}
	if targetOpenID == "" {
		return gateway.DeliveryMetadata{}, errors.New("select a target before sending a passive reply")
	}
	if mode != "passive" && mode != "wakeup" {
		return gateway.DeliveryMetadata{}, errors.New("verified QQBot interaction is only used for passive or wakeup mode")
	}
	if mode == "wakeup" && targetType != "user" {
		return gateway.DeliveryMetadata{}, errors.New("QQBot interaction recall is supported only for C2C user targets")
	}
	now := time.Now().UTC()
	window := 5 * time.Minute
	eventTypes := []string(nil)
	switch {
	case mode == "wakeup":
		window = 30 * 24 * time.Hour
		eventTypes = []string{"C2C_MESSAGE_CREATE", "INTERACTION_CREATE"}
	case targetType == "user":
		eventTypes = []string{"C2C_MESSAGE_CREATE", "INTERACTION_CREATE", "C2C_INTERACTION_CREATE", "C2C_MSG_RECEIVE", "FRIEND_ADD"}
	case targetType == "group":
		eventTypes = []string{"GROUP_AT_MESSAGE_CREATE", "GROUP_MESSAGE_CREATE", "INTERACTION_CREATE", "GROUP_ADD_ROBOT", "GROUP_MSG_RECEIVE"}
	}
	var event model.GatewayEvent
	err := model.DB.WithContext(ctx).
		Where("channel_id = ? AND status = ? AND target_type = ? AND target_open_id = ?", strconv.Itoa(configured.Id), "processed", targetType, targetOpenID).
		Where("event_type IN ?", eventTypes).
		Where("received_at >= ? AND received_at <= ?", now.Add(-window), now.Add(5*time.Minute)).
		Order("received_at DESC").First(&event).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return gateway.DeliveryMetadata{}, errGatewayInteractionRequired
	}
	if err != nil {
		return gateway.DeliveryMetadata{}, errGatewayDataUnavailable
	}
	if (mode == "wakeup" && event.EventID == "") || (mode == "passive" && event.EventID == "" && event.MessageID == "") {
		return gateway.DeliveryMetadata{}, errGatewayInteractionRequired
	}
	// MessageSequence is the callback envelope's dispatch cursor. Outbound msg_seq
	// is allocated by the queue from the verified MessageID inside its transaction.
	messageID := event.MessageID
	if mode == "wakeup" {
		// Wakeup uses the verified callback event only. It must not carry a passive
		// msg_id into the outbound request.
		messageID = ""
	}
	return gateway.DeliveryMetadata{
		EventID: event.EventID, MessageID: messageID, Mode: mode, IsWakeUp: mode == "wakeup",
		TargetType: event.TargetType, TargetOpenID: event.TargetOpenID, ReceivedAt: event.ReceivedAt,
	}, nil
}

func GetGatewayDashboard(c *gin.Context) {
	service, ok := gatewayService(c)
	if !ok {
		return
	}
	sourceID := strings.TrimSpace(c.Query("source_id"))
	ownerID := gatewayOwnerID(c)
	if sourceID != "" {
		if _, err := service.GetSource(c.Request.Context(), ownerID, sourceID); err != nil {
			gatewayLookupFailure(c, err, "source not found")
			return
		}
	}
	stats, err := service.Stats(c.Request.Context(), sourceID, ownerID)
	if err != nil {
		gatewayFailure(c, http.StatusInternalServerError, "dashboard statistics could not be loaded")
		return
	}
	gatewaySuccess(c, http.StatusOK, gin.H{
		"stats": stats,
		"today": gin.H{"received": stats.TodayReceived, "accepted": stats.TodayAccepted, "failed": stats.TodayFailed, "retrying": stats.TodayRetrying},
		"trend": stats.Last24Hours, "by_channel": stats.ByChannel, "recent_errors": stats.RecentErrors,
	})
}

func ListGatewayAuditLogs(c *gin.Context) {
	service, ok := gatewayService(c)
	if !ok {
		return
	}
	from, err := parseGatewayTime(c.Query("from"))
	if err != nil {
		gatewayFailure(c, http.StatusBadRequest, err.Error())
		return
	}
	to, err := parseGatewayTime(c.Query("to"))
	if err != nil {
		gatewayFailure(c, http.StatusBadRequest, err.Error())
		return
	}
	page, pageSize := gatewayPage(c)
	filter := gateway.AuditFilter{
		OwnerUserID: gatewayOwnerID(c), SourceID: strings.TrimSpace(c.Query("source_id")),
		ActorID: strings.TrimSpace(c.Query("actor_id")), Action: strings.TrimSpace(c.Query("action")),
		ResourceType: strings.TrimSpace(c.Query("resource_type")), From: from, To: to,
		Page: page, PageSize: pageSize,
	}
	items, total, err := service.ListAuditLogs(c.Request.Context(), filter)
	if err != nil {
		gatewayFailure(c, http.StatusInternalServerError, "audit logs could not be loaded")
		return
	}
	gatewaySuccess(c, http.StatusOK, gatewayPageData(items, total, page, pageSize))
}

func gatewayTimePtr(value time.Time) *time.Time { return &value }
