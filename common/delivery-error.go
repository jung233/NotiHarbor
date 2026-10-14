package common

import (
	"errors"
	"fmt"
	"time"
)

// DeliveryError carries the retry and provider metadata needed by the delivery
// scheduler without requiring it to inspect a channel-specific error string.
type DeliveryError struct {
	Category           string
	RetryableValue     bool
	UnknownValue       bool
	ProviderCodeValue  string
	RetryAfterValue    time.Duration
	HTTPStatusValue    int
	SendAttemptedValue bool
	Err                error
}

func (e *DeliveryError) Error() string {
	if e == nil {
		return "delivery failed"
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	if e.Category != "" {
		return e.Category
	}
	return "delivery failed"
}

func (e *DeliveryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *DeliveryError) DeliveryCategory() string {
	if e == nil || e.Category == "" {
		return "unknown"
	}
	return e.Category
}

func (e *DeliveryError) Retryable() bool {
	return e != nil && e.RetryableValue
}

func (e *DeliveryError) DeliveryUnknown() bool {
	return e != nil && e.UnknownValue
}

func (e *DeliveryError) ProviderCode() string {
	if e == nil {
		return ""
	}
	return e.ProviderCodeValue
}

func (e *DeliveryError) RetryAfter() time.Duration {
	if e == nil {
		return 0
	}
	return e.RetryAfterValue
}

func (e *DeliveryError) HTTPStatus() int {
	if e == nil {
		return 0
	}
	return e.HTTPStatusValue
}

// SendAttempted reports whether an adapter reached the provider's delivery
// endpoint. Preflight work such as credential refresh and local rate limiting
// leaves this false.
func (e *DeliveryError) SendAttempted() bool {
	return e != nil && e.SendAttemptedValue
}

// NewDeliveryError constructs a typed delivery error. The provider response
// body should be sanitized before it is placed in Err or written to logs.
func NewDeliveryError(category, providerCode string, retryable, unknown bool, retryAfter time.Duration, httpStatus int, err error) *DeliveryError {
	return &DeliveryError{
		Category:          category,
		RetryableValue:    retryable,
		UnknownValue:      unknown,
		ProviderCodeValue: providerCode,
		RetryAfterValue:   retryAfter,
		HTTPStatusValue:   httpStatus,
		Err:               err,
	}
}

// NewDeliveryErrorWithAttempted constructs a DeliveryError with an explicit
// provider-send attempt marker. The legacy constructor keeps its original
// preflight default of false.
func NewDeliveryErrorWithAttempted(category, providerCode string, retryable, unknown, attempted bool, retryAfter time.Duration, httpStatus int, err error) *DeliveryError {
	deliveryErr := NewDeliveryError(category, providerCode, retryable, unknown, retryAfter, httpStatus, err)
	deliveryErr.SendAttemptedValue = attempted
	return deliveryErr
}

// MarkDeliveryAttempted copies a typed delivery error with the specified send
// attempt state while preserving its provider and retry metadata.
func MarkDeliveryAttempted(err error, attempted bool) error {
	deliveryErr, ok := AsDeliveryError(err)
	if !ok {
		return err
	}
	copyErr := *deliveryErr
	copyErr.SendAttemptedValue = attempted
	return &copyErr
}

// AsDeliveryError finds a channel error through wrapped errors.
func AsDeliveryError(err error) (*DeliveryError, bool) {
	var deliveryErr *DeliveryError
	if !errors.As(err, &deliveryErr) || deliveryErr == nil {
		return nil, false
	}
	return deliveryErr, true
}

// ErrorWithFallback provides a stable message when a provider returned no
// diagnostic text.
func ErrorWithFallback(err error, fallback string) error {
	if err != nil {
		return err
	}
	if fallback == "" {
		fallback = "delivery failed"
	}
	return fmt.Errorf("%s", fallback)
}
