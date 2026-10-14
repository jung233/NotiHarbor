package channel

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"message-pusher/common"
	"message-pusher/model"
)

const (
	customWebhookTimeout = 10 * time.Second
	customWebhookMaxBody = 1 << 20
)

type customChannelConfig struct {
	BodyTemplate        string            `json:"body_template"`
	Headers             map[string]string `json:"headers"`
	AuthHeader          string            `json:"auth_header"`
	AuthTemplate        string            `json:"auth_template"`
	SuccessStatusCodes  []int             `json:"success_status_codes"`
	TimeoutSeconds      int               `json:"timeout_seconds"`
	AllowPrivateNetwork bool              `json:"allow_private_network"`
}

// SendCustomMessage preserves the original error-only entry point.
func SendCustomMessage(message *model.Message, user *model.User, channel_ *model.Channel) error {
	_, err := SendCustomMessageContext(context.Background(), message, user, channel_)
	return err
}

// SendCustomMessageContext posts a JSON template to a pinned HTTPS destination.
// Custom channel credentials are read only from Channel.Secret.
func SendCustomMessageContext(ctx context.Context, message *model.Message, user *model.User, channel_ *model.Channel) (DeliveryResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if message == nil || user == nil || channel_ == nil {
		return DeliveryResult{}, common.NewDeliveryError("invalid_request", "", false, false, 0, 0, errors.New("message, user, and channel are required"))
	}
	endpoint, err := parseCustomWebhookURL(channel_.URL)
	if err != nil {
		return DeliveryResult{}, err
	}
	config, legacyBody, err := parseCustomChannelConfig(channel_.Other)
	if err != nil {
		return DeliveryResult{}, err
	}
	bodyTemplate := config.BodyTemplate
	if legacyBody {
		bodyTemplate = channel_.Other
	}
	body, err := renderCustomBody(bodyTemplate, message)
	if err != nil {
		return DeliveryResult{}, err
	}
	if len(body) == 0 || !json.Valid(body) {
		return DeliveryResult{}, common.NewDeliveryError("invalid_request", "", false, false, 0, 0, errors.New("custom channel request body must be valid JSON"))
	}
	if len(body) > customWebhookMaxBody {
		return DeliveryResult{}, common.NewDeliveryError("invalid_request", "", false, false, 0, 0, errors.New("custom channel request body exceeds the size limit"))
	}
	if config.TimeoutSeconds == 0 {
		config.TimeoutSeconds = int(customWebhookTimeout.Seconds())
	}
	if config.TimeoutSeconds < 1 || config.TimeoutSeconds > 120 {
		return DeliveryResult{}, common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("custom channel timeout_seconds must be between 1 and 120"))
	}
	if len(config.SuccessStatusCodes) == 0 {
		if legacyBody {
			config.SuccessStatusCodes = []int{http.StatusOK}
		} else {
			for status := 200; status < 300; status++ {
				config.SuccessStatusCodes = append(config.SuccessStatusCodes, status)
			}
		}
	}
	successCodes, err := normalizeCustomSuccessCodes(config.SuccessStatusCodes)
	if err != nil {
		return DeliveryResult{}, err
	}
	if err = validateCustomHeaders(config, channel_.Secret); err != nil {
		return DeliveryResult{}, err
	}
	timeout := time.Duration(config.TimeoutSeconds) * time.Second
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err = validateCustomWebhookDNS(requestCtx, endpoint, config.AllowPrivateNetwork); err != nil {
		return DeliveryResult{}, err
	}
	var attempted atomic.Bool
	requestCtx = httptrace.WithClientTrace(requestCtx, &httptrace.ClientTrace{
		WroteRequest: func(httptrace.WroteRequestInfo) { attempted.Store(true) },
	})
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return DeliveryResult{}, common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("custom channel URL is invalid"))
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	for name, template := range config.Headers {
		value, renderErr := renderCustomHeader(template, message, channel_.Secret)
		if renderErr != nil {
			return DeliveryResult{}, renderErr
		}
		request.Header.Set(name, value)
	}
	if config.AuthTemplate != "" {
		headerName := config.AuthHeader
		if headerName == "" {
			headerName = "Authorization"
		}
		value, renderErr := renderCustomHeader(config.AuthTemplate, message, channel_.Secret)
		if renderErr != nil {
			return DeliveryResult{}, renderErr
		}
		request.Header.Set(headerName, value)
	}

	transport := &http.Transport{
		Proxy:                 nil,
		DisableKeepAlives:     true,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		DialContext:           customPinnedDialContext(endpoint.Hostname(), endpoint.Port(), config.AllowPrivateNetwork),
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	defer transport.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return DeliveryResult{}, classifyCustomWebhookTransportError(err, attempted.Load())
	}
	defer response.Body.Close()
	providerCode := ""
	result := DeliveryResult{HTTPStatus: response.StatusCode}
	if _, ok := successCodes[response.StatusCode]; ok {
		return result, nil
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, customWebhookMaxBody+1))
	if len(responseBody) > customWebhookMaxBody {
		return DeliveryResult{HTTPStatus: response.StatusCode}, common.NewDeliveryErrorWithAttempted("provider_response", "", false, false, true, 0, response.StatusCode, errors.New("custom provider response exceeded the size limit"))
	}
	if readErr != nil {
		return DeliveryResult{HTTPStatus: response.StatusCode}, common.NewDeliveryErrorWithAttempted("provider_response", "", response.StatusCode >= 500, false, true, parseRetryAfter(response.Header, nil), response.StatusCode, errors.New("could not read custom provider response"))
	}
	providerCode = customProviderCode(responseBody)
	result.ProviderCode = providerCode
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return result, common.NewDeliveryErrorWithAttempted("redirect_rejected", providerCode, false, false, true, 0, response.StatusCode, errors.New("custom channel redirects are not permitted"))
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return result, common.NewDeliveryErrorWithAttempted("rate_limit", providerCode, true, false, true, parseRetryAfter(response.Header, nil), response.StatusCode, errors.New("custom provider rate limit was reached"))
	}
	if response.StatusCode >= 500 {
		return result, common.NewDeliveryErrorWithAttempted("provider_server", providerCode, true, false, true, parseRetryAfter(response.Header, nil), response.StatusCode, errors.New("custom provider returned a server error"))
	}
	if response.StatusCode >= 400 {
		return result, common.NewDeliveryErrorWithAttempted("provider_rejected", providerCode, false, false, true, 0, response.StatusCode, errors.New("custom provider rejected the request"))
	}
	return result, common.NewDeliveryErrorWithAttempted("provider_response", providerCode, false, false, true, 0, response.StatusCode, fmt.Errorf("custom provider returned unexpected status %d", response.StatusCode))
}

func parseCustomChannelConfig(other string) (customChannelConfig, bool, error) {
	config := customChannelConfig{}
	trimmed := strings.TrimSpace(other)
	if trimmed == "" {
		return config, true, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &fields); err != nil {
		return config, true, nil
	}
	_, hasConfig := fields["body_template"]
	for _, key := range []string{"headers", "auth_header", "auth_template", "success_status_codes", "timeout_seconds", "allow_private_network"} {
		if _, ok := fields[key]; ok {
			hasConfig = true
		}
	}
	if !hasConfig {
		return config, true, nil
	}
	if err := json.Unmarshal([]byte(trimmed), &config); err != nil {
		return customChannelConfig{}, false, common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("invalid custom channel configuration"))
	}
	if config.BodyTemplate == "" {
		return customChannelConfig{}, false, common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("custom channel body_template is required"))
	}
	return config, false, nil
}

func parseCustomWebhookURL(raw string) (*url.URL, error) {
	endpoint, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || endpoint == nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.Fragment != "" {
		return nil, common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("custom channel URL must be an HTTPS URL without user info or a fragment"))
	}
	if endpoint.Opaque != "" || strings.ContainsAny(endpoint.Host, "\\\r\n") {
		return nil, common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("custom channel URL is invalid"))
	}
	for name := range endpoint.Query() {
		lower := strings.ToLower(name)
		for _, sensitive := range []string{"token", "secret", "password", "credential", "api_key", "apikey", "auth"} {
			if strings.Contains(lower, sensitive) {
				return nil, common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("custom channel credentials must be stored in Secret, not the URL"))
			}
		}
	}
	if port := endpoint.Port(); port != "" {
		parsedPort, parseErr := strconv.Atoi(port)
		if parseErr != nil || parsedPort < 1 || parsedPort > 65535 {
			return nil, common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("custom channel URL port is invalid"))
		}
	}
	return endpoint, nil
}

func renderCustomBody(template string, message *model.Message) ([]byte, error) {
	if message == nil {
		return nil, common.NewDeliveryError("invalid_request", "", false, false, 0, 0, errors.New("message is required"))
	}
	content := message.Content
	if content == "" {
		content = message.Desp
	}
	description := message.Description
	if description == "" {
		description = message.Short
	}
	urlValue := message.URL
	if urlValue == "" {
		urlValue = message.Link
	}
	template = strings.NewReplacer(
		"$title", message.Title,
		"$description", description,
		"$content", content,
		"$url", urlValue,
		"$to", message.To,
	).Replace(template)
	return []byte(template), nil
}

func renderCustomHeader(template string, message *model.Message, secret string) (string, error) {
	if strings.Contains(template, "{{secret}}") {
		if secret == "" {
			return "", common.NewDeliveryError("authentication", "", false, false, 0, 0, errors.New("custom channel Secret is required by the configured auth template"))
		}
		template = strings.ReplaceAll(template, "{{secret}}", secret)
	}
	if message != nil {
		content := message.Content
		if content == "" {
			content = message.Desp
		}
		description := message.Description
		if description == "" {
			description = message.Short
		}
		urlValue := message.URL
		if urlValue == "" {
			urlValue = message.Link
		}
		template = strings.NewReplacer(
			"$title", message.Title,
			"$description", description,
			"$content", content,
			"$url", urlValue,
			"$to", message.To,
		).Replace(template)
	}
	if strings.Contains(template, "{{secret}}") || strings.ContainsAny(template, "\r\n") {
		return "", common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("custom channel header template is invalid"))
	}
	return template, nil
}

func validateCustomHeaders(config customChannelConfig, secret string) error {
	if config.AuthHeader != "" && (!validCustomHeaderName(config.AuthHeader) || forbiddenCustomHeaderName(config.AuthHeader)) {
		return common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("custom channel auth_header is invalid"))
	}
	if config.AuthTemplate != "" && !strings.Contains(config.AuthTemplate, "{{secret}}") {
		return common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("custom channel auth_template must read credentials from {{secret}}"))
	}
	if config.AuthTemplate == "" && config.AuthHeader != "" {
		return common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("custom channel auth_header requires auth_template"))
	}
	if strings.Contains(config.AuthTemplate, "{{secret}}") && secret == "" {
		return common.NewDeliveryError("authentication", "", false, false, 0, 0, errors.New("custom channel Secret is required by auth_template"))
	}
	for name, value := range config.Headers {
		if !validCustomHeaderName(name) || forbiddenCustomHeaderName(name) {
			return common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("custom channel contains a forbidden header name"))
		}
		if strings.ContainsAny(value, "\r\n") {
			return common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("custom channel header value contains a line break"))
		}
		if customHeaderContainsCredential(name) && !strings.Contains(value, "{{secret}}") {
			return common.NewDeliveryError("configuration", "", false, false, 0, 0, fmt.Errorf("sensitive header %q must use the {{secret}} credential placeholder", name))
		}
		if strings.Contains(value, "{{secret}}") && secret == "" {
			return common.NewDeliveryError("authentication", "", false, false, 0, 0, errors.New("custom channel Secret is required by a header template"))
		}
		authHeader := config.AuthHeader
		if authHeader == "" {
			authHeader = "Authorization"
		}
		if config.AuthTemplate != "" && strings.EqualFold(name, authHeader) {
			return common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("custom channel auth header must not also appear in headers"))
		}
	}
	return nil
}

func customHeaderContainsCredential(name string) bool {
	lower := strings.ToLower(name)
	for _, keyword := range []string{"authorization", "api-key", "api_key", "apikey", "token", "secret", "credential", "password"} {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

func forbiddenCustomHeaderName(name string) bool {
	for _, forbidden := range []string{"host", "content-length", "transfer-encoding", "connection", "proxy-authorization"} {
		if strings.EqualFold(name, forbidden) {
			return true
		}
	}
	return false
}

func validCustomHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", r)) {
			return false
		}
	}
	return true
}

func normalizeCustomSuccessCodes(codes []int) (map[int]struct{}, error) {
	if len(codes) == 0 || len(codes) > 20 {
		return nil, common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("custom channel success_status_codes must contain between 1 and 20 codes"))
	}
	result := make(map[int]struct{}, len(codes))
	for _, code := range codes {
		if code < 200 || code >= 300 {
			return nil, common.NewDeliveryError("configuration", "", false, false, 0, 0, errors.New("custom channel success status codes must be between 200 and 299"))
		}
		result[code] = struct{}{}
	}
	return result, nil
}

func validateCustomWebhookDNS(ctx context.Context, endpoint *url.URL, allowPrivate bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, endpoint.Hostname())
	if err != nil || len(ips) == 0 {
		return common.NewDeliveryError("dns", "", true, false, 0, 0, errors.New("custom channel host DNS lookup failed"))
	}
	for _, resolved := range ips {
		if unsafeCustomWebhookIP(resolved.IP, allowPrivate) {
			return common.NewDeliveryError("ssrf_blocked", "", false, false, 0, 0, errors.New("custom channel host resolves to a disallowed network address"))
		}
	}
	return nil
}

func customPinnedDialContext(expectedHost, port string, allowPrivate bool) func(context.Context, string, string) (net.Conn, error) {
	if port == "" {
		port = "443"
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, actualPort, err := net.SplitHostPort(address)
		if err != nil || !strings.EqualFold(strings.TrimSuffix(host, "."), strings.TrimSuffix(expectedHost, ".")) || actualPort != port {
			return nil, &customWebhookPreSendError{category: "configuration", message: "custom channel dial target changed unexpectedly"}
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, expectedHost)
		if err != nil || len(ips) == 0 {
			return nil, &customWebhookPreSendError{category: "dns", retryable: true, message: "custom channel DNS lookup failed"}
		}
		for _, resolved := range ips {
			if unsafeCustomWebhookIP(resolved.IP, allowPrivate) {
				return nil, &customWebhookPreSendError{category: "ssrf_blocked", message: "custom channel DNS answer is disallowed"}
			}
		}
		var lastErr error
		for _, resolved := range ips {
			connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), port))
			if dialErr == nil {
				return connection, nil
			}
			lastErr = dialErr
		}
		if lastErr == nil {
			lastErr = errors.New("custom channel connection failed")
		}
		return nil, lastErr
	}
}

func unsafeCustomWebhookIP(ip net.IP, allowPrivate bool) bool {
	if ip == nil {
		return true
	}
	ip = ip.To16()
	if ip == nil || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
		// Cloud metadata and link-local ranges remain blocked even when private
		// network access is explicitly enabled.
		if ip.Equal(net.IPv4(169, 254, 169, 254)) || ip.Equal(net.IPv4(169, 254, 170, 2)) {
			return true
		}
		privateV4 := ip.IsPrivate() || ip.IsLoopback()
		if allowPrivate {
			if privateV4 {
				return false
			}
			return !ip.IsGlobalUnicast() || isCustomIPInPrefixes(ip, []string{
				"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24",
				"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
			})
		}
		return privateV4 || !ip.IsGlobalUnicast() || isCustomIPInPrefixes(ip, []string{
			"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24",
			"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
		})
	}
	if ip.Equal(net.ParseIP("fd00:ec2::254")) {
		return true
	}
	privateV6 := isCustomIPInPrefixes(ip, []string{"fc00::/7", "::1/128"})
	if allowPrivate && privateV6 {
		return false
	}
	return privateV6 || !ip.IsGlobalUnicast() || isCustomIPInPrefixes(ip, []string{
		"2001:db8::/32", "2001::/23", "64:ff9b:1::/48",
	})
}

func isCustomIPInPrefixes(ip net.IP, cidrs []string) bool {
	for _, cidr := range cidrs {
		_, subnet, err := net.ParseCIDR(cidr)
		if err == nil && subnet.Contains(ip) {
			return true
		}
	}
	return false
}

type customWebhookPreSendError struct {
	category  string
	retryable bool
	message   string
}

func (err *customWebhookPreSendError) Error() string { return err.message }

func classifyCustomWebhookTransportError(err error, attempted bool) error {
	if err == nil {
		return nil
	}
	var preSendErr *customWebhookPreSendError
	if errors.As(err, &preSendErr) {
		return common.NewDeliveryError(preSendErr.category, "", preSendErr.retryable, false, 0, 0, errors.New(preSendErr.message))
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return common.NewDeliveryError("dns", "", true, false, 0, 0, errors.New("custom channel host DNS lookup failed"))
	}
	var certErr *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameErr x509.HostnameError
	if errors.As(err, &certErr) || errors.As(err, &unknownAuthority) || errors.As(err, &hostnameErr) {
		return common.NewDeliveryError("tls", "", false, false, 0, 0, errors.New("custom channel TLS certificate validation failed"))
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return common.NewDeliveryError("network", "", true, false, 0, 0, errors.New("custom channel connection failed before sending"))
	}
	if errors.Is(err, context.Canceled) {
		return common.NewDeliveryErrorWithAttempted("cancelled", "", false, attempted, attempted, 0, 0, errors.New("custom channel request was cancelled; delivery may be unknown"))
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return common.NewDeliveryErrorWithAttempted("timeout", "", !attempted, attempted, attempted, 0, 0, errors.New("custom channel request timed out; delivery may be unknown"))
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return common.NewDeliveryErrorWithAttempted("timeout", "", !attempted, attempted, attempted, 0, 0, errors.New("custom channel request timed out; delivery may be unknown"))
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if attempted {
			return common.NewDeliveryErrorWithAttempted("delivery_unknown", "", false, true, true, 0, 0, errors.New("custom channel request failed after it may have been sent"))
		}
		return common.NewDeliveryError("network", "", true, false, 0, 0, errors.New("custom channel request failed before it was sent"))
	}
	if attempted {
		return common.NewDeliveryErrorWithAttempted("delivery_unknown", "", false, true, true, 0, 0, errors.New("custom channel request result is unknown"))
	}
	return common.NewDeliveryError("network", "", true, false, 0, 0, errors.New("custom channel request failed before it was sent"))
}

func customProviderCode(body []byte) string {
	var value map[string]json.RawMessage
	if json.Unmarshal(body, &value) != nil {
		return ""
	}
	for _, key := range []string{"provider_code", "error_code", "errcode", "code"} {
		if raw, ok := value[key]; ok {
			var text string
			if json.Unmarshal(raw, &text) == nil {
				if len(text) <= 128 {
					return text
				}
			} else if len(raw) <= 128 {
				return string(raw)
			}
		}
	}
	return ""
}
