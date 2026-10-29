// Package apperr defines the application error model shared by every layer.
//
// Domain and service code return *Error (directly or wrapped with %w) whenever the
// failure is meaningful to the user. Transport layers convert any error with From,
// which guarantees that unexpected errors never leak internal details.
package apperr

import (
	"context"
	"errors"
	"fmt"
)

// Code is a stable, machine-readable error category shared with the frontend.
type Code string

const (
	CodeBadRequest       Code = "ERR_BAD_REQUEST"
	CodeValidation       Code = "ERR_VALIDATION"
	CodeUnauthorized     Code = "ERR_UNAUTHORIZED"
	CodeNotFound         Code = "ERR_NOT_FOUND"
	CodeConflict         Code = "ERR_CONFLICT"
	CodeUnknownAction    Code = "ERR_UNKNOWN_ACTION"
	CodeConcurrencyLimit Code = "ERR_CONCURRENCY_LIMIT"
	CodeTimeout          Code = "ERR_TIMEOUT"
	CodeCancelled        Code = "ERR_CANCELLED"
	CodeUnavailable      Code = "ERR_UNAVAILABLE"
	CodeInternal         Code = "ERR_INTERNAL"
)

// Codes lists every code; the contract generator exports it to TypeScript.
func Codes() []Code {
	return []Code{
		CodeBadRequest, CodeValidation, CodeUnauthorized, CodeNotFound, CodeConflict,
		CodeUnknownAction, CodeConcurrencyLimit, CodeTimeout, CodeCancelled,
		CodeUnavailable, CodeInternal,
	}
}

// Error is a user-safe error with a stable code. Message must never contain
// secrets, file contents or SQL; put diagnostics in the wrapped cause instead.
type Error struct {
	Code    Code
	Message string
	Details map[string]any
	cause   error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.cause }

// WithDetails returns a copy carrying structured, user-safe details.
func (e *Error) WithDetails(details map[string]any) *Error {
	c := *e
	c.Details = details
	return &c
}

// New creates an error with the given code and message.
func New(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Wrap creates an error with the given code and message that keeps cause for logging.
func Wrap(code Code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, cause: cause}
}

// Validation is shorthand for a CodeValidation error.
func Validation(message string) *Error { return New(CodeValidation, message) }

// NotFound is shorthand for a CodeNotFound error.
func NotFound(message string) *Error { return New(CodeNotFound, message) }

// Unavailable is shorthand for a CodeUnavailable error.
func Unavailable(message string) *Error { return New(CodeUnavailable, message) }

// Internal wraps an unexpected failure; the cause is logged, never shown.
func Internal(cause error) *Error { return Wrap(CodeInternal, "internal error", cause) }

// From converts any error into a user-safe *Error.
func From(err error) *Error {
	if err == nil {
		return nil
	}
	var ae *Error
	if errors.As(err, &ae) {
		return ae
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return Wrap(CodeTimeout, "operation timed out", err)
	case errors.Is(err, context.Canceled):
		return Wrap(CodeCancelled, "operation cancelled", err)
	}
	return Internal(err)
}

// IsCode reports whether err converts to the given code.
func IsCode(err error, code Code) bool {
	return err != nil && From(err).Code == code
}
