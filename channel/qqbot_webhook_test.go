package channel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"message-pusher/model"
)

func TestQQBotWebhookChallengeSignatureVerifiesCallback(t *testing.T) {
	const secret = "test-client-secret"
	const timestamp = "1725442341"
	body := []byte(`{"op":0,"s":17,"t":"C2C_MESSAGE_CREATE","d":{"id":"message-1"}}`)
	signature, err := QQBotWebhookChallengeSignature(secret, timestamp, string(body))
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyQQBotWebhookSignature(secret, timestamp, signature, body) {
		t.Fatal("signature for the exact timestamp and raw body should verify")
	}
	body[0] = '['
	if VerifyQQBotWebhookSignature(secret, timestamp, signature, body) {
		t.Fatal("signature must fail when the raw callback body changes")
	}
}

func TestNormalizeQQBotCallbackSeparatesDispatchAndReplySequence(t *testing.T) {
	body := []byte(`{"op":0,"s":31,"id":"event-2","t":"GROUP_AT_MESSAGE_CREATE","d":{"id":"message-2","group_openid":"group-openid","author":{"user_openid":"user-openid"},"content":"hello"}}`)
	var envelope qqBotCallbackEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	event, err := normalizeQQBotCallback(envelope, body)
	if err != nil {
		t.Fatal(err)
	}
	if event.MessageSequence != 31 {
		t.Fatalf("dispatch sequence = %d, want 31", event.MessageSequence)
	}
	if event.MessageID != "message-2" || event.EventID != "event-2" || !event.TargetInteraction {
		t.Fatalf("message/event IDs and interaction should remain distinct: %+v", event)
	}
	if event.TargetType != "group" || event.TargetOpenID != "group-openid" {
		t.Fatalf("unexpected target: %+v", event)
	}
	if event.UserOpenID != "user-openid" {
		t.Fatalf("group member OpenID should still be available: %+v", event)
	}
}

func TestNormalizeQQBotCallbackOnlyUsesMessageIDsForMessageEvents(t *testing.T) {
	body := []byte(`{"op":0,"s":32,"id":"event-add","t":"FRIEND_ADD","d":{"id":"not-a-message-id","user_openid":"user-openid"}}`)
	var envelope qqBotCallbackEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	event, err := normalizeQQBotCallback(envelope, body)
	if err != nil {
		t.Fatal(err)
	}
	if event.MessageID != "" || event.EventID != "event-add" || event.TargetInteraction || event.TargetRemoved {
		t.Fatalf("friend-add IDs must not be treated as message IDs or user chat: %+v", event)
	}

	body = []byte(`{"op":0,"s":33,"id":"event-del","t":"FRIEND_DEL","d":{"id":"not-a-message-id","user_openid":"user-openid"}}`)
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	event, err = normalizeQQBotCallback(envelope, body)
	if err != nil {
		t.Fatal(err)
	}
	if event.MessageID != "" || !event.TargetRemoved || event.TargetInteraction {
		t.Fatalf("friend removal should mark the target removed without refreshing interaction: %+v", event)
	}
}

func TestNormalizeQQBotGroupMessageCreateUsesMessageID(t *testing.T) {
	body := []byte(`{"op":0,"s":34,"id":"event-group-message","t":"GROUP_MESSAGE_CREATE","d":{"id":"group-message-1","group_openid":"group-openid","author":{"user_openid":"user-openid"},"content":"notice"}}`)
	var envelope qqBotCallbackEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	event, err := normalizeQQBotCallback(envelope, body)
	if err != nil {
		t.Fatal(err)
	}
	if event.MessageID != "group-message-1" || event.EventID != "event-group-message" || !event.TargetInteraction {
		t.Fatalf("GROUP_MESSAGE_CREATE should preserve event and message IDs separately: %+v", event)
	}
}

func TestVerifyQQBotWebhookSignatureRejectsMalformedHeaders(t *testing.T) {
	for _, test := range []struct {
		name      string
		timestamp string
		signature string
	}{
		{name: "empty timestamp", signature: "00"},
		{name: "non-numeric timestamp", timestamp: "1x", signature: "00"},
		{name: "short signature", timestamp: "123", signature: "00"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if VerifyQQBotWebhookSignature("secret", test.timestamp, test.signature, []byte("{}")) {
				t.Fatal("malformed signature headers must be rejected")
			}
		})
	}
}

func TestQQBotWebhookHandlerPersistsThenReturnsProtocolAck(t *testing.T) {
	const secret = "handler-client-secret"
	const timestamp = "1725442341"
	body := []byte(`{"op":0,"s":17,"id":"event-1","t":"C2C_MESSAGE_CREATE","d":{"id":"message-1","user_openid":"user-1","content":"hello"}}`)
	signature, err := QQBotWebhookChallengeSignature(secret, timestamp, string(body))
	if err != nil {
		t.Fatal(err)
	}
	channel := &model.Channel{Type: model.TypeQQBotV2, AppId: "app-1", Secret: secret}
	persisted := false
	handler := NewQQBotWebhookHandler(
		func(context.Context, string) (*model.Channel, error) { return channel, nil },
		func(_ context.Context, _ *model.Channel, event QQBotCallbackEvent) error {
			persisted = true
			if event.EventID != "event-1" || event.TargetOpenID != "user-1" {
				t.Fatalf("unexpected normalized event: %+v", event)
			}
			return nil
		},
	)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/qqbot/callback", strings.NewReader(string(body)))
	request.Header.Set("X-Bot-Appid", "app-1")
	request.Header.Set("X-Signature-Timestamp", timestamp)
	request.Header.Set("X-Signature-Ed25519", signature)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !persisted {
		t.Fatalf("response status=%d persisted=%t", response.Code, persisted)
	}
	var ack struct {
		Operation int `json:"op"`
		Data      int `json:"d"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &ack); err != nil {
		t.Fatalf("invalid webhook ACK: %v", err)
	}
	if ack.Operation != 12 || ack.Data != 0 {
		t.Fatalf("unexpected callback ACK: %+v", ack)
	}
}

func TestQQBotWebhookHandlerDistinguishesLookupFailureFromUnknownApp(t *testing.T) {
	for _, test := range []struct {
		name       string
		resolve    QQBotChannelResolver
		wantStatus int
	}{
		{name: "unknown app id", resolve: func(context.Context, string) (*model.Channel, error) { return nil, nil }, wantStatus: http.StatusForbidden},
		{name: "database error", resolve: func(context.Context, string) (*model.Channel, error) { return nil, errors.New("database unavailable") }, wantStatus: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := NewQQBotWebhookHandler(test.resolve, func(context.Context, *model.Channel, QQBotCallbackEvent) error { return nil })
			request := httptest.NewRequest(http.MethodPost, "/api/v1/qqbot/callback", strings.NewReader(`{}`))
			request.Header.Set("X-Bot-Appid", "app-1")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
		})
	}
}
