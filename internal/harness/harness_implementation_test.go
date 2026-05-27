package harness

import (
	"os"
	"path/filepath"
	"testing"
)

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
