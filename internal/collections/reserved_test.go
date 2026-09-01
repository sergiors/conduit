package collections

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsReservedCollectionName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		// Conduit internal configuration namespaces.
		{"config.collections", true},
		{"config.sinks", true},
		// MongoDB system namespaces.
		{"system.profile", true},
		{"system.indexes", true},
		{"system.views", true},
		{"system.js", true},
		// Normal names are not reserved.
		{"users", false},
		{"orders", false},
		{"config", false},
		{"config.collection", false},
		{"configs", false},
		{"system", false},
		{"systematic", false},
		{"system_events", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsReservedCollectionName(tt.name), "IsReservedCollectionName(%q)", tt.name)
		})
	}
}

func TestManagerCreateRejectsReservedNames(t *testing.T) {
	manager, _, ctx := newTestManager(t)

	for _, name := range []string{"config.collections", "config.sinks", "system.profile"} {
		t.Run(name, func(t *testing.T) {
			col := &Collection{CollectionName: name, StreamEnabled: true}
			err := manager.Create(ctx, col)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrValidation, "reserved name should map to ErrValidation")
			assert.Contains(t, err.Error(), "reserved")
		})
	}
}

func TestManagerCreateAcceptsNormalNames(t *testing.T) {
	manager, _, ctx := newTestManager(t)

	// Names that look like reserved ones but are not, plus a plain name.
	for _, name := range []string{"config", "config.collection", "system", "systematic", "users"} {
		t.Run(name, func(t *testing.T) {
			col := &Collection{CollectionName: name, StreamEnabled: true}
			err := manager.Create(ctx, col)
			require.NoError(t, err, "normal name should be accepted")

			// Cleanup so the test is repeatable.
			created, err := manager.Get(ctx, name)
			require.NoError(t, err)
			if created.DeletionProtection {
				require.NoError(t, manager.DisableDeletionProtection(ctx, name))
			}
			require.NoError(t, manager.Delete(ctx, name))
		})
	}
}

// TestIsReservedCollectionNameErrorIsValidation guards the API mapping contract:
// the reserved-name rejection must be identifiable via errors.Is(err, ErrValidation)
// so the API layer maps it to HTTP 400 without string matching.
func TestIsReservedCollectionNameErrorIsValidation(t *testing.T) {
	err := NewValidationError("collection name %q is reserved", "config.sinks")
	assert.True(t, errors.Is(err, ErrValidation))
}
