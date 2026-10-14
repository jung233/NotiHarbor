package common

import (
	"context"
	"errors"
	"net/mail"
	"strings"
	"testing"
)

func TestParseSMTPRecipientsSupportsMultipleSeparators(t *testing.T) {
	addresses, err := parseSMTPRecipients("One <one@example.com>; two@example.com|three@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(addresses) != 3 {
		t.Fatalf("parsed %d recipients, want 3", len(addresses))
	}
}

func TestValidSMTPHostRejectsPortsAndHeaderCharacters(t *testing.T) {
	for _, host := range []string{"smtp.example.com", "localhost", "192.0.2.1", "2001:db8::1"} {
		if !validSMTPHost(host) {
			t.Errorf("validSMTPHost(%q) should accept a host without a port", host)
		}
	}
	for _, host := range []string{"smtp.example.com:587", "smtp.example.com\r\nBcc:bad", "smtp example.com", ""} {
		if validSMTPHost(host) {
			t.Errorf("validSMTPHost(%q) should reject an invalid host", host)
		}
	}
}

func TestBuildSMTPMessageIncludesTextAndHTMLAlternative(t *testing.T) {
	from, err := mail.ParseAddress("sender@example.com")
	if err != nil {
		t.Fatal(err)
	}
	to, err := parseSMTPRecipients("reader@example.com")
	if err != nil {
		t.Fatal(err)
	}
	message, err := buildSMTPMessage("Subject", to, from, "plain text", "<b>html</b>")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"multipart/alternative", "text/plain; charset=UTF-8", "text/html; charset=UTF-8", "plain text", "<b>html</b>"} {
		if !strings.Contains(string(message), expected) {
			t.Errorf("SMTP message omitted %q", expected)
		}
	}
}

func TestSMTPErrorAttemptedDependsOnMessageDataStage(t *testing.T) {
	preflight := classifySMTPError(errors.New("connection refused"), false)
	if preflight.(*DeliveryError).SendAttempted() {
		t.Fatal("SMTP preflight connection failure must not count as a send")
	}
	uncertain := classifySMTPError(context.DeadlineExceeded, true)
	var deliveryErr *DeliveryError
	if !errors.As(uncertain, &deliveryErr) || !deliveryErr.SendAttempted() || !deliveryErr.DeliveryUnknown() {
		t.Fatalf("SMTP data-stage timeout should be marked as an unknown attempted delivery: %v", uncertain)
	}
}
