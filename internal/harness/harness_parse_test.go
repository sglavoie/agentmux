package harness

import (
	"os"
	"path/filepath"
	"slices"
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

func TestParseInstalledEventsReturnsSortedEvents(t *testing.T) {
	cases := []struct {
		name string
		data string
		want []string
	}{
		{
			name: Claude,
			data: `{"hooks":{
				"Stop":[{"hooks":[{"type":"command","command":"amx hook-indicator Stop"}]}],
				"SessionStart":[{"hooks":[{"type":"command","command":"amx hook-indicator SessionStart"}]}],
				"PreToolUse":[{"hooks":[{"type":"command","command":"amx hook-indicator PreToolUse"}]}]
			}}`,
			want: []string{"PreToolUse", "SessionStart", "Stop"},
		},
		{
			name: Copilot,
			data: `{"version":1,"hooks":{
				"sessionEnd":[{"type":"command","bash":"amx hook-indicator sessionEnd"}],
				"agentStop":[{"type":"command","bash":"amx hook-indicator agentStop"}],
				"preToolUse":[{"type":"command","powershell":"amx hook-indicator preToolUse"}]
			}}`,
			want: []string{"agentStop", "preToolUse", "sessionEnd"},
		},
		{
			name: Codex,
			data: `{"hooks":{
				"Stop":[{"hooks":[{"type":"command","command":"amx hook-indicator Stop"}]}],
				"SessionStart":[{"hooks":[{"type":"command","command":"amx hook-indicator SessionStart"}]}],
				"PreToolUse":[{"hooks":[{"type":"command","command":"amx hook-indicator PreToolUse"}]}]
			}}`,
			want: []string{"PreToolUse", "SessionStart", "Stop"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "hooks.json")
			if err := os.WriteFile(path, []byte(tc.data), 0o600); err != nil {
				t.Fatalf("write hooks file: %v", err)
			}

			for i := 0; i < 20; i++ {
				got, err := Lookup(tc.name).HookContract().ParseInstalledEvents(path)
				if err != nil {
					t.Fatalf("ParseInstalledEvents(%q) err: %v", tc.name, err)
				}
				if !slices.Equal(got, tc.want) {
					t.Fatalf("ParseInstalledEvents(%q) = %v, want %v", tc.name, got, tc.want)
				}
			}
		})
	}
}
