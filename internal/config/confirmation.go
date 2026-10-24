package config

import (
	"fmt"
	"os"
	"strings"
)

// WorkspaceConfirmationEnv names the app-owned live policy file. Only embedded
// management views receive it; agent terminals and shells do not inherit it.
const WorkspaceConfirmationEnv = "AMX_WORKSPACE_CONFIRMATION_FILE"

// ConfirmationPolicies are the workspace confirmation policies
// ConfirmationRequired accepts, in the order they are offered.
func ConfirmationPolicies() []string { return []string{"follow-cli", "always", "never"} }

// ConfirmationRequired evaluates an explicit workspace policy. Inheritance
// rereads config.toml so already-open views follow edits without restarting.
func ConfirmationRequired(policy string) (bool, error) {
	switch strings.TrimSpace(policy) {
	case "always":
		return true, nil
	case "never":
		return false, nil
	case "follow-cli":
		cfg, err := LoadDefault()
		if err != nil {
			return true, err
		}
		return cfg.Defaults.ConfirmDestructive, nil
	default:
		return true, fmt.Errorf("unknown confirmation policy %q: want one of %s", policy, strings.Join(ConfirmationPolicies(), ", "))
	}
}

// WorkspaceConfirmation preserves ordinary terminal behavior. An unreadable
// workspace policy asks for confirmation; it must never silently disable it.
func WorkspaceConfirmation(fallback bool) bool {
	path := os.Getenv(WorkspaceConfirmationEnv)
	if path == "" {
		return fallback
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	confirm, err := ConfirmationRequired(string(data))
	return err != nil || confirm
}
