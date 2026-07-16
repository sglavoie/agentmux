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
	args = applyNativeLogging(h.Name(), &input, args)
	switch kind {
	case InvocationFresh:
		invocation := newInvocation(h, input, withPrompt(args, input.InitialPrompt))
		invocation.Kind = kind
		return invocation, nil
	case InvocationResume:
		if err := requireSessionID(h, kind, input); err != nil {
			return Invocation{}, err
		}
		args = append(args, "--resume", input.SessionID)
		invocation := newInvocation(h, input, withPrompt(args, input.InitialPrompt))
		invocation.Kind = kind
		return invocation, nil
	case InvocationFork:
		if err := requireSessionID(h, kind, input); err != nil {
			return Invocation{}, err
		}
		args = append(args, "--resume", input.SessionID, "--fork-session")
		invocation := newInvocation(h, input, withPrompt(args, input.InitialPrompt))
		invocation.Kind = kind
		return invocation, nil
	default:
		return Invocation{}, unsupportedInvocation(h, kind)
	}
}
