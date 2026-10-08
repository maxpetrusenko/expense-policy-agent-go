// Package failure classifies errors so the pipeline knows whether to retry,
// dead-letter, or surface them. Classification is by type and HTTP status,
// never by matching error strings.
package failure

import (
	"context"
	"errors"
	"fmt"
	"net"
)

// Class is the retry decision attached to an error.
type Class string

const (
	// Retryable failures are transient: timeouts, 429, 5xx, connection resets.
	Retryable Class = "retryable"
	// Schema means the model output violated the extraction schema.
	Schema Class = "schema_violation"
	// InvalidDoc means the input cannot produce an expense (empty, unparseable total).
	InvalidDoc Class = "invalid_document"
	// Permanent covers other non-retryable failures (4xx auth, bad request).
	Permanent Class = "permanent"
)

// Error carries a Class and the stage that produced it.
type Error struct {
	Class Class
	Stage string
	Err   error
}

func (e *Error) Error() string { return fmt.Sprintf("%s [%s]: %v", e.Stage, e.Class, e.Err) }
func (e *Error) Unwrap() error { return e.Err }

// New wraps err with an explicit class.
func New(class Class, stage string, err error) *Error {
	return &Error{Class: class, Stage: stage, Err: err}
}

// StatusError is an HTTP status returned by a provider.
type StatusError struct {
	Status int
	Err    error
}

func (e *StatusError) Error() string { return fmt.Sprintf("http %d: %v", e.Status, e.Err) }
func (e *StatusError) Unwrap() error { return e.Err }

// Classify returns the Class for any error. Unknown errors are Permanent so
// that bugs do not turn into infinite retry loops.
func Classify(err error) Class {
	if err == nil {
		return ""
	}
	var fe *Error
	if errors.As(err, &fe) {
		return fe.Class
	}
	var se *StatusError
	if errors.As(err, &se) {
		return ClassifyStatus(se.Status)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return Retryable
	}
	if errors.Is(err, context.Canceled) {
		return Permanent
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return Retryable
	}
	return Permanent
}

// ClassifyStatus maps an HTTP status code to a Class.
func ClassifyStatus(status int) Class {
	switch {
	case status == 408 || status == 409 || status == 425 || status == 429:
		return Retryable
	case status >= 500:
		return Retryable
	default:
		return Permanent
	}
}

// IsRetryable reports whether err should be retried.
func IsRetryable(err error) bool { return Classify(err) == Retryable }
