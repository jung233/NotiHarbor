package common

// NOT EXECUTED — LOCAL TESTING PROHIBITED.
import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestCredentialEncryptionAuthenticationAndKeyRotation(t *testing.T) {
	t.Setenv("ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 32))))
	one, err := EncryptCredential("sample-secret")
	if err != nil {
		t.Fatal(err)
	}
	two, err := EncryptCredential("sample-secret")
	if err != nil || one == two || strings.Contains(one, "sample-secret") {
		t.Fatal("nonce must randomize ciphertext")
	}
	plain, err := DecryptCredential(one)
	if err != nil || plain != "sample-secret" {
		t.Fatal("round trip failed")
	}
	reencoded, err := EncryptCredential(one)
	if err != nil || reencoded != one {
		t.Fatal("existing encrypted value must not be encrypted twice")
	}
	t.Setenv("ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("b", 32))))
	if _, err := DecryptCredential(one); err == nil {
		t.Fatal("wrong key accepted")
	}
	t.Setenv("ENCRYPTION_KEY", "weak")
	if err := ValidateEncryptionKey(); err == nil {
		t.Fatal("weak key accepted")
	}
}

func TestLegacyCredentialReadAndLogRedaction(t *testing.T) {
	plain, err := DecryptCredential("legacy-value")
	if err != nil || plain != "legacy-value" {
		t.Fatal("legacy upgrade compatibility lost")
	}
	t.Setenv("SESSION_SECRET", "environment-secret")
	redacted := RedactLog("session_secret=environment-secret token=provider-secret password=smtp-secret")
	for _, secret := range []string{"environment-secret", "provider-secret", "smtp-secret"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("secret leaked: %s", secret)
		}
	}
}
