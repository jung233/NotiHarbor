package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"gorm.io/gorm"
	"message-pusher/model"
)

var (
	ErrIdempotencyConflict = errors.New("idempotency key was already used with a different request")
	ErrNotFound            = errors.New("gateway resource not found")
	ErrInvalidRequest      = errors.New("invalid gateway request")
	ErrLeaseLost           = errors.New("delivery lease was lost")
	ErrLeaseActive         = errors.New("delivery is leased by another worker")
)

const (
	PolicyBroadcast = "broadcast"
	PolicyFallback  = "fallback"

	UnknownDoNotRetry = "manual"
	UnknownRetryOnce  = "retry_once"

	ScopeNotificationsWrite = "notifications:write"
	ScopeNotificationsRead  = "notifications:read"
	ScopeTargetsWrite       = "targets:write"
)

// Sender adapts existing channel implementations without importing channel into this package.
type Sender interface {
	Send(context.Context, *model.Notification, *model.Delivery, *model.Target) (SendResult, error)
}

type SenderFunc func(context.Context, *model.Notification, *model.Delivery, *model.Target) (SendResult, error)

func (f SenderFunc) Send(ctx context.Context, notification *model.Notification, delivery *model.Delivery, target *model.Target) (SendResult, error) {
	return f(ctx, notification, delivery, target)
}

type Options struct {
	StreamName             string
	ConsumerGroup          string
	ConsumerName           string
	Concurrency            int
	BatchSize              int64
	BlockDuration          time.Duration
	ClaimIdle              time.Duration
	LeaseDuration          time.Duration
	SendTimeout            time.Duration
	OutboxPoll             time.Duration
	RetryPoll              time.Duration
	OutboxLease            time.Duration
	RetryBackoff           []time.Duration
	MaxAttempts            int
	MaxPreparationFailures int
	UnknownPolicy          string
	JitterFraction         float64
	RateLimitPrefix        string
}

type Service struct {
	db     *gorm.DB
	rdb    redis.UniversalClient
	sender Sender
	opts   Options
}

func NewService(db *gorm.DB, rdb redis.UniversalClient, sender Sender, opts Options) *Service {
	return &Service{db: db, rdb: rdb, sender: sender, opts: withDefaults(opts)}
}

func withDefaults(opts Options) Options {
	if opts.StreamName == "" {
		opts.StreamName = "message-pusher:gateway:deliveries"
	}
	if opts.ConsumerGroup == "" {
		opts.ConsumerGroup = "message-pusher:gateway-workers"
	}
	if opts.ConsumerName == "" {
		opts.ConsumerName = newID()
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 16
	}
	if opts.BlockDuration <= 0 {
		opts.BlockDuration = 2 * time.Second
	}
	if opts.ClaimIdle <= 0 {
		opts.ClaimIdle = 45 * time.Second
	}
	if opts.LeaseDuration <= 0 {
		opts.LeaseDuration = 90 * time.Second
	}
	if opts.SendTimeout <= 0 {
		opts.SendTimeout = 30 * time.Second
	}
	if opts.OutboxPoll <= 0 {
		opts.OutboxPoll = time.Second
	}
	if opts.RetryPoll <= 0 {
		opts.RetryPoll = time.Second
	}
	if opts.OutboxLease <= 0 {
		opts.OutboxLease = 30 * time.Second
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 5
	}
	if opts.MaxPreparationFailures <= 0 {
		opts.MaxPreparationFailures = 5
	}
	if len(opts.RetryBackoff) == 0 {
		opts.RetryBackoff = []time.Duration{30 * time.Second, 2 * time.Minute, 5 * time.Minute, 15 * time.Minute}
	}
	if opts.UnknownPolicy == "" {
		opts.UnknownPolicy = UnknownDoNotRetry
	}
	if opts.RateLimitPrefix == "" {
		opts.RateLimitPrefix = "message-pusher:gateway:rate"
	}
	return opts
}

type DeliverySpec struct {
	ChannelID           string          `json:"channel_id,omitempty"`
	ChannelName         string          `json:"channel_name,omitempty"`
	ChannelType         string          `json:"channel_type,omitempty"`
	MessageType         string          `json:"message_type,omitempty"`
	TargetID            string          `json:"target_id,omitempty"`
	Target              string          `json:"target,omitempty"`
	RouteOrder          int             `json:"route_order,omitempty"`
	MaxAttempts         int             `json:"max_attempts,omitempty"`
	RetryBackoffSeconds []int           `json:"retry_backoff_seconds,omitempty"`
	DeliveryMetadata    json.RawMessage `json:"-"`
}

type EnqueueRequest struct {
	SourceID       string          `json:"source_id"`
	IdempotencyKey string          `json:"idempotency_key"`
	RequestID      string          `json:"request_id,omitempty"`
	Title          string          `json:"title"`
	Content        string          `json:"content"`
	Priority       string          `json:"priority,omitempty"`
	DeliveryPolicy string          `json:"delivery_policy,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	Deliveries     []DeliverySpec  `json:"deliveries"`
}

type SourceInput struct {
	Name               string   `json:"name"`
	Description        string   `json:"description,omitempty"`
	RateLimitPerMinute int      `json:"rate_limit_per_minute,omitempty"`
	Status             string   `json:"status,omitempty"`
	AllowedChannelIDs  []string `json:"allowed_channel_ids,omitempty"`
}

type NotificationFilter struct {
	From                 *time.Time `json:"from,omitempty"`
	To                   *time.Time `json:"to,omitempty"`
	SourceID             string     `json:"source_id,omitempty"`
	OwnerUserID          int        `json:"-"`
	ChannelID            string     `json:"channel_id,omitempty"`
	Status               string     `json:"status,omitempty"`
	Error                string     `json:"error,omitempty"`
	Search               string     `json:"search,omitempty"`
	IncludeLegacyArchive bool       `json:"-"`
	Page                 int        `json:"page,omitempty"`
	PageSize             int        `json:"page_size,omitempty"`
}

type AuditFilter struct {
	OwnerUserID  int
	SourceID     string
	ActorID      string
	Action       string
	ResourceType string
	From         *time.Time
	To           *time.Time
	Page         int
	PageSize     int
}

type TestRequest struct {
	OwnerUserID  int
	ActorID      string
	ActorType    string
	SourceID     string
	ChannelID    string
	ChannelName  string
	ChannelType  string
	MessageType  string
	DeliveryMode string
	TargetID     string
	Target       string
	Title        string
	Content      string
	Metadata     DeliveryMetadata `json:"-"`
}

type TargetInput struct {
	Id                string     `json:"id,omitempty"`
	SourceID          string     `json:"source_id"`
	ChannelID         string     `json:"channel_id"`
	TargetType        string     `json:"target_type"`
	ExternalID        string     `json:"external_id"`
	Name              string     `json:"name,omitempty"`
	Source            string     `json:"source,omitempty"`
	Status            string     `json:"status,omitempty"`
	LastInteractionAt *time.Time `json:"last_interaction_at,omitempty"`
	IsDefault         bool       `json:"is_default,omitempty"`
}

type SendResult struct {
	HTTPStatus        int
	ProviderCode      string
	ProviderMessageID string
}

type Stats struct {
	Notifications         int64              `json:"notifications"`
	Accepted              int64              `json:"accepted"`
	Failed                int64              `json:"failed"`
	Retrying              int64              `json:"retrying"`
	Unknown               int64              `json:"delivery_unknown"`
	Queued                int64              `json:"queued"`
	DeadLetter            int64              `json:"dead_letter"`
	TodayReceived         int64              `json:"today_received"`
	TodayAccepted         int64              `json:"today_accepted"`
	TodayFailed           int64              `json:"today_failed"`
	TodayRetrying         int64              `json:"today_retrying"`
	QueueBacklog          int64              `json:"queue_backlog"`
	PendingMessages       int64              `json:"pending_messages"`
	QueueBacklogAvailable bool               `json:"queue_backlog_available"`
	ByChannel             []ChannelStats     `json:"by_channel"`
	Last24Hours           []TrendBucket      `json:"last_24_hours"`
	RecentErrors          []StatsRecentError `json:"recent_errors"`
}

type ChannelStats struct {
	ChannelID         string  `json:"channel_id"`
	ChannelName       string  `json:"channel_name"`
	Total             int64   `json:"total"`
	Accepted          int64   `json:"accepted"`
	Failed            int64   `json:"failed"`
	Retrying          int64   `json:"retrying"`
	SuccessRate       float64 `json:"success_rate"`
	AvgDurationMS     float64 `json:"avg_duration_ms"`
	AverageDurationMS float64 `json:"average_duration_ms"`
}

type TrendBucket struct {
	Hour     time.Time `json:"hour"`
	Received int64     `json:"received"`
	Accepted int64     `json:"accepted"`
	Failed   int64     `json:"failed"`
}

type StatsRecentError struct {
	DeliveryID  string    `json:"delivery_id"`
	SourceID    string    `json:"source_id"`
	ChannelID   string    `json:"channel_id"`
	ChannelName string    `json:"channel_name"`
	Status      string    `json:"status"`
	ErrorCode   string    `json:"error_code"`
	Error       string    `json:"error"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type DeliveryMetadata struct {
	EventID      string
	MessageID    string
	MessageSeq   int
	IsWakeUp     bool
	Mode         string
	MessageType  string
	TargetType   string
	TargetOpenID string
	ReceivedAt   time.Time
}

type deliveryMetadataContextKey struct{}

func WithDeliveryMetadata(ctx context.Context, metadata DeliveryMetadata) context.Context {
	return context.WithValue(ctx, deliveryMetadataContextKey{}, metadata)
}

func DeliveryMetadataFromContext(ctx context.Context) (DeliveryMetadata, bool) {
	if ctx == nil {
		return DeliveryMetadata{}, false
	}
	metadata, ok := ctx.Value(deliveryMetadataContextKey{}).(DeliveryMetadata)
	return metadata, ok
}

var defaultService struct {
	sync.RWMutex
	service *Service
}

func SetDefaultService(service *Service) {
	defaultService.Lock()
	defaultService.service = service
	defaultService.Unlock()
}

func DefaultService() *Service {
	defaultService.RLock()
	defer defaultService.RUnlock()
	return defaultService.service
}
