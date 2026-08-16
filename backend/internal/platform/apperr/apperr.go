// Package apperr defines the error vocabulary shared by the services.
// Domain code returns these errors and the transport layer maps them to
// protocol specific responses, so business rules stay free of HTTP details.
package apperr

import (
	"errors"
	"fmt"
)

// Kind classifies an error so transports can translate it consistently.
type Kind string

const (
	// KindInvalid means the caller sent data that fails validation.
	KindInvalid Kind = "invalid"
	// KindNotFound means the requested resource does not exist.
	KindNotFound Kind = "not_found"
	// KindConflict means the request clashes with the current state,
	// such as a duplicate product code or insufficient stock balance.
	KindConflict Kind = "conflict"
	// KindUnauthorized means the caller is not allowed to perform the action.
	KindUnauthorized Kind = "unauthorized"
	// KindUnavailable means a dependency failed and the call may be retried.
	KindUnavailable Kind = "unavailable"
	// KindInternal means an unexpected failure the caller cannot act on.
	KindInternal Kind = "internal"
)

// Error carries a kind, a machine readable code, a message safe to show to the
// caller, optional field level details and an optional wrapped cause.
type Error struct {
	Kind    Kind
	Code    string
	Message string
	Details map[string]string
	cause   error
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap exposes the wrapped cause to errors.Is and errors.As.
func (e *Error) Unwrap() error { return e.cause }

// Is treats two errors as equal when they share kind and code. WithDetails and
// WithCause return copies, so comparing against a package level sentinel such
// as ErrProductNotFound keeps working after they are used.
func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && e.Kind == other.Kind && e.Code == other.Code
}

// WithDetails returns a copy of the error carrying field level details.
func (e *Error) WithDetails(details map[string]string) *Error {
	clone := *e
	clone.Details = details
	return &clone
}

// WithCause returns a copy of the error wrapping the given cause.
// The cause is meant for logs only and is never exposed to callers.
func (e *Error) WithCause(cause error) *Error {
	clone := *e
	clone.cause = cause
	return &clone
}

// New builds an error of the given kind.
func New(kind Kind, code, message string) *Error {
	return &Error{Kind: kind, Code: code, Message: message}
}

// Invalid builds a validation error.
func Invalid(code, message string) *Error { return New(KindInvalid, code, message) }

// NotFound builds a missing resource error.
func NotFound(code, message string) *Error { return New(KindNotFound, code, message) }

// Conflict builds a state conflict error.
func Conflict(code, message string) *Error { return New(KindConflict, code, message) }

// Unauthorized builds an access denied error.
func Unauthorized(code, message string) *Error { return New(KindUnauthorized, code, message) }

// Unavailable builds a dependency failure error.
func Unavailable(code, message string) *Error { return New(KindUnavailable, code, message) }

// Internal builds an unexpected failure error.
func Internal(code, message string) *Error { return New(KindInternal, code, message) }

// From returns err as an *Error. Errors that do not carry a kind are reported
// as internal failures with a generic message, so implementation details such
// as database errors never reach the caller.
func From(err error) *Error {
	if err == nil {
		return nil
	}
	var appErr *Error
	if errors.As(err, &appErr) {
		return appErr
	}
	return Internal("internal_error", "An unexpected error occurred.").WithCause(err)
}

// KindOf reports the kind of err, defaulting to KindInternal.
func KindOf(err error) Kind {
	if err == nil {
		return ""
	}
	var appErr *Error
	if errors.As(err, &appErr) {
		return appErr.Kind
	}
	return KindInternal
}

// IsKind reports whether err has the given kind.
func IsKind(err error, kind Kind) bool { return KindOf(err) == kind }
