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
func (codexHarness) TranscriptsDir(homeDir string) string {
	return filepath.Join(homeDir, "sessions")
}

func (codexHarness) InvocationCapabilities() InvocationCapabilities {
	return allNativeInvocationCapabilities
}

func (h codexHarness) BuildInvocation(kind InvocationKind, input InvocationInput) (Invocation, error) {
	// Codex parses global flags before its resume/fork subcommands. Keep the
	// profile arguments first so a custom model, sandbox mode, or config layer
	// applies consistently to fresh and continuity launches.
	args := append([]string(nil), input.ProfileArgs...)
	switch kind {
	case InvocationFresh:
		return newInvocation(h, input, withPrompt(args, input.InitialPrompt)), nil
	case InvocationResume, InvocationFork:
		if err := requireSessionID(h, kind, input); err != nil {
			return Invocation{}, err
		}
		args = append(args, string(kind), input.SessionID)
		return newInvocation(h, input, withPrompt(args, input.InitialPrompt)), nil
	default:
		return Invocation{}, unsupportedInvocation(h, kind)
	}
}
