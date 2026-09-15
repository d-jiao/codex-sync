package main

import (
	"testing"

	"github.com/d-jiao/codex-sync/internal/config"
)

// TestResolveScope covers the non-interactive branches of resolveScope: explicit
// valid values pass through, and an unrecognized value is rejected. The empty
// (interactive prompt) case requires a TTY and is exercised manually.
func TestResolveScope(t *testing.T) {
	t.Run("valid values pass through unchanged", func(t *testing.T) {
		for _, in := range []string{config.ScopeFull, config.ScopeSessions} {
			got, err := resolveScope(in)
			if err != nil {
				t.Errorf("resolveScope(%q) returned error: %v", in, err)
			}
			if got != in {
				t.Errorf("resolveScope(%q) = %q, want %q", in, got, in)
			}
		}
	})

	t.Run("unrecognized value is rejected", func(t *testing.T) {
		if _, err := resolveScope("bogus"); err == nil {
			t.Error("resolveScope(\"bogus\") expected an error, got nil")
		}
	})
}
