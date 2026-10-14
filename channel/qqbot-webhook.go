package channel

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"message-pusher/model"
)

type QQBotChannelResolver func(context.Context, string) (*model.Channel, error)

type QQBotEventHandler func(context.Context, *model.Channel, QQBotCallbackEvent) error

// QQBotCallbackEvent is the verified and normalized subset of an official
// QQBot event used for target discovery and passive replies.
type QQBotCallbackEvent struct {
	EventID           string
	EventType         string
	MessageSequence   int64
	TargetType        string
	TargetOpenID      string
	UserOpenID        string
	GroupOpenID       string
	MessageID         string
	TargetInteraction bool
	TargetRemoved     bool
	Content           string
	ReceivedAt        time.Time
	RawData           json.RawMessage
}

type qqBotCallbackEnvelope struct {
	Operation int                        `json:"op"`
	Sequence  int                        `json:"s"`
	EventType string                     `json:"t"`
	EventID   string                     `json:"id"`
	Data      map[string]json.RawMessage `json:"d"`
}

type qqBotChallenge struct {
	PlainToken string `json:"plain_token"`
	EventTS    string `json:"event_ts"`
}

type qqBotChallengeResponse struct {
	PlainToken string `json:"plain_token"`
	Signature  string `json:"signature"`
}

// NewQQBotWebhookHandler returns an HTTP handler that routes a verified QQBot
// callback by its X-Bot-Appid header. The resolver must return a decrypted
// channel credential from the application's channel store.
func NewQQBotWebhookHandler(resolve QQBotChannelResolver, onEvent QQBotEventHandler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if resolve == nil || onEvent == nil {
			http.Error(writer, "QQBot callback is not configured", http.StatusServiceUnavailable)
			return
		}
		appID := strings.TrimSpace(request.Header.Get("X-Bot-Appid"))
		if appID == "" || len(appID) > 128 {
			http.Error(writer, "missing bot application id", http.StatusForbidden)
			return
		}
		body, err := io.ReadAll(io.LimitReader(request.Body, qqBotMaxBodyBytes+1))
		if err != nil {
			http.Error(writer, "invalid callback body", http.StatusBadRequest)
			return
		}
		if len(body) > qqBotMaxBodyBytes {
			http.Error(writer, "callback body is too large", http.StatusRequestEntityTooLarge)
			return
		}
		channel_, err := resolve(request.Context(), appID)
		if err != nil {
			http.Error(writer, "QQBot callback channel lookup failed", http.StatusServiceUnavailable)
			return
		}
		if channel_ == nil || channel_.Type != model.TypeQQBotV2 || channel_.AppId != appID || channel_.Secret == "" {
			http.Error(writer, "unknown QQBot application", http.StatusForbidden)
			return
		}
		var envelope qqBotCallbackEnvelope
		if err = json.Unmarshal(body, &envelope); err != nil {
			http.Error(writer, "invalid QQBot callback payload", http.StatusBadRequest)
			return
		}
		if envelope.Operation == 13 {
			var challenge qqBotChallenge
			if raw, ok := envelope.Data["plain_token"]; !ok || json.Unmarshal(raw, &challenge.PlainToken) != nil {
				http.Error(writer, "invalid QQBot verification challenge", http.StatusBadRequest)
				return
			}
			if raw, ok := envelope.Data["event_ts"]; !ok || json.Unmarshal(raw, &challenge.EventTS) != nil || challenge.PlainToken == "" || challenge.EventTS == "" {
				http.Error(writer, "invalid QQBot verification challenge", http.StatusBadRequest)
				return
			}
			signature, signErr := QQBotWebhookChallengeSignature(channel_.Secret, challenge.EventTS, challenge.PlainToken)
			if signErr != nil {
				http.Error(writer, "could not sign QQBot verification challenge", http.StatusInternalServerError)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(writer).Encode(qqBotChallengeResponse{PlainToken: challenge.PlainToken, Signature: signature})
			return
		}
		timestamp := request.Header.Get("X-Signature-Timestamp")
		signature := request.Header.Get("X-Signature-Ed25519")
		if !VerifyQQBotWebhookSignature(channel_.Secret, timestamp, signature, body) {
			http.Error(writer, "invalid QQBot callback signature", http.StatusUnauthorized)
			return
		}
		if envelope.Operation != 0 {
			writeQQBotCallbackAck(writer)
			return
		}
		event, parseErr := normalizeQQBotCallback(envelope, body)
		if parseErr != nil {
			http.Error(writer, "invalid QQBot event", http.StatusBadRequest)
			return
		}
		if err = onEvent(request.Context(), channel_, event); err != nil {
			http.Error(writer, "QQBot event could not be persisted", http.StatusServiceUnavailable)
			return
		}
		writeQQBotCallbackAck(writer)
	})
}

func writeQQBotCallbackAck(writer http.ResponseWriter) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(writer, `{"op":12,"d":0}`)
}

// QQBotWebhookChallengeSignature signs event_ts + plain_token with the
// Ed25519 key derived from the bot ClientSecret for the official callback URL
// verification handshake.
func QQBotWebhookChallengeSignature(clientSecret, eventTS, plainToken string) (string, error) {
	privateKey, err := deriveQQBotWebhookPrivateKey(clientSecret)
	if err != nil {
		return "", err
	}
	message := []byte(eventTS + plainToken)
	return hex.EncodeToString(ed25519.Sign(privateKey, message)), nil
}

// VerifyQQBotWebhookSignature checks the official Ed25519 signature over the
// exact timestamp header followed by the raw callback bytes.
func VerifyQQBotWebhookSignature(clientSecret, timestamp, signature string, rawBody []byte) bool {
	if clientSecret == "" || timestamp == "" || signature == "" || len(timestamp) > 32 || len(signature) != ed25519.SignatureSize*2 {
		return false
	}
	for _, r := range timestamp {
		if r < '0' || r > '9' {
			return false
		}
	}
	signatureBytes, err := hex.DecodeString(signature)
	if err != nil || len(signatureBytes) != ed25519.SignatureSize || signatureBytes[63]&0xe0 != 0 {
		return false
	}
	privateKey, err := deriveQQBotWebhookPrivateKey(clientSecret)
	if err != nil {
		return false
	}
	message := make([]byte, 0, len(timestamp)+len(rawBody))
	message = append(message, timestamp...)
	message = append(message, rawBody...)
	return ed25519.Verify(privateKey.Public().(ed25519.PublicKey), message, signatureBytes)
}

func deriveQQBotWebhookPrivateKey(clientSecret string) (ed25519.PrivateKey, error) {
	if clientSecret == "" {
		return nil, errors.New("QQBot ClientSecret is required")
	}
	secret := []byte(clientSecret)
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = secret[i%len(secret)]
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func normalizeQQBotCallback(envelope qqBotCallbackEnvelope, rawBody []byte) (QQBotCallbackEvent, error) {
	rawData, err := json.Marshal(envelope.Data)
	if err != nil {
		return QQBotCallbackEvent{}, fmt.Errorf("encode callback event data: %w", err)
	}
	event := QQBotCallbackEvent{
		EventID:         envelope.EventID,
		EventType:       envelope.EventType,
		MessageSequence: int64(envelope.Sequence),
		RawData:         rawData,
		ReceivedAt:      time.Now().UTC(),
	}
	if isQQBotTargetEvent(event.EventType) {
		event.UserOpenID = callbackString(envelope.Data, "user_openid", "openid")
		if authorRaw, ok := envelope.Data["author"]; ok {
			var author map[string]json.RawMessage
			if json.Unmarshal(authorRaw, &author) == nil {
				event.UserOpenID = firstCallbackString(author, "user_openid", "member_openid", "openid")
			}
		}
		event.GroupOpenID = callbackString(envelope.Data, "group_openid")
		event.Content = callbackString(envelope.Data, "content")
	}
	if event.EventID == "" {
		event.EventID = callbackString(envelope.Data, "event_id")
	}
	if isQQBotInboundMessageEvent(event.EventType) {
		event.MessageID = callbackString(envelope.Data, "id", "msg_id")
		event.TargetInteraction = true
	}
	if event.EventType == "INTERACTION_CREATE" {
		event.TargetInteraction = true
	}
	switch event.EventType {
	case "FRIEND_DEL", "GROUP_DEL_ROBOT", "C2C_MSG_REJECT", "GROUP_MSG_REJECT":
		event.TargetRemoved = true
	}
	if event.EventID == "" {
		return QQBotCallbackEvent{}, errors.New("QQBot callback omitted the event id")
	}
	if event.GroupOpenID != "" {
		event.TargetType = "group"
		event.TargetOpenID = event.GroupOpenID
	} else if event.UserOpenID != "" {
		event.TargetType = "user"
		event.TargetOpenID = event.UserOpenID
	}
	event.ReceivedAt = callbackTime(envelope.Data, event.ReceivedAt)
	if len(rawBody) == 0 {
		return QQBotCallbackEvent{}, errors.New("QQBot callback body is empty")
	}
	return event, nil
}

func isQQBotInboundMessageEvent(eventType string) bool {
	switch eventType {
	case "C2C_MESSAGE_CREATE", "GROUP_AT_MESSAGE_CREATE", "GROUP_MESSAGE_CREATE":
		return true
	default:
		return false
	}
}

func isQQBotTargetEvent(eventType string) bool {
	switch eventType {
	case "C2C_MESSAGE_CREATE", "FRIEND_ADD", "FRIEND_DEL", "C2C_MSG_REJECT", "C2C_MSG_RECEIVE",
		"GROUP_AT_MESSAGE_CREATE", "GROUP_MESSAGE_CREATE", "GROUP_ADD_ROBOT", "GROUP_DEL_ROBOT", "GROUP_MSG_REJECT", "GROUP_MSG_RECEIVE", "INTERACTION_CREATE":
		return true
	default:
		return false
	}
}

func callbackString(values map[string]json.RawMessage, keys ...string) string {
	return firstCallbackString(values, keys...)
}

func firstCallbackString(values map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		raw, ok := values[key]
		if !ok {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) == nil && value != "" {
			return value
		}
	}
	return ""
}

func callbackTime(values map[string]json.RawMessage, fallback time.Time) time.Time {
	raw, ok := values["timestamp"]
	if !ok {
		return fallback
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
			return parsed.UTC()
		}
		if unix, err := strconv.ParseInt(value, 10, 64); err == nil {
			return unixTime(unix)
		}
	}
	var unix int64
	if json.Unmarshal(raw, &unix) == nil {
		return unixTime(unix)
	}
	return fallback
}

func unixTime(value int64) time.Time {
	if value > 1_000_000_000_000 {
		return time.UnixMilli(value).UTC()
	}
	return time.Unix(value, 0).UTC()
}
