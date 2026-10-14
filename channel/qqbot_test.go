package channel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"message-pusher/common"
	"message-pusher/model"
)

type qqBotRoundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip qqBotRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestBuildQQBotMessageUsesPassiveMessageReply(t *testing.T) {
	message := &model.Message{Title: "Notice", Content: "Body"}
	metadata := QQBotDeliveryMetadata{
		Mode:       "passive",
		MessageID:  "message-id",
		EventID:    "event-id",
		ReceivedAt: time.Now(),
	}
	payload, err := buildQQBotMessage(message, qqBotConfig{MessageType: "text"}, metadata, "user")
	if err != nil {
		t.Fatal(err)
	}
	if payload.EventID != "" || payload.MessageID != "message-id" || payload.MessageSeq != 1 {
		t.Fatalf("unexpected passive reply metadata: %+v", payload)
	}
	if payload.MessageType != 0 || payload.Content != "# Notice\n\nBody" {
		t.Fatalf("unexpected text payload: %+v", payload)
	}
}

func TestBuildQQBotMarkdownUsesMarkdownFieldWithoutTopLevelContent(t *testing.T) {
	message := &model.Message{Title: "Notice", Content: "Body"}
	payload, err := buildQQBotMessage(message, qqBotConfig{DeliveryMode: "active", MessageType: "markdown"}, QQBotDeliveryMetadata{}, "user")
	if err != nil {
		t.Fatal(err)
	}
	if payload.MessageType != 2 || payload.Content != "" || payload.Markdown == nil || payload.Markdown.Content != "# Notice\n\nBody" {
		t.Fatalf("unexpected QQBot markdown payload: %+v", payload)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["content"]; exists {
		t.Fatalf("markdown requests must omit top-level content: %s", encoded)
	}
}

func TestBuildQQBotMessageHonorsUserAndGroupReplyWindows(t *testing.T) {
	message := &model.Message{Content: "reply"}
	tests := []struct {
		name       string
		targetType string
		age        time.Duration
		wantErr    bool
	}{
		{name: "user within conservative window", targetType: "user", age: 4 * time.Minute},
		{name: "user expired at endpoint limit", targetType: "user", age: 6 * time.Minute, wantErr: true},
		{name: "group within window", targetType: "group", age: 4 * time.Minute},
		{name: "group expired", targetType: "group", age: 6 * time.Minute, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metadata := QQBotDeliveryMetadata{Mode: "passive", EventID: "event-id", ReceivedAt: time.Now().Add(-test.age)}
			_, err := buildQQBotMessage(message, qqBotConfig{MessageType: "text"}, metadata, test.targetType)
			if (err != nil) != test.wantErr {
				t.Fatalf("buildQQBotMessage() error = %v, wantErr %v", err, test.wantErr)
			}
			if test.wantErr {
				var deliveryErr *common.DeliveryError
				if !errors.As(err, &deliveryErr) || deliveryErr.DeliveryCategory() != "expired_reply_window" {
					t.Fatalf("expected expired reply window error, got %v", err)
				}
			}
		})
	}
}

func TestBuildQQBotMessageOrdinaryActiveOmitsPassiveFields(t *testing.T) {
	message := &model.Message{Content: "scheduled notification"}
	metadata := QQBotDeliveryMetadata{
		Mode:       "active",
		EventID:    "old-event",
		MessageID:  "old-message",
		MessageSeq: 2,
		IsWakeup:   false,
	}
	payload, err := buildQQBotMessage(message, qqBotConfig{MessageType: "text"}, metadata, "user")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"event_id", "msg_id", "msg_seq", "is_wakeup"} {
		if strings.Contains(string(encoded), `"`+field+`"`) {
			t.Errorf("active payload must omit %s: %s", field, encoded)
		}
	}
}

func TestBuildQQBotMessageSupportsWakeupOnlyForUsers(t *testing.T) {
	message := &model.Message{Content: "come back"}
	metadata := QQBotDeliveryMetadata{Mode: "wakeup", EventID: "old-event", MessageID: "old-message"}
	payload, err := buildQQBotMessage(message, qqBotConfig{MessageType: "text"}, metadata, "user")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !payload.IsWakeup || strings.Contains(string(encoded), `"event_id"`) || strings.Contains(string(encoded), `"msg_id"`) {
		t.Fatalf("wakeup must set only is_wakeup among delivery metadata fields: %s", encoded)
	}
	metadata = QQBotDeliveryMetadata{IsWakeup: true}
	payload, err = buildQQBotMessage(message, qqBotConfig{DeliveryMode: "active", MessageType: "text"}, metadata, "user")
	if err != nil || !payload.IsWakeup {
		t.Fatalf("IsWakeup metadata should select the wakeup request mode, payload=%+v err=%v", payload, err)
	}
	_, err = buildQQBotMessage(message, qqBotConfig{MessageType: "text"}, metadata, "group")
	var deliveryErr *common.DeliveryError
	if !errors.As(err, &deliveryErr) || deliveryErr.DeliveryCategory() != "unsupported_capability" || deliveryErr.ProviderCode() != "40034123" {
		t.Fatalf("group wakeup should fail with an explicit capability error, got %v", err)
	}
}

func TestClassifyQQBotCurrentPermissionAndQuotaCodes(t *testing.T) {
	tests := []struct {
		code      string
		category  string
		retryable bool
	}{
		{code: "40034100", category: "rate_limit", retryable: true},
		{code: "40034105", category: "permission"},
		{code: "40054013", category: "recipient_refused"},
		{code: "40034122", category: "wakeup_quota"},
		{code: "40034123", category: "unsupported_capability"},
		{code: "40034128", category: "expired_reply_window"},
	}
	for _, test := range tests {
		err := classifyQQBotHTTPError(http.StatusBadRequest, test.code, "", 0)
		deliveryErr, ok := common.AsDeliveryError(err)
		if !ok || deliveryErr.DeliveryCategory() != test.category || deliveryErr.Retryable() != test.retryable || deliveryErr.ProviderCode() != test.code {
			t.Errorf("code %s classified as %+v, want category=%s retryable=%v", test.code, deliveryErr, test.category, test.retryable)
		}
	}
}

func TestParseQQBotConfigUsesActiveDefaultsAndCapsLimits(t *testing.T) {
	channel := &model.Channel{Other: `{"rate_limit_per_second":50,"bot_rate_limit_per_minute":200,"target_rate_limit_per_minute":200,"target_rate_limit_per_day":2000}`}
	config, err := parseQQBotConfig(channel)
	if err != nil {
		t.Fatal(err)
	}
	if config.DeliveryMode != "active" || config.MaxRequestsPerSecond != 5 || config.MaxBotPerMinute != 30 || config.MaxTargetPerMinute != 20 || config.MaxTargetPerDay != 1000 {
		t.Fatalf("unexpected conservative defaults/limits: %+v", config)
	}
	channel.Other = `{"bot_certified":true}`
	config, err = parseQQBotConfig(channel)
	if err != nil {
		t.Fatal(err)
	}
	if config.MaxRequestsPerSecond != 10 || config.MaxBotPerMinute != 600 {
		t.Fatalf("certified bot limits should follow the official 10 QPS ceiling: %+v", config)
	}
}

func TestQQBotTokenBusinessErrorsUseOfficialErrorCodes(t *testing.T) {
	rateLimit := classifyQQBotTokenBusinessError("100001", 15*time.Second)
	rateLimitError, ok := common.AsDeliveryError(rateLimit)
	if !ok || rateLimitError.DeliveryCategory() != "rate_limit" || !rateLimitError.Retryable() || rateLimitError.HTTPStatus() != http.StatusOK {
		t.Fatalf("unexpected token rate limit classification: %v", rateLimit)
	}
	credentialError, ok := common.AsDeliveryError(classifyQQBotTokenBusinessError("100016", 0))
	if !ok || credentialError.DeliveryCategory() != "authentication" || credentialError.Retryable() {
		t.Fatalf("unexpected token credential classification: %+v", credentialError)
	}
}

func TestQQBotTokenRefreshIsSerializedAcrossConcurrentRequests(t *testing.T) {
	var mu sync.Mutex
	refreshes := 0
	client := &qqBotClient{
		appID:        "app-1",
		clientSecret: "secret-1",
		httpClient: &http.Client{Transport: qqBotRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			mu.Lock()
			refreshes++
			mu.Unlock()
			return qqBotTestResponse(request, http.StatusOK, `{"access_token":"token-1","expires_in":"7200"}`), nil
		})},
		tokenCache: &qqBotTokenCache{},
	}
	const concurrentRequests = 24
	var wait sync.WaitGroup
	for index := 0; index < concurrentRequests; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if token, err := client.getToken(context.Background()); err != nil || token != "token-1" {
				t.Errorf("getToken() = %q, %v", token, err)
			}
		}()
	}
	wait.Wait()
	if refreshes != 1 {
		t.Fatalf("concurrent callers triggered %d token refreshes, want 1", refreshes)
	}
}

func TestQQBotSendRefreshesOnceAfterUnauthorized(t *testing.T) {
	var mu sync.Mutex
	tokenRequests := 0
	sendRequests := 0
	var authorizations []string
	client := &qqBotClient{
		appID:        "app-1",
		clientSecret: "secret-1",
		apiBaseURL:   qqBotOfficialAPIURL,
		httpClient: &http.Client{Transport: qqBotRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			mu.Lock()
			defer mu.Unlock()
			if request.URL.String() == qqBotTokenURL {
				tokenRequests++
				return qqBotTestResponse(request, http.StatusOK, `{"access_token":"token-2","expires_in":"7200"}`), nil
			}
			sendRequests++
			authorizations = append(authorizations, request.Header.Get("Authorization"))
			if sendRequests == 1 {
				return qqBotTestResponse(request, http.StatusUnauthorized, `{"code":401,"message":"expired"}`), nil
			}
			return qqBotTestResponse(request, http.StatusOK, `{"id":"provider-message-1"}`), nil
		})},
		tokenCache: &qqBotTokenCache{token: qqBotToken{value: "token-1", expiresAt: time.Now().Add(time.Hour)}},
	}
	result, err := client.send(context.Background(), "user", "user-openid", qqBotSendRequest{MessageType: 0, Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if result.HTTPStatus != http.StatusOK || result.ProviderMessageID != "provider-message-1" {
		t.Fatalf("unexpected QQBot send result: %+v", result)
	}
	if tokenRequests != 1 || sendRequests != 2 {
		t.Fatalf("token requests=%d message requests=%d, want 1 and 2", tokenRequests, sendRequests)
	}
	if len(authorizations) != 2 || authorizations[0] != "QQBot token-1" || authorizations[1] != "QQBot token-2" {
		t.Fatalf("unexpected authorization sequence: %v", authorizations)
	}
}

func TestQQBotSendRequiresConfirmedProviderResponse(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantAccepted bool
		wantUnknown  bool
		wantCode     string
	}{
		{name: "message id confirms acceptance", body: `{"id":"message-1"}`, wantAccepted: true},
		{name: "empty body is unknown", body: "", wantUnknown: true},
		{name: "malformed body is unknown", body: `{"id":`, wantUnknown: true},
		{name: "missing message id is unknown", body: `{"code":0}`, wantUnknown: true, wantCode: "0"},
		{name: "moderation accepted", body: `{"code":304023}`, wantAccepted: true, wantCode: "304023"},
		{name: "business permission error", body: `{"code":40034105,"message":"no active permission"}`, wantCode: "40034105"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &qqBotClient{
				apiBaseURL: qqBotOfficialAPIURL,
				httpClient: &http.Client{Transport: qqBotRoundTripFunc(func(request *http.Request) (*http.Response, error) {
					return qqBotTestResponse(request, http.StatusOK, test.body), nil
				})},
				tokenCache: &qqBotTokenCache{token: qqBotToken{value: "token", expiresAt: time.Now().Add(time.Hour)}},
			}
			result, err := client.send(context.Background(), "user", "openid", qqBotSendRequest{MessageType: 0, Content: "hello"})
			if test.wantAccepted {
				if err != nil || (test.wantCode == "" && result.ProviderMessageID == "") || result.ProviderCode != test.wantCode {
					t.Fatalf("expected accepted response, result=%+v err=%v", result, err)
				}
				return
			}
			deliveryErr, ok := common.AsDeliveryError(err)
			if !ok || deliveryErr.ProviderCode() != test.wantCode {
				t.Fatalf("expected typed response error with code %q, got result=%+v err=%v", test.wantCode, result, err)
			}
			if test.wantUnknown && (!deliveryErr.DeliveryUnknown() || !deliveryErr.SendAttempted() || deliveryErr.Retryable()) {
				t.Fatalf("response ambiguity must remain unknown and non-retryable: %+v", deliveryErr)
			}
			if test.wantCode == "40034105" && (deliveryErr.Retryable() || deliveryErr.DeliveryUnknown()) {
				t.Fatalf("permission errors must be definitive and non-retryable: %+v", deliveryErr)
			}
		})
	}
}

func TestQQBotTokenRejectsNonzeroCodeEvenWhenTokenIsPresent(t *testing.T) {
	client := &qqBotClient{
		appID:        "app-1",
		clientSecret: "secret-1",
		httpClient: &http.Client{Transport: qqBotRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			return qqBotTestResponse(request, http.StatusOK, `{"access_token":"must-not-use","expires_in":"7200","code":100016}`), nil
		})},
		tokenCache: &qqBotTokenCache{},
	}
	_, err := client.getToken(context.Background())
	deliveryErr, ok := common.AsDeliveryError(err)
	if !ok || deliveryErr.DeliveryCategory() != "authentication" || deliveryErr.ProviderCode() != "100016" {
		t.Fatalf("nonzero token business code must reject the token: %v", err)
	}
}

func qqBotTestResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}
