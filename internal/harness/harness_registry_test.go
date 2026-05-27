package harness

import (
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"", "claude"},
		{"   ", "claude"},
		{"Claude", "claude"},
		{"COPILOT", "copilot"},
		{"Codex", "codex"},
		{"copilot", "copilot"},
		{"codex", "codex"},
		{"claude", "claude"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			if got := Normalize(tc.input); got != tc.want {
				t.Errorf("Normalize(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestValid(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"claude", true},
		{"Claude", true},
		{"copilot", true},
		{"COPILOT", true},
		{"codex", true},
		{"CODEX", true},
		{"", true}, // empty defaults to claude which is valid
		{"unknown", false},
		{"gpt", false},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			if got := Valid(tc.input); got != tc.want {
				t.Errorf("Valid(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestNames(t *testing.T) {
	names := Names()
	if len(names) != 3 {
		t.Fatalf("Names() len = %d, want 3", len(names))
	}
	if names[0] != "claude" {
		t.Errorf("Names()[0] = %q, want %q", names[0], "claude")
	}
	if names[1] != "copilot" {
		t.Errorf("Names()[1] = %q, want %q", names[1], "copilot")
	}
	if names[2] != "codex" {
		t.Errorf("Names()[2] = %q, want %q", names[2], "codex")
	}
}

func TestLookup(t *testing.T) {
	cases := []struct {
		input    string
		wantName string
	}{
		{"claude", "claude"},
		{"Claude", "claude"},
		{"copilot", "copilot"},
		{"COPILOT", "copilot"},
		{"codex", "codex"},
		{"CODEX", "codex"},
		{"", "claude"},        // unknown defaults to claude
		{"unknown", "claude"}, // unknown defaults to claude
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			impl := Lookup(tc.input)
			if impl.Name() != tc.wantName {
				t.Errorf("Lookup(%q).Name() = %q, want %q", tc.input, impl.Name(), tc.wantName)
			}
		})
	}
}
