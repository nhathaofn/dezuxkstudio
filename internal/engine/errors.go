package engine

import (
	"errors"

	"app/internal/apperr"
)

// Error is a node failure that keeps the result code the UI shows.
type Error struct {
	Code    apperr.Code
	Message string
}

func (e *Error) Error() string { return e.Message }

// Invalid reports a request the node cannot run as given.
func Invalid(message string) error {
	return &Error{Code: apperr.CodeValidation, Message: message}
}

// PendingError ends a node whose provider accepted the work but has not finished it yet.
type PendingError struct {
	JobIDs  []string
	Message string
}

func (e *PendingError) Error() string { return e.Message }

// Pending reports work still running on the provider's side.
func Pending(message string, jobIDs []string) error {
	return &PendingError{JobIDs: jobIDs, Message: message}
}

// CodeOf extracts the error code or returns CodeInternal.
func CodeOf(err error) apperr.Code {
	var coded *Error
	if errors.As(err, &coded) {
		return coded.Code
	}
	var pending *PendingError
	if errors.As(err, &pending) {
		return apperr.CodeUnavailable
	}
	return apperr.CodeInternal
}
