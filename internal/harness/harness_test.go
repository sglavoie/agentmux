package harness

import (
	"os"
	"path/filepath"
	"slices"
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

func TestClaudeHarness(t *testing.T) {
	h := claudeHarness{}
	home, _ := os.UserHomeDir()

	if got := h.Name(); got != "claude" {
		t.Errorf("Name() = %q, want %q", got, "claude")
	}
	if got := h.Executable(); got != "claude" {
		t.Errorf("Executable() = %q, want %q", got, "claude")
	}
	if got := h.ConfigEnvVar(); got != "CLAUDE_CONFIG_DIR" {
		t.Errorf("ConfigEnvVar() = %q, want %q", got, "CLAUDE_CONFIG_DIR")
	}
	if got := h.DefaultHomeDir(); got != filepath.Join(home, ".claude") {
		t.Errorf("DefaultHomeDir() = %q, want %q", got, filepath.Join(home, ".claude"))
	}
	tokens := h.PipeModeTokens()
	if len(tokens) != 2 || tokens[0] != "-p" || tokens[1] != "--print" {
		t.Errorf("PipeModeTokens() = %v, want [-p --print]", tokens)
	}
}

func TestCopilotHarness(t *testing.T) {
	h := copilotHarness{}
	home, _ := os.UserHomeDir()

	if got := h.Name(); got != "copilot" {
		t.Errorf("Name() = %q, want %q", got, "copilot")
	}
	if got := h.Executable(); got != "copilot" {
		t.Errorf("Executable() = %q, want %q", got, "copilot")
	}
	if got := h.ConfigEnvVar(); got != "COPILOT_HOME" {
		t.Errorf("ConfigEnvVar() = %q, want %q", got, "COPILOT_HOME")
	}
	if got := h.DefaultHomeDir(); got != filepath.Join(home, ".copilot") {
		t.Errorf("DefaultHomeDir() = %q, want %q", got, filepath.Join(home, ".copilot"))
	}
	tokens := h.PipeModeTokens()
	if len(tokens) != 2 || tokens[0] != "-p" || tokens[1] != "--prompt" {
		t.Errorf("PipeModeTokens() = %v, want [-p --prompt]", tokens)
	}
}

func TestCodexHarness(t *testing.T) {
	h := codexHarness{}
	home, _ := os.UserHomeDir()

	if got := h.Name(); got != "codex" {
		t.Errorf("Name() = %q, want %q", got, "codex")
	}
	if got := h.Executable(); got != "codex" {
		t.Errorf("Executable() = %q, want %q", got, "codex")
	}
	if got := h.ConfigEnvVar(); got != "CODEX_HOME" {
		t.Errorf("ConfigEnvVar() = %q, want %q", got, "CODEX_HOME")
	}
	if got := h.DefaultHomeDir(); got != filepath.Join(home, ".codex") {
		t.Errorf("DefaultHomeDir() = %q, want %q", got, filepath.Join(home, ".codex"))
	}
	tokens := h.PipeModeTokens()
	if len(tokens) != 3 || tokens[0] != "exec" || tokens[1] != "e" || tokens[2] != "review" {
		t.Errorf("PipeModeTokens() = %v, want [exec e review]", tokens)
	}
}

func TestPipeModeTokens(t *testing.T) {
	claude := Lookup("claude")
	copilot := Lookup("copilot")
	codex := Lookup("codex")

	cf := claude.PipeModeTokens()
	if len(cf) != 2 || cf[0] != "-p" || cf[1] != "--print" {
		t.Errorf("claude PipeModeTokens = %v, want [-p --print]", cf)
	}

	pf := copilot.PipeModeTokens()
	if len(pf) != 2 || pf[0] != "-p" || pf[1] != "--prompt" {
		t.Errorf("copilot PipeModeTokens = %v, want [-p --prompt]", pf)
	}

	tf := codex.PipeModeTokens()
	if len(tf) != 3 || tf[0] != "exec" || tf[1] != "e" || tf[2] != "review" {
		t.Errorf("codex PipeModeTokens = %v, want [exec e review]", tf)
	}
}

type fakeHarness struct{ name string }

func (f fakeHarness) Name() string               { return f.name }
func (f fakeHarness) Executable() string         { return "fake" }
func (f fakeHarness) ConfigEnvVar() string       { return "FAKE_DIR" }
func (f fakeHarness) DefaultHomeDir() string     { return "/fake/home" }
func (f fakeHarness) PipeModeTokens() []string   { return []string{"--fake"} }
func (f fakeHarness) HookContract() HookContract { return fakeHookContract{} }

type fakeHookContract struct{}

func (fakeHookContract) InstallPath(homeDir string) string { return homeDir + "/fake-hooks.json" }
func (fakeHookContract) MergeData(_ []byte, _ []string, _ string) ([]byte, error) {
	return []byte("{}\n"), nil
}
func (fakeHookContract) ParseInstalledEvents(_ string) ([]string, error) { return nil, nil }

func TestRegister(t *testing.T) {
	const fakeName = "fake-harness"

	t.Cleanup(func() { delete(registry, fakeName) })

	// Unknown name defaults to claude before registration.
	if got := Lookup(fakeName).Name(); got != "claude" {
		t.Fatalf("pre-register: expected claude default, got %q", got)
	}

	Register(fakeHarness{name: fakeName})

	got := Lookup(fakeName)
	if got.Name() != fakeName {
		t.Errorf("post-register: Lookup(%q).Name() = %q, want %q", fakeName, got.Name(), fakeName)
	}
	if got.Executable() != "fake" {
		t.Errorf("post-register: Executable() = %q, want %q", got.Executable(), "fake")
	}
}

func TestClaudeHooksIncludeCompletionSignals(t *testing.T) {
	hooks := buildClaudeHooks(nil, "amx")
	for _, event := range []string{"Stop", "SessionEnd", "Notification", "TeammateIdle"} {
		if !slices.Contains(claudeHookEvents, event) {
			t.Fatalf("claudeHookEvents missing %q", event)
		}
		if _, ok := hooks[event]; !ok {
			t.Fatalf("buildClaudeHooks missing %q", event)
		}
	}
}

func TestCopilotHooksIncludeCompletionSignals(t *testing.T) {
	hooks := buildCopilotHooks(nil, "amx")
	for _, event := range []string{"agentStop", "sessionEnd", "notification"} {
		if !slices.Contains(copilotHookEvents, event) {
			t.Fatalf("copilotHookEvents missing %q", event)
		}
		if _, ok := hooks.Hooks[event]; !ok {
			t.Fatalf("buildCopilotHooks missing %q", event)
		}
	}
}

func TestKnownEvents_Codex(t *testing.T) {
	got := KnownEvents("codex")
	if !slices.Equal(got, codexHookEvents) {
		t.Fatalf("KnownEvents(codex) = %v, want %v", got, codexHookEvents)
	}
}

func TestCodexHooksIncludeCompletionSignals(t *testing.T) {
	hooks := buildCodexHooks(nil, "amx")
	for _, event := range []string{"Stop", "PostCompact", "SubagentStop"} {
		if !slices.Contains(codexHookEvents, event) {
			t.Fatalf("codexHookEvents missing %q", event)
		}
		if _, ok := hooks[event]; !ok {
			t.Fatalf("buildCodexHooks missing %q", event)
		}
	}
}
