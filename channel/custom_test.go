package channel

import (
	"net"
	"testing"

	"message-pusher/model"
)

func TestParseCustomWebhookURLRejectsCredentialsAndNonHTTPS(t *testing.T) {
	for _, raw := range []string{
		"http://example.com/hook",
		"https://user:password@example.com/hook",
		"https://example.com/hook?api_key=secret",
	} {
		if _, err := parseCustomWebhookURL(raw); err == nil {
			t.Errorf("parseCustomWebhookURL(%q) should fail", raw)
		}
	}
}

func TestRenderCustomBodyUsesMessageFields(t *testing.T) {
	message := &model.Message{Title: "Title", Description: "Description", Content: "Body", URL: "https://example.com", To: "person@example.com"}
	body, err := renderCustomBody(`{"title":"$title","description":"$description","content":"$content","url":"$url","to":"$to"}`, message)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"title":"Title","description":"Description","content":"Body","url":"https://example.com","to":"person@example.com"}` {
		t.Fatalf("unexpected rendered body: %s", body)
	}
}

func TestValidateCustomHeadersRequiresSecretForSensitiveHeaders(t *testing.T) {
	config := customChannelConfig{Headers: map[string]string{"X-Api-Key": "literal-key"}}
	if err := validateCustomHeaders(config, "secret"); err == nil {
		t.Fatal("sensitive header must not store a credential in Other")
	}
	config.Headers["X-Api-Key"] = "{{secret}}"
	if err := validateCustomHeaders(config, "secret"); err != nil {
		t.Fatal(err)
	}
}

func TestCustomWebhookIPPolicyRequiresExplicitPrivateAuthorization(t *testing.T) {
	tests := []struct {
		ip           string
		allowPrivate bool
		blocked      bool
	}{
		{ip: "127.0.0.1", blocked: true},
		{ip: "10.0.0.4", blocked: true},
		{ip: "10.0.0.4", allowPrivate: true},
		{ip: "169.254.169.254", allowPrivate: true, blocked: true},
		{ip: "224.0.0.1", allowPrivate: true, blocked: true},
		{ip: "8.8.8.8"},
	}
	for _, test := range tests {
		t.Run(test.ip, func(t *testing.T) {
			blocked := unsafeCustomWebhookIP(net.ParseIP(test.ip), test.allowPrivate)
			if blocked != test.blocked {
				t.Fatalf("unsafeCustomWebhookIP(%s, %t) = %t, want %t", test.ip, test.allowPrivate, blocked, test.blocked)
			}
		})
	}
}

func TestNormalizeCustomSuccessCodesOnlyAllows2xx(t *testing.T) {
	if _, err := normalizeCustomSuccessCodes([]int{200, 202, 204}); err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeCustomSuccessCodes([]int{302}); err == nil {
		t.Fatal("redirect status must never be configured as success")
	}
}
