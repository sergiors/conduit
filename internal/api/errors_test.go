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
		{"collection not found", collections.ErrCollectionNotFound, http.StatusNotFound, "collection_not_found", "collection not found"},
		{"document not found", collections.ErrDocumentNotFound, http.StatusNotFound, "document_not_found", "document not found"},
		{"sink not found", collections.ErrSinkNotFound, http.StatusNotFound, "sink_not_found", "sink not found"},
		{"sink already exists is 409", collections.ErrSinkAlreadyExists, http.StatusConflict, "sink_already_exists", "an equivalent sink already exists"},
		{"sink identity immutable is 400", collections.ErrSinkIdentityImmutable, http.StatusBadRequest, "sink_identity_immutable", "sink type and spec are immutable; create a new sink instead"},
		{"deletion protection enabled", collections.ErrDeletionProtectionEnabled, http.StatusForbidden, "deletion_protection_enabled", "deletion protection is enabled"},
		{"wrapped collection not found", fmt.Errorf("get: %w", collections.ErrCollectionNotFound), http.StatusNotFound, "collection_not_found", "get: collection not found"},
		{"validation error is 400", collections.NewValidationError("bad input"), http.StatusBadRequest, "validation_error", "bad input"},
		{"wrapped validation error is 400", fmt.Errorf("ctx: %w", collections.NewValidationError("bad")), http.StatusBadRequest, "validation_error", "ctx: bad"},
		{"stream already exists is 409", collections.ErrStreamAlreadyExists, http.StatusConflict, "stream_already_exists", "stream is already enabled"},
		{"ttl already exists is 409", collections.ErrTTLAlreadyExists, http.StatusConflict, "ttl_already_exists", "ttl is already enabled"},
		{"protection already exists is 409", collections.ErrProtectionAlreadyExists, http.StatusConflict, "protection_already_exists", "deletion protection is already enabled"},
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
