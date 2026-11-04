package session

import (
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/shellquote"
)

// ResolveEnv builds the environment passed to a launched agent. It includes
// the harness-specific home/config variable and session-level overrides.
func ResolveEnv(sess config.Session, acc config.Account) map[string]string {
	env := make(map[string]string)

	homeDir := config.ExpandTilde(acc.EffectiveHomeDir())
	if homeDir != "" {
		if envVar := harness.EnvVar(acc.EffectiveHarness()); envVar != "" {
			env[envVar] = homeDir
		}
	}

	for k, v := range sess.Env {
		env[k] = v
	}

	return env
}

// ResolveArgs returns the effective CLI args string for a session, quoted so it
// can round-trip through pane metadata and shell commands.
// Precedence: project args > profile args > shared defaults args.
func ResolveArgs(sess config.Session, acc config.Account, defaults config.Defaults) string {
	return shellquote.Join(resolveArgv(sess, acc, defaults))
}

// ResolveFlags is kept as a compatibility alias for older Claude-oriented code.
func ResolveFlags(sess config.Session, acc config.Account, defaults config.Defaults) string {
	return ResolveArgs(sess, acc, defaults)
}

// SerializeSessionEnv serializes sess.Env as KEY=VALUE pairs joined by "\x1f"
// for storage in the cc_env user var. Returns "" if env is empty.
func SerializeSessionEnv(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+env[k])
	}
	return strings.Join(parts, "\x1f")
}
