// Package apierr maps domain errors from internal/collections to HTTP
// status codes and messages.
//
// It is the single place that knows how a domain sentinel error translates to
// an HTTP response, so handlers never repeat this mapping and never compare
// error strings. It depends only on the domain sentinels and net/http status
// codes (no web framework), which keeps it unit-testable and avoids pulling
// gin into the internal layer.
package apierr

import (
	"errors"
	"net/http"

	"github.com/sergiors/conduit/internal/collections"
)

// ResponseFor maps a domain error to an HTTP status code and message.
//
// Wrapped sentinels are matched with errors.Is, so callers may wrap errors
// with %w without losing the mapping. Unknown errors become 500.
// A nil error returns (0, "").
func ResponseFor(err error) (status int, message string) {
	switch {
	case err == nil:
		return 0, ""
	case errors.Is(err, collections.ErrCollectionNotFound):
		return http.StatusNotFound, "Collection not found"
	case errors.Is(err, collections.ErrDeletionProtectionEnabled):
		return http.StatusForbidden, "Deletion protection is enabled. Disable it before deleting the collection."
	case errors.Is(err, collections.ErrDocumentNotFound):
		return http.StatusNotFound, "Document not found"
	case errors.Is(err, collections.ErrValidation):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, collections.ErrTTLAttributeImmutable):
		return http.StatusConflict, err.Error()
	case errors.Is(err, collections.ErrOldImageImmutable):
		return http.StatusConflict, err.Error()
	default:
		return http.StatusInternalServerError, err.Error()
	}
}
