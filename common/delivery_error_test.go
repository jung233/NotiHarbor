package common

import (
	"errors"
	"testing"
	"time"
)

func TestDeliveryErrorExposesQueueMetadata(t *testing.T) {
	cause := errors.New("provider throttled the request")
	err := NewDeliveryErrorWithAttempted("rate_limit", "22009", true, false, true, 3*time.Second, 429, cause)
	if err.DeliveryCategory() != "rate_limit" || !err.Retryable() || err.DeliveryUnknown() || !err.SendAttempted() {
		t.Fatalf("unexpected delivery metadata: %+v", err)
	}
	if err.ProviderCode() != "22009" || err.RetryAfter() != 3*time.Second || err.HTTPStatus() != 429 {
		t.Fatalf("provider details were not preserved: %+v", err)
	}
	if !errors.Is(err, cause) {
		t.Fatal("DeliveryError should unwrap its cause")
	}
}

func TestDeliveryErrorAttemptedDefaultsToPreflightAndCanBeCopied(t *testing.T) {
	preflight := NewDeliveryError("rate_limit_backend", "", true, false, time.Second, 0, errors.New("redis unavailable"))
	if preflight.SendAttempted() {
		t.Fatal("legacy constructor must default to not attempted")
	}
	marked := MarkDeliveryAttempted(preflight, true)
	if !marked.(*DeliveryError).SendAttempted() || preflight.SendAttempted() {
		t.Fatal("MarkDeliveryAttempted should copy without mutating the original")
	}
}
