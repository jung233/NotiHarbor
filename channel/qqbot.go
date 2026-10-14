package channel

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"message-pusher/common"
	"message-pusher/model"
)

const (
	qqBotTokenURL         = "https://api.bot.qq.com/app/getAppAccessToken"
	qqBotOfficialAPIURL   = "https://api.bot.qq.com"
	qqBotLegacyAPIURL     = "https://api.sgroup.qq.com"
	qqBotSandboxAPIURL    = "https://sandbox.api.sgroup.qq.com"
	qqBotDefaultTimeout   = 8 * time.Second
	qqBotMaxBodyBytes     = 1 << 20
	qqBotTokenRefreshSkew = time.Minute
	qqBotMaxTokenLifetime = 2 * time.Hour
)

type qqBotConfig struct {
	BotName              string `json:"bot_name"`
	DefaultTarget        string `json:"default_target"`
	TargetType           string `json:"target_type"`
	DeliveryMode         string `json:"delivery_mode"`
	MessageType          string `json:"message_type"`
	MarkdownTemplateID   string `json:"markdown_template_id"`
	BotCertified         bool   `json:"bot_certified"`
	TimeoutSeconds       int    `json:"timeout_seconds"`
	MaxRequestsPerSecond int    `json:"rate_limit_per_second"`
	MaxBotPerMinute      int    `json:"bot_rate_limit_per_minute"`
	MaxTargetPerMinute   int    `json:"target_rate_limit_per_minute"`
	MaxTargetPerDay      int    `json:"target_rate_limit_per_day"`
}

type qqBotTokenResponse struct {
	AccessToken string          `json:"access_token"`
	ExpiresIn   json.RawMessage `json:"expires_in"`
	Code        json.RawMessage `json:"code"`
	Message     string          `json:"message"`
	RetryAfter  json.RawMessage `json:"retry_after"`
}

type qqBotToken struct {
	value     string
	expiresAt time.Time
}

type qqBotTokenCache struct {
	mu    sync.Mutex
	token qqBotToken
}

var qqBotTokens sync.Map // map[sha256(app id + secret)]*qqBotTokenCache

type qqBotClient struct {
	appID        string
	clientSecret string
	apiBaseURL   string
	httpClient   *http.Client
	tokenCache   *qqBotTokenCache
}

type qqBotSendRequest struct {
	Content     string         `json:"content,omitempty"`
	MessageType int            `json:"msg_type"`
	Markdown    *qqBotMarkdown `json:"markdown,omitempty"`
	EventID     string         `json:"event_id,omitempty"`
	MessageID   string         `json:"msg_id,omitempty"`
	MessageSeq  int            `json:"msg_seq,omitempty"`
	IsWakeup    bool           `json:"is_wakeup,omitempty"`
}

type qqBotMarkdown struct {
	Content          string                   `json:"content,omitempty"`
	CustomTemplateID string                   `json:"custom_template_id,omitempty"`
	Params           []qqBotMarkdownParameter `json:"params,omitempty"`
}

type qqBotMarkdownParameter struct {
	Key    string   `json:"key"`
	Values []string `json:"values"`
}

type qqBotSendResponse struct {
	ID         string          `json:"id"`
	Code       json.RawMessage `json:"code"`
	ErrCode    json.RawMessage `json:"err_code"`
	Message    string          `json:"message"`
	Timestamp  json.RawMessage `json:"timestamp"`
	RetryAfter json.RawMessage `json:"retry_after"`
}

func parseQQBotConfig(channel_ *model.Channel) (qqBotConfig, error) {
	var config qqBotConfig
	if strings.TrimSpace(channel_.Other) != "" {
		if err := json.Unmarshal([]byte(channel_.Other), &config); err != nil {
			return config, common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("invalid QQBot channel configuration"))
		}
	}
	if config.TargetType == "" {
		config.TargetType = "user"
	}
	if config.TargetType != "user" && config.TargetType != "group" {
		return config, common.NewDeliveryError("invalid_target", "", false, false, 0, 0, errors.New("QQBot target_type must be user or group"))
	}
	if config.DeliveryMode == "" {
		config.DeliveryMode = "active"
	}
	if config.DeliveryMode != "active" && config.DeliveryMode != "passive" && config.DeliveryMode != "wakeup" {
		return config, common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("QQBot delivery_mode must be active, passive, or wakeup"))
	}
	if config.MessageType == "" {
		config.MessageType = "text"
	}
	if config.MessageType != "text" && config.MessageType != "markdown" {
		return config, common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("QQBot message_type must be text or markdown"))
	}
	if config.TimeoutSeconds == 0 {
		config.TimeoutSeconds = int(qqBotDefaultTimeout.Seconds())
	}
	if config.TimeoutSeconds < 1 || config.TimeoutSeconds > 60 {
		return config, common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("QQBot timeout_seconds must be between 1 and 60"))
	}
	maxBotQPS := 5
	maxBotPerMinute := 30
	if config.BotCertified {
		maxBotQPS = 10
		maxBotPerMinute = 600
	}
	if config.MaxRequestsPerSecond == 0 {
		config.MaxRequestsPerSecond = maxBotQPS
	}
	if config.MaxRequestsPerSecond < 1 {
		return config, common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("QQBot rate_limit_per_second must be positive"))
	}
	if config.MaxRequestsPerSecond > maxBotQPS {
		config.MaxRequestsPerSecond = maxBotQPS
	}
	if config.MaxBotPerMinute == 0 {
		config.MaxBotPerMinute = maxBotPerMinute
	}
	if config.MaxBotPerMinute < 1 {
		return config, common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("QQBot bot_rate_limit_per_minute must be positive"))
	}
	if config.MaxBotPerMinute > maxBotPerMinute {
		config.MaxBotPerMinute = maxBotPerMinute
	}
	if config.MaxTargetPerMinute == 0 {
		config.MaxTargetPerMinute = 20
	}
	if config.MaxTargetPerMinute < 1 {
		return config, common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("QQBot target_rate_limit_per_minute must be positive"))
	}
	if config.MaxTargetPerMinute > 20 {
		config.MaxTargetPerMinute = 20
	}
	if config.MaxTargetPerDay == 0 {
		config.MaxTargetPerDay = 1000
	}
	if config.MaxTargetPerDay < 1 {
		return config, common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("QQBot target_rate_limit_per_day must be positive"))
	}
	if config.MaxTargetPerDay > 1000 {
		config.MaxTargetPerDay = 1000
	}
	return config, nil
}

func qqBotAPIBase(channel_ *model.Channel) (string, error) {
	base := strings.TrimSpace(channel_.URL)
	if base == "" {
		return qqBotOfficialAPIURL, nil
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("QQBot API base must be an official HTTPS endpoint"))
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("QQBot API base cannot include a path"))
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "api.bot.qq.com":
		if parsed.Port() != "" && parsed.Port() != "443" {
			return "", common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("QQBot API endpoint cannot use a custom port"))
		}
		return qqBotOfficialAPIURL, nil
	case "api.sgroup.qq.com":
		if parsed.Port() != "" && parsed.Port() != "443" {
			return "", common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("QQBot API endpoint cannot use a custom port"))
		}
		return qqBotLegacyAPIURL, nil
	case "sandbox.api.sgroup.qq.com":
		if parsed.Port() != "" && parsed.Port() != "443" {
			return "", common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("QQBot API endpoint cannot use a custom port"))
		}
		return qqBotSandboxAPIURL, nil
	default:
		return "", common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("QQBot API base must be api.bot.qq.com, api.sgroup.qq.com, or sandbox.api.sgroup.qq.com"))
	}
}

func newQQBotClient(channel_ *model.Channel, timeout time.Duration) (*qqBotClient, error) {
	apiBaseURL, err := qqBotAPIBase(channel_)
	if err != nil {
		return nil, err
	}
	appID := strings.TrimSpace(channel_.AppId)
	clientSecret := strings.TrimSpace(channel_.Secret)
	if appID == "" || clientSecret == "" {
		return nil, common.NewDeliveryError("authentication", "", false, false, 0, 0, errors.New("QQBot AppID and ClientSecret are required"))
	}
	cacheKey := sha256.Sum256([]byte(appID + "\x00" + clientSecret))
	cacheValue, _ := qqBotTokens.LoadOrStore(hex.EncodeToString(cacheKey[:]), &qqBotTokenCache{})
	return &qqBotClient{
		appID:        appID,
		clientSecret: clientSecret,
		apiBaseURL:   apiBaseURL,
		httpClient: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		tokenCache: cacheValue.(*qqBotTokenCache),
	}, nil
}

func parseQQBotExpiresIn(raw json.RawMessage) (time.Duration, error) {
	var seconds int64
	if len(raw) == 0 {
		return 0, errors.New("QQBot omitted expires_in")
	} else if raw[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return 0, err
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return 0, err
		}
		seconds = parsed
	} else {
		if err := json.Unmarshal(raw, &seconds); err != nil {
			return 0, err
		}
	}
	if seconds <= 0 || seconds > int64(qqBotMaxTokenLifetime/time.Second) {
		return 0, errors.New("QQBot returned an invalid token lifetime")
	}
	return time.Duration(seconds) * time.Second, nil
}

func (c *qqBotClient) getToken(ctx context.Context) (string, error) {
	c.tokenCache.mu.Lock()
	defer c.tokenCache.mu.Unlock()
	if c.tokenCache.token.value != "" && time.Until(c.tokenCache.token.expiresAt) > qqBotTokenRefreshSkew {
		return c.tokenCache.token.value, nil
	}
	return c.refreshTokenLocked(ctx)
}

func (c *qqBotClient) refreshAfterUnauthorized(ctx context.Context, rejectedToken string) (string, error) {
	c.tokenCache.mu.Lock()
	defer c.tokenCache.mu.Unlock()
	// Another request may already have refreshed this token after its own 401.
	if c.tokenCache.token.value != "" && c.tokenCache.token.value != rejectedToken && time.Until(c.tokenCache.token.expiresAt) > 0 {
		return c.tokenCache.token.value, nil
	}
	c.tokenCache.token = qqBotToken{}
	return c.refreshTokenLocked(ctx)
}

func (c *qqBotClient) refreshTokenLocked(ctx context.Context) (string, error) {
	requestBody, err := json.Marshal(map[string]string{
		"appId":        c.appID,
		"clientSecret": c.clientSecret,
	})
	if err != nil {
		return "", common.NewDeliveryError("authentication", "", false, false, 0, 0, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, qqBotTokenURL, strings.NewReader(string(requestBody)))
	if err != nil {
		return "", common.NewDeliveryError("authentication", "", false, false, 0, 0, err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return "", classifyQQBotTransportError(err, false)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, qqBotMaxBodyBytes+1))
	if err != nil {
		return "", common.NewDeliveryError("network", "", true, false, 0, response.StatusCode, err)
	}
	if len(body) > qqBotMaxBodyBytes {
		return "", common.NewDeliveryError("provider_response", "", false, false, 0, response.StatusCode, errors.New("QQBot token response exceeded size limit"))
	}
	var tokenResponse qqBotTokenResponse
	_ = json.Unmarshal(body, &tokenResponse)
	providerCode := rawJSONValue(tokenResponse.Code)
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices && providerCode != "" && providerCode != "0" {
		return "", classifyQQBotTokenBusinessError(providerCode, parseRetryAfter(response.Header, tokenResponse.RetryAfter))
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices || tokenResponse.AccessToken == "" {
		retryAfter := parseRetryAfter(response.Header, tokenResponse.RetryAfter)
		if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
			return "", classifyQQBotTokenBusinessError(providerCode, retryAfter)
		}
		return "", classifyQQBotHTTPError(response.StatusCode, providerCode, tokenResponse.Message, retryAfter)
	}
	expiresIn, err := parseQQBotExpiresIn(tokenResponse.ExpiresIn)
	if err != nil {
		return "", common.NewDeliveryError("provider_response", providerCode, false, false, 0, response.StatusCode, errors.New("QQBot token response omitted a valid expires_in"))
	}
	c.tokenCache.token = qqBotToken{value: tokenResponse.AccessToken, expiresAt: time.Now().Add(expiresIn)}
	return tokenResponse.AccessToken, nil
}

func (c *qqBotClient) send(ctx context.Context, targetType, targetOpenID string, payload qqBotSendRequest) (DeliveryResult, error) {
	path := "/v2/users/" + url.PathEscape(targetOpenID) + "/messages"
	if targetType == "group" {
		path = "/v2/groups/" + url.PathEscape(targetOpenID) + "/messages"
	}
	token, err := c.getToken(ctx)
	if err != nil {
		return DeliveryResult{}, err
	}
	sendCtx, attempted := qqBotAttemptContext(ctx)
	response, body, err := c.sendWithToken(sendCtx, path, token, payload)
	if response == nil {
		if err != nil {
			if _, ok := common.AsDeliveryError(err); ok {
				return DeliveryResult{}, common.MarkDeliveryAttempted(err, attempted.Load())
			}
			return DeliveryResult{}, classifyQQBotTransportError(err, attempted.Load())
		}
		return DeliveryResult{}, common.NewDeliveryError("network", "", true, false, 0, 0, errors.New("QQBot returned no HTTP response"))
	}
	if response.StatusCode == http.StatusUnauthorized {
		token, err = c.refreshAfterUnauthorized(ctx, token)
		if err != nil {
			return DeliveryResult{HTTPStatus: response.StatusCode}, common.MarkDeliveryAttempted(err, true)
		}
		sendCtx, attempted = qqBotAttemptContext(ctx)
		response, body, err = c.sendWithToken(sendCtx, path, token, payload)
		if response == nil {
			if err != nil {
				if _, ok := common.AsDeliveryError(err); ok {
					return DeliveryResult{}, common.MarkDeliveryAttempted(err, true)
				}
				return DeliveryResult{}, classifyQQBotTransportError(err, true)
			}
			return DeliveryResult{}, common.NewDeliveryError("network", "", true, false, 0, 0, errors.New("QQBot returned no HTTP response"))
		}
	}
	var provider qqBotSendResponse
	if err == nil && len(body) > 0 {
		err = json.Unmarshal(body, &provider)
	}
	providerCode := rawJSONValue(provider.ErrCode)
	if providerCode == "" {
		providerCode = rawJSONValue(provider.Code)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		retryAfter := parseRetryAfter(response.Header, provider.RetryAfter)
		return DeliveryResult{HTTPStatus: response.StatusCode, ProviderCode: providerCode}, common.MarkDeliveryAttempted(classifyQQBotHTTPError(response.StatusCode, providerCode, provider.Message, retryAfter), true)
	}
	if err == nil && (providerCode == "304023" || providerCode == "304024") {
		// These official moderation responses mean the message was accepted for
		// review, though it may not be visible to recipients until approved.
		return DeliveryResult{HTTPStatus: response.StatusCode, ProviderCode: providerCode, ProviderMessageID: provider.ID}, nil
	}
	if err != nil {
		return DeliveryResult{HTTPStatus: response.StatusCode, ProviderCode: providerCode}, qqBotUnknownResponseError(response.StatusCode, providerCode)
	}
	if providerCode != "" && providerCode != "0" {
		retryAfter := parseRetryAfter(response.Header, provider.RetryAfter)
		return DeliveryResult{HTTPStatus: response.StatusCode, ProviderCode: providerCode}, common.MarkDeliveryAttempted(classifyQQBotHTTPError(response.StatusCode, providerCode, provider.Message, retryAfter), true)
	}
	if provider.ID == "" {
		return DeliveryResult{HTTPStatus: response.StatusCode, ProviderCode: providerCode}, qqBotUnknownResponseError(response.StatusCode, providerCode)
	}
	return DeliveryResult{HTTPStatus: response.StatusCode, ProviderCode: providerCode, ProviderMessageID: provider.ID}, nil
}

func qqBotUnknownResponseError(status int, providerCode string) error {
	return common.NewDeliveryErrorWithAttempted(
		"provider_response_unknown", providerCode, false, true, true, 0, status,
		errors.New("QQBot returned a success status without a valid message identifier; delivery outcome is unknown"),
	)
}

func qqBotAttemptContext(ctx context.Context) (context.Context, *atomic.Bool) {
	attempted := &atomic.Bool{}
	trace := &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { attempted.Store(true) }}
	return httptrace.WithClientTrace(ctx, trace), attempted
}

func (c *qqBotClient) sendWithToken(ctx context.Context, path, token string, payload qqBotSendRequest) (*http.Response, []byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, common.NewDeliveryError("invalid_request", "", false, false, 0, 0, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiBaseURL+path, strings.NewReader(string(body)))
	if err != nil {
		return nil, nil, common.NewDeliveryError("invalid_request", "", false, false, 0, 0, err)
	}
	request.Header.Set("Authorization", "QQBot "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, nil, err
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, qqBotMaxBodyBytes+1))
	closeErr := response.Body.Close()
	if readErr != nil {
		return response, nil, readErr
	}
	if closeErr != nil {
		return response, nil, closeErr
	}
	if len(responseBody) > qqBotMaxBodyBytes {
		return response, nil, common.NewDeliveryError("provider_response", "", false, false, 0, response.StatusCode, errors.New("QQBot response exceeded size limit"))
	}
	return response, responseBody, nil
}

func rawJSONValue(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var stringValue string
	if json.Unmarshal(raw, &stringValue) == nil {
		return stringValue
	}
	return string(raw)
}

func classifyQQBotHTTPError(status int, providerCode, message string, retryAfter time.Duration) error {
	switch providerCode {
	case "22009", "40034100":
		return common.NewDeliveryError("rate_limit", providerCode, true, false, retryAfter, status, errors.New("QQBot rate limit exceeded"))
	case "40034105":
		return common.NewDeliveryError("permission", providerCode, false, false, 0, status, errors.New("QQBot active message permission is unavailable for this bot or target"))
	case "40054013":
		return common.NewDeliveryError("recipient_refused", providerCode, false, false, 0, status, errors.New("QQBot recipient has disabled or refused bot messages"))
	case "40034122":
		return common.NewDeliveryError("wakeup_quota", providerCode, false, false, 0, status, errors.New("QQBot interaction recall quota for this period is exhausted"))
	case "40034123":
		return common.NewDeliveryError("unsupported_capability", providerCode, false, false, 0, status, errors.New("QQBot does not support interaction recall for this target"))
	case "40034128":
		return common.NewDeliveryError("expired_reply_window", providerCode, false, false, 0, status, errors.New("QQBot passive reply window or reply count limit was exceeded"))
	}
	if status == http.StatusTooManyRequests {
		return common.NewDeliveryError("rate_limit", providerCode, true, false, retryAfter, status, errors.New("QQBot rate limit exceeded"))
	}
	if status == http.StatusUnauthorized {
		return common.NewDeliveryError("authentication", providerCode, false, false, 0, status, errors.New("QQBot rejected the refreshed token or credentials"))
	}
	if status >= 500 {
		return common.NewDeliveryError("provider_server", providerCode, true, false, retryAfter, status, errors.New("QQBot returned a server error"))
	}
	if status == http.StatusForbidden {
		category := "permission"
		if strings.Contains(strings.ToLower(message), "target") || strings.Contains(strings.ToLower(message), "openid") {
			category = "invalid_target"
		}
		return common.NewDeliveryError(category, providerCode, false, false, 0, status, errors.New("QQBot rejected the target or message permission"))
	}
	if status == http.StatusNotFound {
		return common.NewDeliveryError("invalid_target", providerCode, false, false, 0, status, errors.New("QQBot target was not found"))
	}
	if status >= 400 {
		return common.NewDeliveryError("invalid_request", providerCode, false, false, 0, status, errors.New("QQBot rejected the request"))
	}
	return common.NewDeliveryError("provider_error", providerCode, false, false, 0, status, fmt.Errorf("QQBot API failed with status %d", status))
}

func classifyQQBotTokenBusinessError(providerCode string, retryAfter time.Duration) error {
	switch providerCode {
	case "100001":
		return common.NewDeliveryError("rate_limit", providerCode, true, false, retryAfter, http.StatusOK, errors.New("QQBot token endpoint rate limit was reached"))
	case "100007", "100016", "10004":
		return common.NewDeliveryError("authentication", providerCode, false, false, 0, http.StatusOK, errors.New("QQBot application credentials or bot status were rejected"))
	default:
		return common.NewDeliveryError("provider_error", providerCode, false, false, 0, http.StatusOK, errors.New("QQBot token endpoint returned a business error"))
	}
}

func classifyQQBotTransportError(err error, requestMayHaveBeenAccepted bool) error {
	if err == nil {
		return nil
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return common.NewDeliveryError("dns", "", true, false, 0, 0, errors.New("QQBot request DNS lookup failed"))
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return common.NewDeliveryError("network", "", true, false, 0, 0, errors.New("QQBot connection failed"))
	}
	if errors.Is(err, context.Canceled) {
		return common.NewDeliveryErrorWithAttempted("cancelled", "", false, requestMayHaveBeenAccepted, requestMayHaveBeenAccepted, 0, 0, errors.New("QQBot request was cancelled"))
	}
	if errors.Is(err, context.DeadlineExceeded) || isTimeoutError(err) {
		return common.NewDeliveryErrorWithAttempted("timeout", "", !requestMayHaveBeenAccepted, requestMayHaveBeenAccepted, requestMayHaveBeenAccepted, 0, 0, errors.New("QQBot request timed out; delivery may be unknown"))
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	if requestMayHaveBeenAccepted {
		return common.NewDeliveryErrorWithAttempted("network", "", false, true, true, 0, 0, errors.New("QQBot connection ended before the delivery result was received"))
	}
	return common.NewDeliveryError("network", "", true, false, 0, 0, errors.New("QQBot connection failed"))
}

func isTimeoutError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func parseRetryAfter(header http.Header, bodyValue json.RawMessage) time.Duration {
	if raw := strings.TrimSpace(header.Get("Retry-After")); raw != "" {
		if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds >= 0 {
			return time.Duration(seconds) * time.Second
		}
		if timestamp, err := http.ParseTime(raw); err == nil {
			if delay := time.Until(timestamp); delay > 0 {
				return delay
			}
		}
	}
	if raw := strings.TrimSpace(rawJSONValue(bodyValue)); raw != "" {
		if seconds, err := strconv.ParseFloat(raw, 64); err == nil && seconds >= 0 && seconds < math.MaxInt32 {
			return time.Duration(seconds * float64(time.Second))
		}
	}
	return 0
}

const qqBotRateLimitScript = `
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local windows = {tonumber(ARGV[1]), tonumber(ARGV[3]), tonumber(ARGV[5]), tonumber(ARGV[7])}
local limits = {tonumber(ARGV[2]), tonumber(ARGV[4]), tonumber(ARGV[6]), tonumber(ARGV[8])}
local wait = 0
for i = 1, 4 do
  redis.call('ZREMRANGEBYSCORE', KEYS[i], '-inf', now - windows[i])
  local count = redis.call('ZCARD', KEYS[i])
  if count >= limits[i] then
    local oldest = redis.call('ZRANGE', KEYS[i], 0, 0, 'WITHSCORES')
    local delay = tonumber(oldest[2]) + windows[i] - now
    if delay > wait then wait = delay end
  end
end
if wait > 0 then return {0, wait} end
for i = 1, 4 do
  redis.call('ZADD', KEYS[i], now, ARGV[9])
  redis.call('PEXPIRE', KEYS[i], windows[i] + 1000)
end
return {1, 0}
`

func reserveQQBotRateLimit(ctx context.Context, channel_ *model.Channel, targetType, targetOpenID string, config qqBotConfig) error {
	if !common.RedisEnabled || common.RDB == nil {
		return common.NewDeliveryError("rate_limit_backend", "", true, false, 30*time.Second, 0, errors.New("QQBot sending requires Redis for shared rate limiting"))
	}
	botHash := sha256.Sum256([]byte(strings.TrimSpace(channel_.AppId)))
	targetHash := sha256.Sum256([]byte(targetType + "\x00" + targetOpenID))
	botPerMinute := config.MaxBotPerMinute
	if targetType == "group" {
		groupMaximum := 30
		if config.BotCertified {
			groupMaximum = 60
		}
		if botPerMinute > groupMaximum {
			botPerMinute = groupMaximum
		}
	}
	keys := []string{
		"message-pusher:qqbot:v1:rate:" + hex.EncodeToString(botHash[:]) + ":" + targetType + ":bot",
		"message-pusher:qqbot:v1:rate:" + hex.EncodeToString(botHash[:]) + ":" + targetType + ":bot-minute",
		"message-pusher:qqbot:v1:rate:" + hex.EncodeToString(botHash[:]) + ":target:" + hex.EncodeToString(targetHash[:]),
		"message-pusher:qqbot:v1:rate:" + hex.EncodeToString(botHash[:]) + ":target-day:" + hex.EncodeToString(targetHash[:]),
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return common.NewDeliveryError("internal", "", true, false, time.Second, 0, errors.New("could not allocate QQBot rate limit token"))
	}
	member := hex.EncodeToString(nonce[:])
	result, err := common.RDB.Eval(ctx, qqBotRateLimitScript, keys,
		1000, config.MaxRequestsPerSecond,
		60_000, botPerMinute,
		60_000, config.MaxTargetPerMinute,
		86_400_000, config.MaxTargetPerDay,
		member,
	).Slice()
	if err != nil {
		return common.NewDeliveryError("rate_limit_backend", "", true, false, 30*time.Second, 0, errors.New("shared QQBot rate limiter is unavailable"))
	}
	if len(result) != 2 {
		return common.NewDeliveryError("rate_limit_backend", "", true, false, 30*time.Second, 0, errors.New("shared QQBot rate limiter returned an invalid result"))
	}
	allowed, allowedOK := result[0].(int64)
	waitMillis, waitOK := result[1].(int64)
	if !allowedOK || !waitOK {
		return common.NewDeliveryError("rate_limit_backend", "", true, false, 30*time.Second, 0, errors.New("shared QQBot rate limiter returned an invalid result"))
	}
	if allowed == 0 {
		if waitMillis < 1 {
			waitMillis = 1
		}
		return common.NewDeliveryError("rate_limit", "", true, false, time.Duration(waitMillis)*time.Millisecond, 429, errors.New("QQBot conservative shared rate limit reached"))
	}
	return nil
}

func resolveQQBotTarget(message *model.Message, channel_ *model.Channel, config qqBotConfig, metadata QQBotDeliveryMetadata) (string, string, error) {
	targetType := config.TargetType
	if metadata.TargetType != "" {
		targetType = metadata.TargetType
	}
	if targetType != "user" && targetType != "group" {
		return "", "", common.NewDeliveryError("invalid_target", "", false, false, 0, 0, errors.New("QQBot target type must be user or group"))
	}
	targetOpenID := strings.TrimSpace(metadata.TargetOpenID)
	if targetOpenID == "" && message != nil {
		targetOpenID = strings.TrimSpace(message.To)
		if targetOpenID == "" {
			targetOpenID = strings.TrimSpace(message.OpenId)
		}
	}
	if targetOpenID == "" {
		targetOpenID = strings.TrimSpace(config.DefaultTarget)
	}
	if targetOpenID == "" {
		targetOpenID = strings.TrimSpace(channel_.AccountId)
	}
	if targetOpenID == "" {
		return "", "", common.NewDeliveryError("invalid_target", "", false, false, 0, 0, errors.New("QQBot target OpenID is required"))
	}
	if len(targetOpenID) > 256 {
		return "", "", common.NewDeliveryError("invalid_target", "", false, false, 0, 0, errors.New("QQBot target OpenID is too long"))
	}
	for _, r := range targetOpenID {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '=') {
			return "", "", common.NewDeliveryError("invalid_target", "", false, false, 0, 0, errors.New("QQBot target must be a QQ OpenID"))
		}
	}
	return targetType, targetOpenID, nil
}

func buildQQBotMessage(message *model.Message, config qqBotConfig, metadata QQBotDeliveryMetadata, targetType string) (qqBotSendRequest, error) {
	mode := metadata.Mode
	if mode == "" {
		mode = config.DeliveryMode
	}
	if metadata.IsWakeup {
		mode = "wakeup"
	}
	payload := qqBotSendRequest{}
	switch mode {
	case "active":
		// An ordinary proactive message has no message/event correlation fields.
	case "wakeup":
		if targetType != "user" {
			return qqBotSendRequest{}, common.NewDeliveryError("unsupported_capability", "40034123", false, false, 0, 0, errors.New("QQBot interaction recall is supported only for C2C user targets"))
		}
		payload.IsWakeup = true
	case "passive":
		if metadata.EventID == "" && metadata.MessageID == "" {
			return qqBotSendRequest{}, common.NewDeliveryError("unsupported_capability", "", false, false, 0, 0, errors.New("QQBot passive delivery requires event_id or msg_id from a verified incoming event"))
		}
		if metadata.ReceivedAt.IsZero() {
			return qqBotSendRequest{}, common.NewDeliveryError("invalid_request", "", false, false, 0, 0, errors.New("QQBot passive delivery requires the incoming event timestamp"))
		}
		// The current C2C overview says 60 minutes, while the msg_id endpoint
		// row still says 5 minutes. Enforce the shorter documented window until
		// Tencent resolves that inconsistency.
		window := 5 * time.Minute
		maxReplies := 5
		if targetType == "group" {
			window = 5 * time.Minute
		} else {
			maxReplies = 4
		}
		if time.Since(metadata.ReceivedAt) > window || time.Until(metadata.ReceivedAt) > 5*time.Minute {
			return qqBotSendRequest{}, common.NewDeliveryError("expired_reply_window", "", false, false, 0, 0, errors.New("QQBot passive reply window has expired"))
		}
		if metadata.MessageSeq < 0 || metadata.MessageSeq > maxReplies {
			return qqBotSendRequest{}, common.NewDeliveryError("invalid_request", "40034128", false, false, 0, 0, fmt.Errorf("QQBot passive reply msg_seq must be between 0 and %d", maxReplies))
		}
		if metadata.MessageID != "" {
			payload.MessageID = metadata.MessageID
			payload.MessageSeq = metadata.MessageSeq
			if payload.MessageSeq == 0 {
				payload.MessageSeq = 1
			}
		} else {
			payload.EventID = metadata.EventID
		}
	default:
		return qqBotSendRequest{}, common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("QQBot delivery_mode must be active, passive, or wakeup"))
	}
	content := message.Content
	if message.Description != "" {
		content = message.Description + "\n\n" + content
	}
	if message.Title != "" && message.Title != common.SystemName {
		content = "# " + message.Title + "\n\n" + content
	}
	messageType := metadata.MessageType
	if messageType == "" && message.RenderMode == "markdown" {
		messageType = "markdown"
	}
	if messageType == "" {
		messageType = config.MessageType
	}
	if content == "" {
		return qqBotSendRequest{}, common.NewDeliveryError("invalid_request", "", false, false, 0, 0, errors.New("QQBot message content is empty"))
	}
	switch messageType {
	case "text":
		payload.MessageType = 0
		payload.Content = content
	case "markdown":
		payload.MessageType = 2
		payload.Markdown = &qqBotMarkdown{Content: content}
		if config.MarkdownTemplateID != "" {
			payload.Markdown = &qqBotMarkdown{
				CustomTemplateID: config.MarkdownTemplateID,
				Params: []qqBotMarkdownParameter{
					{Key: "title", Values: []string{message.Title}},
					{Key: "description", Values: []string{message.Description}},
					{Key: "content", Values: []string{message.Content}},
				},
			}
		}
	default:
		return qqBotSendRequest{}, common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("QQBot message_type must be text or markdown"))
	}
	return payload, nil
}

// Dispatch provides the queue-facing context-aware send API for adapters
// implemented in this package. Existing channels remain available through
// SendMessage and are wrapped here for compatibility with the queue.
func Dispatch(ctx context.Context, message *model.Message, user *model.User, channel_ *model.Channel) (DeliveryResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if message == nil || user == nil || channel_ == nil {
		return DeliveryResult{}, common.NewDeliveryError("invalid_request", "", false, false, 0, 0, errors.New("message, user, and channel are required"))
	}
	switch channel_.Type {
	case model.TypeQQBotV2:
		return dispatchQQBot(ctx, message, channel_)
	case model.TypeEmail:
		return DeliveryResult{}, SendEmailMessageContext(ctx, message, user, channel_)
	case model.TypeCustom:
		return SendCustomMessageContext(ctx, message, user, channel_)
	default:
		return DeliveryResult{}, SendMessage(message, user, channel_)
	}
}

// SendQQBotMessage preserves a simple error-only entry point for older
// callers. New queue code should pass its context to Dispatch.
func SendQQBotMessage(message *model.Message, user *model.User, channel_ *model.Channel) error {
	_, err := Dispatch(context.Background(), message, user, channel_)
	return err
}

func dispatchQQBot(ctx context.Context, message *model.Message, channel_ *model.Channel) (DeliveryResult, error) {
	config, err := parseQQBotConfig(channel_)
	if err != nil {
		return DeliveryResult{}, err
	}
	metadata, _ := QQBotDeliveryMetadataFromContext(ctx)
	targetType, targetOpenID, err := resolveQQBotTarget(message, channel_, config, metadata)
	if err != nil {
		return DeliveryResult{}, err
	}
	payload, err := buildQQBotMessage(message, config, metadata, targetType)
	if err != nil {
		return DeliveryResult{}, err
	}
	timeout := time.Duration(config.TimeoutSeconds) * time.Second
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client, err := newQQBotClient(channel_, timeout)
	if err != nil {
		return DeliveryResult{}, err
	}
	if err = reserveQQBotRateLimit(requestCtx, channel_, targetType, targetOpenID, config); err != nil {
		return DeliveryResult{}, err
	}
	return client.send(requestCtx, targetType, targetOpenID, payload)
}
