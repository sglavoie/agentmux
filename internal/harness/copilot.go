package harness

import (
	"os"
	"path/filepath"
)

type copilotHarness struct{}

func (copilotHarness) Name() string          { return Copilot }
func (copilotHarness) Executable() string    { return "copilot" }
func (copilotHarness) ConfigEnvVar() string  { return "COPILOT_HOME" }
func (copilotHarness) DefaultHomeDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".copilot")
}
func (copilotHarness) PipeModeFlags() []string { return []string{"-p", "--prompt"} }
func (copilotHarness) HookContract() HookContract { return copilotHookContract{} }
