package collections

import (
	"errors"
	"fmt"
)

// Sentinel domain errors returned by Manager. Callers MUST identify them
// with errors.Is (never by comparing err.Error()), so HTTP status mapping
// does not depend on fragile string matching.
var (
	ErrCollectionNotFound        = errors.New("collection not found")
	ErrDeletionProtectionEnabled = errors.New("deletion protection is enabled")
	ErrDocumentNotFound          = errors.New("document not found")
	ErrStreamAlreadyExists       = errors.New("stream is already enabled")
	ErrTTLAlreadyExists          = errors.New("ttl is already enabled")
	ErrProtectionAlreadyExists   = errors.New("deletion protection is already enabled")
	ErrSinkNotFound              = errors.New("sink not found")
	ErrValidation                = errors.New("validation failed")
)

// ValidationError wraps a dynamic client-validation message while remaining
// identifiable via errors.Is(err, ErrValidation). Use NewValidationError so
// callers can map validation errors to HTTP 400 without comparing strings.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string        { return e.Message }
func (e *ValidationError) Is(target error) bool { return target == ErrValidation }

// NewValidationError returns a validation error with a formatted message.
func NewValidationError(format string, args ...any) error {
	return &ValidationError{Message: fmt.Sprintf(format, args...)}
}
