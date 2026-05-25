package harness

import (
	"os"
	"path/filepath"
)

type claudeHarness struct{}

func (claudeHarness) Name() string         { return Claude }
func (claudeHarness) Executable() string   { return "claude" }
func (claudeHarness) ConfigEnvVar() string { return "CLAUDE_CONFIG_DIR" }
func (claudeHarness) DefaultHomeDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}
func (claudeHarness) PipeModeTokens() []string   { return []string{"-p", "--print"} }
func (claudeHarness) HookContract() HookContract { return claudeHookContract{} }
func (claudeHarness) TranscriptsDir(homeDir string) string {
	return filepath.Join(homeDir, "projects")
}
