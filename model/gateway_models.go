package model

import "time"

const (
	GatewayStatusActive   = "active"
	GatewayStatusDisabled = "disabled"
	GatewayStatusRevoked  = "revoked"

	NotificationPendingDispatch = "pending_dispatch"
	NotificationQueued          = "queued"
	NotificationProcessing      = "processing"
	NotificationRetrying        = "retrying"
	NotificationAccepted        = "accepted"
	NotificationFailed          = "failed"
	NotificationCancelled       = "cancelled"
	NotificationUnknown         = "delivery_unknown"

	DeliveryHeld            = "held"
	DeliveryPendingDispatch = "pending_dispatch"
	DeliveryQueued          = "queued"
	DeliveryProcessing      = "processing"
	DeliveryRetrying        = "retrying"
	DeliveryAccepted        = "accepted"
	DeliveryFailed          = "failed"
	DeliveryCancelled       = "cancelled"
	DeliveryUnknown         = "delivery_unknown"
	DeliveryDeadLetter      = "dead_letter"

	OutboxPending    = "pending"
	OutboxPublishing = "publishing"
	OutboxPublished  = "published"
)

// Notification is one logical message accepted from an upstream source.
type Notification struct {
	IdempotencyKey string     `json:"idempotency_key" gorm:"type:varchar(191);uniqueIndex:idx_gateway_source_idempotency,priority:2"`
	Id             string     `json:"id" gorm:"type:char(36);primaryKey"`
	SourceID       string     `json:"source_id" gorm:"type:char(36);not null;uniqueIndex:idx_gateway_source_idempotency,priority:1;index:idx_gateway_notifications_source_created,priority:1"`
	RequestHash    string     `json:"-" gorm:"type:char(64);not null"`
	RequestID      string     `json:"request_id" gorm:"type:varchar(191);index"`
	Title          string     `json:"title" gorm:"type:varchar(512)"`
	Content        string     `json:"content" gorm:"type:longtext"`
	MetadataJSON   string     `json:"metadata,omitempty" gorm:"type:longtext"`
	Priority       string     `json:"priority" gorm:"type:varchar(32);index"`
	DeliveryPolicy string     `json:"delivery_policy" gorm:"type:varchar(32);not null;default:'broadcast'"`
	Status         string     `json:"status" gorm:"type:varchar(32);not null;index"`
	IsTest         bool       `json:"is_test" gorm:"not null;default:false;index"`
	CreatedAt      time.Time  `json:"created_at" gorm:"index:idx_gateway_notifications_source_created,priority:2"`
	UpdatedAt      time.Time  `json:"updated_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

func (Notification) TableName() string { return "gateway_notifications" }

// Delivery is one channel/target execution in a notification route.
type Delivery struct {
	Id                      string     `json:"id" gorm:"type:char(36);primaryKey"`
	NotificationID          string     `json:"notification_id" gorm:"type:char(36);not null;index;index:idx_gateway_delivery_route_order,priority:1"`
	ChannelID               string     `json:"channel_id" gorm:"type:varchar(191);index"`
	ChannelName             string     `json:"channel_name" gorm:"type:varchar(191)"`
	ChannelType             string     `json:"channel_type" gorm:"type:varchar(64)"`
	MessageType             string     `json:"message_type,omitempty" gorm:"type:varchar(64)"`
	MessageSequence         int        `json:"-" gorm:"not null;default:0"`
	MetadataJSON            string     `json:"metadata,omitempty" gorm:"type:longtext"`
	TargetID                string     `json:"target_id" gorm:"type:char(36);index"`
	Target                  string     `json:"target,omitempty" gorm:"type:text"`
	RouteOrder              int        `json:"route_order" gorm:"not null;default:0;index:idx_gateway_delivery_route_order,priority:2"`
	Status                  string     `json:"status" gorm:"type:varchar(32);not null;index"`
	AttemptCount            int        `json:"attempt_count" gorm:"not null;default:0"`
	MaxAttempts             int        `json:"max_attempts" gorm:"not null;default:5"`
	PreparationFailureCount int        `json:"preparation_failure_count" gorm:"not null;default:0"`
	UnknownRetryCount       int        `json:"unknown_retry_count" gorm:"not null;default:0"`
	RetryBackoffJSON        string     `json:"retry_backoff_seconds,omitempty" gorm:"type:varchar(512)"`
	DispatchGeneration      int        `json:"-" gorm:"not null;default:1"`
	NextRetryAt             *time.Time `json:"next_retry_at,omitempty" gorm:"index"`
	LeaseOwner              string     `json:"-" gorm:"type:varchar(191);index"`
	LeaseToken              string     `json:"-" gorm:"type:char(36)"`
	LeaseExpiresAt          *time.Time `json:"lease_expires_at,omitempty" gorm:"index"`
	LastErrorCode           string     `json:"last_error_code,omitempty" gorm:"type:varchar(191)"`
	LastErrorCategory       string     `json:"last_error_category,omitempty" gorm:"type:varchar(64)"`
	LastErrorMessage        string     `json:"last_error_message,omitempty" gorm:"type:text"`
	CompletedAt             *time.Time `json:"completed_at,omitempty"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
}

func (Delivery) TableName() string { return "gateway_deliveries" }

// DeliveryAttempt records one actual call to a channel sender.
type DeliveryAttempt struct {
	Id                string     `json:"id" gorm:"type:char(36);primaryKey"`
	DeliveryID        string     `json:"delivery_id" gorm:"type:char(36);not null;index;uniqueIndex:idx_gateway_attempt_number,priority:1"`
	AttemptNumber     int        `json:"attempt_number" gorm:"not null;uniqueIndex:idx_gateway_attempt_number,priority:2"`
	AttemptKind       string     `json:"attempt_kind" gorm:"type:varchar(32);not null;default:'send';index"`
	StartedAt         time.Time  `json:"started_at"`
	FinishedAt        *time.Time `json:"finished_at,omitempty"`
	DurationMS        int64      `json:"duration_ms" gorm:"not null;default:0"`
	HTTPStatus        int        `json:"http_status,omitempty"`
	ProviderErrorCode string     `json:"provider_error_code,omitempty" gorm:"type:varchar(191)"`
	ErrorCategory     string     `json:"error_category,omitempty" gorm:"type:varchar(64)"`
	ErrorMessage      string     `json:"error_message,omitempty" gorm:"type:text"`
	ProviderMessageID string     `json:"provider_message_id,omitempty" gorm:"type:varchar(191)"`
	Result            string     `json:"result" gorm:"type:varchar(32);not null;index"`
	NextRetryAt       *time.Time `json:"next_retry_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}

func (DeliveryAttempt) TableName() string { return "gateway_delivery_attempts" }

// OutboxEvent is committed with a delivery state transition and published to Redis later.
type OutboxEvent struct {
	Id              string     `json:"id" gorm:"type:char(36);primaryKey"`
	DeliveryID      string     `json:"delivery_id" gorm:"type:char(36);not null;uniqueIndex:idx_gateway_outbox_generation,priority:1;index"`
	Generation      int        `json:"generation" gorm:"not null;uniqueIndex:idx_gateway_outbox_generation,priority:2"`
	EventType       string     `json:"event_type" gorm:"type:varchar(64);not null"`
	Status          string     `json:"status" gorm:"type:varchar(32);not null;index"`
	AvailableAt     time.Time  `json:"available_at" gorm:"index"`
	LeaseOwner      string     `json:"-" gorm:"type:varchar(191)"`
	LeaseToken      string     `json:"-" gorm:"type:char(36)"`
	LeaseExpiresAt  *time.Time `json:"-" gorm:"index"`
	AttemptCount    int        `json:"attempt_count" gorm:"not null;default:0"`
	StreamMessageID string     `json:"stream_message_id,omitempty" gorm:"type:varchar(128)"`
	LastError       string     `json:"last_error,omitempty" gorm:"type:text"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func (OutboxEvent) TableName() string { return "gateway_outbox_events" }

// Source represents an upstream application allowed to submit notifications.
type Source struct {
	Id                    string    `json:"id" gorm:"type:char(36);primaryKey"`
	OwnerUserID           int       `json:"owner_user_id" gorm:"not null;index"`
	Name                  string    `json:"name" gorm:"type:varchar(191);not null"`
	Description           string    `json:"description,omitempty" gorm:"type:text"`
	AllowedChannelIDsJSON string    `json:"allowed_channel_ids,omitempty" gorm:"type:text"`
	Status                string    `json:"status" gorm:"type:varchar(32);not null;default:'active';index"`
	RateLimitPerMinute    int       `json:"rate_limit_per_minute" gorm:"not null;default:0"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

func (Source) TableName() string { return "gateway_sources" }

// APIKey stores a one-way digest. Plaintext keys are only returned at creation/rotation.
type APIKey struct {
	Id         string     `json:"id" gorm:"type:char(36);primaryKey"`
	SourceID   string     `json:"source_id" gorm:"type:char(36);not null;index"`
	Name       string     `json:"name" gorm:"type:varchar(191);not null"`
	Prefix     string     `json:"prefix" gorm:"type:varchar(16);not null;index"`
	KeyHash    string     `json:"-" gorm:"type:char(64);not null;uniqueIndex"`
	ScopesJSON string     `json:"scopes_json" gorm:"type:text;not null"`
	Status     string     `json:"status" gorm:"type:varchar(32);not null;default:'active';index"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty" gorm:"index"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func (APIKey) TableName() string { return "gateway_api_keys" }

// Target stores a channel's external destination, such as an email or platform OpenID.
type Target struct {
	Id                string     `json:"id" gorm:"type:char(36);primaryKey"`
	SourceID          string     `json:"source_id" gorm:"type:char(36);not null;index;uniqueIndex:idx_gateway_target_external,priority:1"`
	ChannelID         string     `json:"channel_id" gorm:"type:varchar(191);not null;uniqueIndex:idx_gateway_target_external,priority:2"`
	TargetType        string     `json:"target_type" gorm:"type:varchar(64);not null;uniqueIndex:idx_gateway_target_external,priority:3"`
	ExternalID        string     `json:"external_id" gorm:"type:varchar(191);not null;uniqueIndex:idx_gateway_target_external,priority:4"`
	Name              string     `json:"name" gorm:"type:varchar(191)"`
	Source            string     `json:"source" gorm:"type:varchar(64)"`
	LastInteractionAt *time.Time `json:"last_interaction_at,omitempty"`
	IsDefault         bool       `json:"is_default" gorm:"not null;default:false"`
	Status            string     `json:"status" gorm:"type:varchar(32);not null;default:'active';index"`
	LastSendStatus    string     `json:"last_send_status,omitempty" gorm:"type:varchar(32)"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

func (Target) TableName() string { return "gateway_targets" }

// AuditLog records administrative and test-send operations without credential material.
type AuditLog struct {
	Id           string    `json:"id" gorm:"type:char(36);primaryKey"`
	ActorType    string    `json:"actor_type" gorm:"type:varchar(32);not null"`
	ActorID      string    `json:"actor_id" gorm:"type:varchar(191);index"`
	SourceID     string    `json:"source_id,omitempty" gorm:"type:char(36);index"`
	Action       string    `json:"action" gorm:"type:varchar(191);not null;index"`
	ResourceType string    `json:"resource_type" gorm:"type:varchar(64);not null"`
	ResourceID   string    `json:"resource_id" gorm:"type:varchar(191);index"`
	RequestID    string    `json:"request_id,omitempty" gorm:"type:varchar(191);index"`
	DetailsJSON  string    `json:"details_json,omitempty" gorm:"type:text"`
	CreatedAt    time.Time `json:"created_at" gorm:"index"`
}

func (AuditLog) TableName() string { return "gateway_audit_logs" }

// GatewaySchemaVersion allows the queue schema to be migrated independently of legacy tables.
type GatewaySchemaVersion struct {
	Version   int       `json:"version" gorm:"primaryKey"`
	AppliedAt time.Time `json:"applied_at"`
}

func (GatewaySchemaVersion) TableName() string { return "gateway_schema_versions" }

// GatewayEvent stores durable callback deduplication and a recoverable processing lease.
type GatewayEvent struct {
	Id              string     `json:"id" gorm:"type:char(36);primaryKey"`
	ChannelID       string     `json:"channel_id" gorm:"type:varchar(191);not null;uniqueIndex:idx_gateway_event_identity,priority:1"`
	EventID         string     `json:"event_id" gorm:"type:varchar(191);not null;uniqueIndex:idx_gateway_event_identity,priority:2"`
	EventType       string     `json:"event_type,omitempty" gorm:"type:varchar(64)"`
	MessageID       string     `json:"message_id,omitempty" gorm:"type:varchar(191)"`
	MessageSequence int64      `json:"message_sequence,omitempty"`
	TargetType      string     `json:"target_type,omitempty" gorm:"type:varchar(64)"`
	TargetOpenID    string     `json:"target_openid,omitempty" gorm:"type:varchar(191)"`
	PayloadHash     string     `json:"-" gorm:"type:char(64)"`
	Status          string     `json:"status" gorm:"type:varchar(32);not null;index"`
	AttemptCount    int        `json:"attempt_count" gorm:"not null;default:0"`
	LeaseOwner      string     `json:"-" gorm:"type:varchar(191)"`
	LeaseToken      string     `json:"-" gorm:"type:char(36)"`
	LeaseExpiresAt  *time.Time `json:"-" gorm:"index"`
	LastError       string     `json:"last_error,omitempty" gorm:"type:text"`
	ReceivedAt      time.Time  `json:"received_at"`
	ProcessedAt     *time.Time `json:"processed_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func (GatewayEvent) TableName() string { return "gateway_events" }

// QQBotMessageSequence allocates the bounded passive-reply sequence per bot and
// incoming QQBot message. A delivery stores its assigned sequence so retries reuse it.
type QQBotMessageSequence struct {
	BotAppID     string    `json:"-" gorm:"type:varchar(191);not null;uniqueIndex:idx_gateway_qqbot_message_sequence,priority:1"`
	MessageID    string    `json:"-" gorm:"type:varchar(191);not null;uniqueIndex:idx_gateway_qqbot_message_sequence,priority:2"`
	TargetType   string    `json:"-" gorm:"type:varchar(64);not null;uniqueIndex:idx_gateway_qqbot_message_sequence,priority:3"`
	LastSequence int       `json:"last_sequence" gorm:"not null;default:0"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (QQBotMessageSequence) TableName() string { return "gateway_qqbot_message_sequences" }
