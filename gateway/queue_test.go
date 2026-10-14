package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"message-pusher/common"
	"message-pusher/model"
)

func TestPrepareEnqueueRequestGeneratesIndependentKeys(t *testing.T) {
	request := EnqueueRequest{
		SourceID: "source-1",
		Title:    "notice",
		Content:  "body",
		Deliveries: []DeliverySpec{{
			ChannelID: "1",
		}},
	}

	first, firstHash, err := prepareEnqueueRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	second, secondHash, err := prepareEnqueueRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if first.IdempotencyKey == second.IdempotencyKey {
		t.Fatal("requests without a caller key must receive independent idempotency keys")
	}
	if firstHash != secondHash {
		t.Fatal("generated idempotency keys must not change the request content hash")
	}

	request.IdempotencyKey = "client-key"
	first, firstHash, err = prepareEnqueueRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	second, secondHash, err = prepareEnqueueRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if first.IdempotencyKey != "client-key" || second.IdempotencyKey != "client-key" || firstHash != secondHash {
		t.Fatal("caller idempotency keys must retain their stable semantics")
	}
}

func TestRequestHashCanonicalizesMetadataObjectKeyOrder(t *testing.T) {
	first := EnqueueRequest{
		SourceID: "source-1", Title: "notice", Content: "body",
		Metadata:   json.RawMessage(`{"alpha":1,"beta":{"x":true,"y":false}}`),
		Deliveries: []DeliverySpec{{ChannelID: "1"}},
	}
	second := first
	second.Metadata = json.RawMessage(`{"beta":{"y":false,"x":true},"alpha":1}`)

	_, firstHash, err := prepareEnqueueRequest(first)
	if err != nil {
		t.Fatal(err)
	}
	_, secondHash, err := prepareEnqueueRequest(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != secondHash {
		t.Fatal("equivalent metadata objects must have the same idempotency request hash")
	}
}

func TestClassifyErrorSeparatesPreparationFromUnknownSend(t *testing.T) {
	preflight := common.NewDeliveryErrorWithAttempted(
		"database", "preflight_database", true, false, false, 3*time.Second, 0,
		errors.New("database unavailable"),
	)
	classified := classifyError(fmt.Errorf("wrapped: %w", preflight))
	if classified.attempted || classified.unknown || !classified.retryable || classified.retryAfter != 3*time.Second {
		t.Fatalf("unexpected preflight classification: %+v", classified)
	}

	unknown := common.NewDeliveryErrorWithAttempted(
		"timeout", "", false, true, false, 0, 0, errors.New("request timed out"),
	)
	classified = classifyError(unknown)
	if !classified.attempted || !classified.unknown {
		t.Fatalf("unknown delivery must consume one actual-send attempt even when adapter flag is false: %+v", classified)
	}
}

func TestDeliveryMetadataUsesPersistedQQBotSequence(t *testing.T) {
	notification := &model.Notification{MetadataJSON: `{"event_id":"event-1","msg_id":"message-1","msg_seq":1,"mode":"passive"}`}
	delivery := &model.Delivery{MessageType: "text", MessageSequence: 4}

	metadata := deliveryMetadata(notification, delivery)
	if metadata.EventID != "event-1" || metadata.MessageID != "message-1" || metadata.Mode != "passive" {
		t.Fatalf("callback metadata was not restored from durable notification data: %+v", metadata)
	}
	if metadata.MessageSeq != 4 {
		t.Fatalf("retry sequence must come from the persisted delivery: got %d", metadata.MessageSeq)
	}
}

func TestDeliveryMetadataPrefersPerDeliveryQQContext(t *testing.T) {
	notification := &model.Notification{MetadataJSON: `{"mode":"active","target_type":"group","target_openid":"shared-target"}`}
	delivery := &model.Delivery{
		MessageType:     "markdown",
		MetadataJSON:    `{"event_id":"event-2","msg_id":"message-2","mode":"passive","target_type":"user","target_openid":"user-2","received_at":"2026-10-14T00:00:00Z"}`,
		MessageSequence: 2,
	}
	metadata := deliveryMetadata(notification, delivery)
	if metadata.Mode != "passive" || metadata.TargetType != "user" || metadata.TargetOpenID != "user-2" || metadata.EventID != "event-2" {
		t.Fatalf("delivery-specific QQ metadata was not restored: %+v", metadata)
	}
}

func TestQQBotModesAndReplyEventValidation(t *testing.T) {
	if got := effectiveQQBotMode(model.Channel{}, ""); got != "active" {
		t.Fatalf("default QQBot mode = %q, want active", got)
	}
	if !validQQBotReplyEvent("user", "C2C_MESSAGE_CREATE", "passive") {
		t.Fatal("incoming C2C message should permit passive reply")
	}
	if !validQQBotReplyEvent("user", "FRIEND_ADD", "passive") {
		t.Fatal("verified user friend event should permit event-id reply")
	}
	if validQQBotReplyEvent("group", "C2C_MESSAGE_CREATE", "passive") {
		t.Fatal("C2C callback must not be treated as a group interaction")
	}
	if validQQBotReplyEvent("group", "GROUP_MSG_RECEIVE", "wakeup") {
		t.Fatal("wakeup is available only for user conversations")
	}
	for _, eventType := range []string{"C2C_MESSAGE_CREATE", "INTERACTION_CREATE"} {
		if !validQQBotReplyEvent("user", eventType, "wakeup") {
			t.Fatalf("user interaction %q should permit wakeup", eventType)
		}
	}
	for _, eventType := range []string{"FRIEND_ADD", "C2C_MSG_RECEIVE", "C2C_INTERACTION_CREATE"} {
		if validQQBotReplyEvent("user", eventType, "wakeup") {
			t.Fatalf("event %q must not grant wakeup eligibility", eventType)
		}
	}
}

func TestRetryDelayUsesPersistedDeliveryBackoff(t *testing.T) {
	service := NewService(nil, nil, nil, Options{JitterFraction: 0})
	delivery := &model.Delivery{RetryBackoffJSON: `[4,9]`}
	if got := service.retryDelayFor(delivery, 1, 0); got != 4*time.Second {
		t.Fatalf("first persisted delay = %s, want 4s", got)
	}
	if got := service.retryDelayFor(delivery, 3, 0); got != 9*time.Second {
		t.Fatalf("delay past configured entries = %s, want final 9s delay", got)
	}
	if got := service.retryDelayFor(delivery, 1, 2*time.Minute); got != 2*time.Minute {
		t.Fatalf("provider Retry-After = %s, want 2m", got)
	}
}
