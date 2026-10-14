package channel

import (
	"context"
	"time"
)

type qqBotMetadataKey struct{}

// QQBotDeliveryMetadata carries target and delivery-mode data from the durable
// gateway record. Passive replies need verified event fields; OpenIDs are
// opaque platform identifiers and must never be derived from QQ account IDs.
type QQBotDeliveryMetadata struct {
	TargetType   string
	TargetOpenID string
	Mode         string
	MessageType  string
	EventID      string
	MessageID    string
	MessageSeq   int
	IsWakeup     bool
	ReceivedAt   time.Time
}

func WithQQBotDeliveryMetadata(ctx context.Context, metadata QQBotDeliveryMetadata) context.Context {
	return context.WithValue(ctx, qqBotMetadataKey{}, metadata)
}

func QQBotDeliveryMetadataFromContext(ctx context.Context) (QQBotDeliveryMetadata, bool) {
	if ctx == nil {
		return QQBotDeliveryMetadata{}, false
	}
	metadata, ok := ctx.Value(qqBotMetadataKey{}).(QQBotDeliveryMetadata)
	return metadata, ok
}

// DeliveryResult contains provider identifiers and HTTP status for the
// queue's DeliveryAttempt record. Retry classification belongs to
// common.DeliveryError.
type DeliveryResult struct {
	HTTPStatus        int
	ProviderCode      string
	ProviderMessageID string
}
