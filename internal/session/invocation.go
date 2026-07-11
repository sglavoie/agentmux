package session

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

// BuildInvocation resolves a configured project's effective arguments,
// profile home environment, and working directory before delegating CLI
// ordering to its harness adapter. It is the single session-level seam for
// fresh, resume, and fork launches.
func BuildInvocation(
	sess config.Session,
	acc config.Account,
	defaults config.Defaults,
	kind harness.InvocationKind,
	sessionID string,
	initialPrompt string,
) (harness.Invocation, error) {
	return harness.BuildInvocation(acc.EffectiveHarness(), kind, harness.InvocationInput{
		ProfileArgs:   resolveArgv(sess, acc, defaults),
		Environment:   ResolveEnv(sess, acc),
		CWD:           config.ExpandTilde(sess.Directory),
		SessionID:     sessionID,
		InitialPrompt: initialPrompt,
	})
}

// RenderInvocation turns a structured invocation into the shell command that
// Kitty launches. Arguments are quoted here, after each harness has finalized
// their ordering. The profile-home environment variable is also pinned here:
// an interactive login shell can otherwise overwrite the environment passed to
// Kitty before the harness starts.
func RenderInvocation(invocation harness.Invocation) string {
	parts := []string{"exec"}
	if envVar := harness.EnvVar(invocation.Harness); envVar != "" {
		if envValue, ok := invocation.Environment[envVar]; ok {
			parts = append(parts, "env", shellQuoteWord(envVar+"="+envValue))
		}
	}
	parts = append(parts, shellQuoteWord(invocation.Executable))
	for _, arg := range invocation.Args {
		parts = append(parts, shellQuoteWord(arg))
	}
	return strings.Join(parts, " ")
}
