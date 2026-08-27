package api

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/stretchr/testify/assert"
)

func TestResponseFor(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		status  int
		code    string
		message string
	}{
		{"nil is empty", nil, 0, "", ""},
		{"bad request", newBadRequest("missing name"), http.StatusBadRequest, "invalid_request", "missing name"},
		{"collection not found", collections.ErrCollectionNotFound, http.StatusNotFound, "collection_not_found", "Collection not found."},
		{"document not found", collections.ErrDocumentNotFound, http.StatusNotFound, "document_not_found", "Document not found."},
		{"sink not found", collections.ErrSinkNotFound, http.StatusNotFound, "sink_not_found", "Sink not found."},
		{"deletion protection enabled", collections.ErrDeletionProtectionEnabled, http.StatusForbidden, "deletion_protection_enabled", "Deletion protection is enabled. Disable it before deleting the collection."},
		{"wrapped collection not found", fmt.Errorf("get: %w", collections.ErrCollectionNotFound), http.StatusNotFound, "collection_not_found", "Collection not found."},
		{"validation error is 400", collections.NewValidationError("bad input"), http.StatusBadRequest, "validation_error", "bad input"},
		{"wrapped validation error is 400", fmt.Errorf("ctx: %w", collections.NewValidationError("bad")), http.StatusBadRequest, "validation_error", "ctx: bad"},
		{"ttl immutable is 409", collections.ErrTTLAttributeImmutable, http.StatusConflict, "ttl_attribute_immutable", "TTL attribute is immutable"},
		{"old_image immutable is 409", collections.ErrOldImageImmutable, http.StatusConflict, "old_image_immutable", "old_image is immutable once the stream is enabled"},
		{"unknown error is 500", errors.New("boom"), http.StatusInternalServerError, "internal_error", "boom"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, code, message := responseFor(tc.err)
			assert.Equal(t, tc.status, status, "status should match")
			assert.Equal(t, tc.code, code, "code should match")
			assert.Equal(t, tc.message, message, "message should match")
		})
	}
}
