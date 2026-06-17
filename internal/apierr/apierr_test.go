package apierr

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
		message string
	}{
		{"nil is empty", nil, 0, ""},
		{"collection not found", collections.ErrCollectionNotFound, http.StatusNotFound, "Collection not found"},
		{"deletion protection enabled", collections.ErrDeletionProtectionEnabled, http.StatusForbidden, "Deletion protection is enabled. Disable it before deleting the collection."},
		{"document not found", collections.ErrDocumentNotFound, http.StatusNotFound, "Document not found"},
		{"wrapped collection not found", fmt.Errorf("get: %w", collections.ErrCollectionNotFound), http.StatusNotFound, "Collection not found"},
		{"wrapped document not found", fmt.Errorf("update: %w", collections.ErrDocumentNotFound), http.StatusNotFound, "Document not found"},
		{"validation error is 400", collections.NewValidationError("bad input"), http.StatusBadRequest, "bad input"},
		{"wrapped validation error is 400", fmt.Errorf("ctx: %w", collections.NewValidationError("bad")), http.StatusBadRequest, "ctx: bad"},
		{"ttl immutable is 409", collections.ErrTTLAttributeImmutable, http.StatusConflict, "TTL attribute is immutable"},
		{"old_image immutable is 409", collections.ErrOldImageImmutable, http.StatusConflict, "old_image is immutable once the stream is enabled"},
		{"unknown error is 500", errors.New("boom"), http.StatusInternalServerError, "boom"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, message := ResponseFor(tc.err)
			assert.Equal(t, tc.status, status, "status should match")
			assert.Equal(t, tc.message, message, "message should match")
		})
	}
}
