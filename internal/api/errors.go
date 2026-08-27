package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sergiors/conduit/internal/collections"
)

// ErrorInfo describes a single API error.
type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ErrorResponse is the canonical JSON shape for every API error.
type ErrorResponse struct {
	Error ErrorInfo `json:"error"`
}

// badRequestError marks malformed HTTP requests (invalid JSON, missing path
// parameters, etc.). It is handled by writeError and never leaks into business
// packages.
type badRequestError struct {
	message string
}

func (e *badRequestError) Error() string { return e.message }

// newBadRequest creates a bad request error with a formatted message.
func newBadRequest(format string, args ...any) error {
	return &badRequestError{message: fmt.Sprintf(format, args...)}
}

// writeError translates any error into the canonical error response and sends
// it. It is the single place where domain errors become HTTP responses.
func writeError(c *gin.Context, err error) {
	status, code, message := responseFor(err)
	if status == 0 {
		return
	}

	c.JSON(status, ErrorResponse{
		Error: ErrorInfo{
			Code:    code,
			Message: message,
		},
	})
}

// responseFor maps an error to an HTTP status, error code, and message.
// A nil error returns (0, "", ""). Unknown errors become 500.
func responseFor(err error) (int, string, string) {
	var badRequest *badRequestError

	switch {
	case err == nil:
		return 0, "", ""
	case errors.As(err, &badRequest):
		return http.StatusBadRequest, "invalid_request", badRequest.Error()
	case errors.Is(err, collections.ErrCollectionNotFound):
		return http.StatusNotFound, "collection_not_found", "Collection not found."
	case errors.Is(err, collections.ErrDocumentNotFound):
		return http.StatusNotFound, "document_not_found", "Document not found."
	case errors.Is(err, collections.ErrSinkNotFound):
		return http.StatusNotFound, "sink_not_found", "Sink not found."
	case errors.Is(err, collections.ErrDeletionProtectionEnabled):
		return http.StatusForbidden, "deletion_protection_enabled", "Deletion protection is enabled. Disable it before deleting the collection."
	case errors.Is(err, collections.ErrValidation):
		return http.StatusBadRequest, "validation_error", err.Error()
	case errors.Is(err, collections.ErrTTLAttributeImmutable):
		return http.StatusConflict, "ttl_attribute_immutable", err.Error()
	case errors.Is(err, collections.ErrOldImageImmutable):
		return http.StatusConflict, "old_image_immutable", err.Error()
	default:
		return http.StatusInternalServerError, "internal_error", err.Error()
	}
}
