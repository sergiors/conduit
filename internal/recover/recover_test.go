package recover

import (
	"errors"
	"strings"
	"testing"
)

func TestProtect(t *testing.T) {
	t.Run("recovers a panic and reports it", func(t *testing.T) {
		recovered, panicked := Protect("test:fn", func() {
			var m map[string]int
			_ = m["missing"] // nil map read is safe; force a write to panic
			m["key"] = 1
		})
		if !panicked {
			t.Fatal("expected panicked=true")
		}
		if recovered == nil {
			t.Fatal("expected a recovered value")
		}
	})

	t.Run("clean run returns nil, false", func(t *testing.T) {
		recovered, panicked := Protect("test:fn", func() {})
		if panicked {
			t.Fatalf("expected panicked=false, got %v", panicked)
		}
		if recovered != nil {
			t.Fatalf("expected recovered=nil, got %v", recovered)
		}
	})
}

func TestProtectErr(t *testing.T) {
	t.Run("recovers a panic and returns it as an error", func(t *testing.T) {
		err, panicked := ProtectErr("test:fn", func() error {
			var m map[string]int
			m["key"] = 1 // nil map write panics
			return nil
		})
		if !panicked {
			t.Fatal("expected panicked=true")
		}
		if err == nil {
			t.Fatal("expected a non-nil error")
		}
		if !strings.Contains(err.Error(), "panic in test:fn") {
			t.Fatalf("error should mention the name, got: %v", err)
		}
	})

	t.Run("normal error passes through unchanged", func(t *testing.T) {
		sentinel := errors.New("normal error")
		err, panicked := ProtectErr("test:fn", func() error {
			return sentinel
		})
		if panicked {
			t.Fatalf("expected panicked=false, got %v", panicked)
		}
		if !errors.Is(err, sentinel) {
			t.Fatalf("expected the original error, got %v", err)
		}
	})

	t.Run("clean run returns nil, false", func(t *testing.T) {
		err, panicked := ProtectErr("test:fn", func() error { return nil })
		if panicked {
			t.Fatalf("expected panicked=false, got %v", panicked)
		}
		if err != nil {
			t.Fatalf("expected err=nil, got %v", err)
		}
	})
}
