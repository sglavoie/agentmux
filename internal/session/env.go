package session

import (
	"os"
	"sort"
	"strings"

	"github.com/sglavoie/cc-manager-kitty/internal/config"
)

// ResolveEnv builds the env map passed as --env flags to kitten @ launch.
// It includes KITTY_LISTEN_ON, CLAUDE_CONFIG_DIR (when non-default), and
// session-level overrides from sess.Env.
func ResolveEnv(sess config.Session, acc config.Account) map[string]string {
	env := make(map[string]string)

	if socket := os.Getenv("KITTY_LISTEN_ON"); socket != "" {
		env["KITTY_LISTEN_ON"] = socket
	}

	configDir := config.ExpandTilde(acc.ConfigDir)
	home, _ := os.UserHomeDir()
	defaultDir := home + "/.claude"
	if configDir != "" && configDir != defaultDir {
		env["CLAUDE_CONFIG_DIR"] = configDir
	}

	for k, v := range sess.Env {
		env[k] = v
	}

	return env
}

// ResolveFlags returns the effective claude flags string for a session,
// falling back to defaults.ClaudeFlags when the session has none.
func ResolveFlags(sess config.Session, defaults config.Defaults) string {
	flags := sess.ClaudeFlags
	if len(flags) == 0 {
		flags = defaults.ClaudeFlags
	}
	return strings.Join(flags, " ")
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
