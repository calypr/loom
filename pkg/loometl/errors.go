package loometl

import (
	"errors"
	"fmt"
)

type APIError struct {
	Status    int
	Code      string
	Message   string
	Phase     string
	Output    string
	Details   map[string]any
	CanRetry  bool
	RequestID string
	Cause     error
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	message := e.Message
	if message == "" {
		message = "Loom request failed"
	}
	if e.Code != "" {
		return fmt.Sprintf("%s: %s", e.Code, message)
	}
	return message
}

func (e *APIError) Unwrap() error   { return e.Cause }
func (e *APIError) Retryable() bool { return e != nil && e.CanRetry }

type TransportError struct{ Cause error }

func (e *TransportError) Error() string {
	if e == nil || e.Cause == nil {
		return "Loom transport failed"
	}
	return "Loom transport failed: " + e.Cause.Error()
}
func (e *TransportError) Unwrap() error   { return e.Cause }
func (e *TransportError) Retryable() bool { return e != nil }

type WorkflowError struct {
	Stage                    string
	Project                  string
	Generation               string
	PreviousReleasePreserved bool
	Cause                    error
}

func (e *WorkflowError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("ETL stage %s failed; prior release remains active: %v", e.Stage, e.Cause)
}
func (e *WorkflowError) Unwrap() error { return e.Cause }

func IsRetryable(err error) bool {
	var retryable interface{ Retryable() bool }
	return errors.As(err, &retryable) && retryable.Retryable()
}
