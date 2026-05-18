package session

import (
	"os"
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

// ResolveEnv builds the env map passed as --env flags to kitten @ launch.
// It includes KITTY_LISTEN_ON, a harness-specific home/config env var when
// non-default, and
// session-level overrides from sess.Env.
func ResolveEnv(sess config.Session, acc config.Account) map[string]string {
	env := make(map[string]string)

	if socket := os.Getenv("KITTY_LISTEN_ON"); socket != "" {
		env["KITTY_LISTEN_ON"] = socket
	}

	homeDir := config.ExpandTilde(acc.EffectiveHomeDir())
	defaultDir := harness.DefaultHomeDir(acc.EffectiveHarness())
	if homeDir != "" && homeDir != defaultDir {
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
// can round-trip through Kitty user vars and shell -c launch commands.
// Precedence: project args > profile args > shared defaults args.
func ResolveArgs(sess config.Session, acc config.Account, defaults config.Defaults) string {
	return shellQuoteArgs(resolveArgv(sess, acc, defaults))
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
