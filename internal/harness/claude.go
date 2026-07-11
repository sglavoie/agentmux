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

func (claudeHarness) InvocationCapabilities() InvocationCapabilities {
	return allNativeInvocationCapabilities
}

func (h claudeHarness) BuildInvocation(kind InvocationKind, input InvocationInput) (Invocation, error) {
	args := append([]string(nil), input.ProfileArgs...)
	switch kind {
	case InvocationFresh:
		return newInvocation(h, input, withPrompt(args, input.InitialPrompt)), nil
	case InvocationResume:
		if err := requireSessionID(h, kind, input); err != nil {
			return Invocation{}, err
		}
		args = append(args, "--resume", input.SessionID)
		return newInvocation(h, input, withPrompt(args, input.InitialPrompt)), nil
	case InvocationFork:
		if err := requireSessionID(h, kind, input); err != nil {
			return Invocation{}, err
		}
		args = append(args, "--resume", input.SessionID, "--fork-session")
		return newInvocation(h, input, withPrompt(args, input.InitialPrompt)), nil
	default:
		return Invocation{}, unsupportedInvocation(h, kind)
	}
}
