package engine

import (
	"errors"

	"app/internal/apperr"
)

// Invalid reports a request the node cannot run as given.
func Invalid(message string) error {
	return apperr.Validation(message)
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
	if err == nil {
		return ""
	}
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	var pending *PendingError
	if errors.As(err, &pending) {
		return apperr.CodeUnavailable
	}
	return apperr.CodeInternal
}
