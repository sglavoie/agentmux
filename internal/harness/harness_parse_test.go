package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseInstalledEventsMalformedJSONReturnsError(t *testing.T) {
	for _, name := range []string{Claude, Copilot, Codex} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "hooks.json")
			if err := os.WriteFile(path, []byte(`{"hooks":`), 0o600); err != nil {
				t.Fatalf("write hooks file: %v", err)
			}

			_, err := Lookup(name).HookContract().ParseInstalledEvents(path)
			if err == nil {
				t.Fatalf("ParseInstalledEvents(%q) returned nil error for malformed JSON", name)
			}
			if !strings.Contains(err.Error(), "parsing") {
				t.Fatalf("ParseInstalledEvents(%q) error = %v, want parsing error", name, err)
			}
		})
	}
}

func TestParseInstalledEventsMalformedHooksSectionReturnsError(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{Claude, `{"hooks":"broken"}`},
		{Copilot, `{"hooks":"broken"}`},
		{Codex, `{"hooks":"broken"}`},
		{Codex, `{"hooks":{"SessionStart":"broken"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/"+tc.data, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "hooks.json")
			if err := os.WriteFile(path, []byte(tc.data), 0o600); err != nil {
				t.Fatalf("write hooks file: %v", err)
			}

			_, err := Lookup(tc.name).HookContract().ParseInstalledEvents(path)
			if err == nil {
				t.Fatalf("ParseInstalledEvents(%q) returned nil error for malformed hooks section", tc.name)
			}
			if !strings.Contains(err.Error(), "parsing") {
				t.Fatalf("ParseInstalledEvents(%q) error = %v, want parsing error", tc.name, err)
			}
		})
	}
}
