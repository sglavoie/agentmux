package harness

import (
	"os"
	"path/filepath"
)

type codexHarness struct{}

func (codexHarness) Name() string         { return Codex }
func (codexHarness) Executable() string   { return "codex" }
func (codexHarness) ConfigEnvVar() string { return "CODEX_HOME" }
func (codexHarness) DefaultHomeDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}
func (codexHarness) PipeModeTokens() []string   { return []string{"exec", "e", "review"} }
func (codexHarness) HookContract() HookContract { return codexHookContract{} }
