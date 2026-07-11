package harness

import (
	"os"
	"path/filepath"
)

type copilotHarness struct{}

func (copilotHarness) Name() string         { return Copilot }
func (copilotHarness) Executable() string   { return "copilot" }
func (copilotHarness) ConfigEnvVar() string { return "COPILOT_HOME" }
func (copilotHarness) DefaultHomeDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".copilot")
}
func (copilotHarness) PipeModeTokens() []string   { return []string{"-p", "--prompt"} }
func (copilotHarness) HookContract() HookContract { return copilotHookContract{} }
func (copilotHarness) TranscriptsDir(homeDir string) string {
	return filepath.Join(homeDir, "session-state")
}

func (copilotHarness) InvocationCapabilities() InvocationCapabilities {
	return copilotInvocationCapabilities
}

func (h copilotHarness) BuildInvocation(kind InvocationKind, input InvocationInput) (Invocation, error) {
	args := append([]string(nil), input.ProfileArgs...)
	switch kind {
	case InvocationFresh:
		return newInvocation(h, input, withPrompt(args, input.InitialPrompt)), nil
	case InvocationResume:
		if err := requireSessionID(h, kind, input); err != nil {
			return Invocation{}, err
		}
		args = append(args, "--resume="+input.SessionID)
		return newInvocation(h, input, withPrompt(args, input.InitialPrompt)), nil
	case InvocationFork:
		return Invocation{}, unsupportedInvocation(h, kind)
	default:
		return Invocation{}, unsupportedInvocation(h, kind)
	}
}
