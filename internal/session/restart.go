package session

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
)

var debugLog = newFileLogger("/tmp/amx-debug.log", "[restart] ")

// RestartOn replaces a running session on tmux. A launch failure leaves the
// original untouched.
func RestartOn(rt Runtime, running RunningSession, cfg *config.Config) (LaunchResult, error) {
	result := LaunchResult{Backend: rt.Kind()}
	if err := rt.Owns(running); err != nil {
		return result, err
	}
	acc, found := findAccount(cfg, running.EffectiveProfile())
	if !found {
		return result, fmt.Errorf("profile %q not found in config", running.EffectiveProfile())
	}

	sess := SessionFromRunning(running)

	result, err := launchFresh(rt, sess, acc, cfg, "", lineage{parent: running.RuntimeSessionID, operation: "restart"}, LaunchPolicyAlwaysNewInstance)
	if err != nil {
		return result, err
	}
	return result, closeReplaced(rt, running)
}

// RestartWithAccountTemplateOn relaunches a session using the target profile's
// project template, then closes the original.
func RestartWithAccountTemplateOn(rt Runtime, running RunningSession, template config.Session, targetAcc config.Account, cfg *config.Config) (LaunchResult, error) {
	result := LaunchResult{Backend: rt.Kind()}
	debugLog.Printf("RestartWithAccount: running=%+v targetAcc=%+v", running, targetAcc)
	if err := rt.Owns(running); err != nil {
		return result, err
	}
	// Case-insensitive compare: profile names get lowercased in SessionKey and
	// duplicates differing only in case are rejected by config validation, so a
	// case-sensitive equality check here would re-launch the session needlessly
	// when the user var case differs from the config name.
	if strings.EqualFold(running.EffectiveProfile(), targetAcc.Name) {
		debugLog.Printf("RestartWithAccount: same account, no-op")
		return result, nil
	}

	sess := template
	sess.Profile = targetAcc.Name
	sess.Account = targetAcc.Name
	debugLog.Printf("RestartWithAccount: session=%+v", sess)

	result, launchErr := launchFresh(rt, sess, targetAcc, cfg, "", lineage{parent: running.RuntimeSessionID, operation: "restart"}, LaunchPolicyAlwaysNewInstance)
	debugLog.Printf("RestartWithAccount: Launch err=%v", launchErr)
	if launchErr != nil {
		return result, launchErr
	}
	return result, closeReplaced(rt, running)
}

// closeReplaced requests closure of the session a restart replaced and ends
// its runtime record only after observing the terminal disappear. An
// unconfirmed close is non-fatal: the replacement has already launched, and
// reconciliation can retire the original once it actually exits.
func closeReplaced(rt Runtime, running RunningSession) error {
	observation, closeErr := CloseObserved(rt.Terminal(), running.GroupRef())
	debugLog.Printf("Restart: Close err=%v", closeErr)
	if closeErr != nil && !errors.Is(closeErr, ErrCloseTimeout) {
		return fmt.Errorf("closing %s session %s: %w", running.Backend, running.GroupRef().ID, closeErr)
	}
	if !observation.Confirmed() {
		return nil
	}
	return EndRuntimeSession(rt, running.RuntimeSessionID)
}

func findAccount(cfg *config.Config, name string) (config.Account, bool) {
	// SessionKey lowercases profile names at runtime and config validation
	// rejects case-only duplicates (see internal/config/validate.go), so a
	// case-insensitive match here is consistent with the rest of the codebase.
	// Without this fold, a user var like "Work" would fail to resolve when the
	// config was renamed to "work" (or vice versa) between launch and restart.
	for _, acc := range cfg.Accounts {
		if strings.EqualFold(acc.Name, name) {
			return acc, true
		}
	}
	return config.Account{}, false
}

// SessionFromRunning reconstructs a config.Session from pane metadata.
func SessionFromRunning(rs RunningSession) config.Session {
	projectArgs := rs.ExplicitFlags
	if !rs.ExplicitFlagsSet && projectArgs == "" {
		projectArgs = rs.EffectiveArgs()
	}

	return config.Session{
		Title:       rs.EffectiveProject(),
		Directory:   rs.Directory,
		Profile:     rs.EffectiveProfile(),
		Account:     rs.EffectiveProfile(),
		Args:        parseFlags(projectArgs),
		ClaudeFlags: parseFlags(projectArgs),
		Env:         deserializeEnv(rs.Env),
		Groups:      rs.Groups,
	}
}

// parseFlags splits a shell-quoted flags string into a slice.
func parseFlags(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	args, err := splitShellArgs(raw)
	if err != nil {
		return strings.Fields(raw)
	}
	return args
}

// deserializeEnv reverses SerializeSessionEnv: splits by "\x1f" and parses
// KEY=VALUE pairs into a map.
func deserializeEnv(raw string) map[string]string {
	if raw == "" {
		return nil
	}
	out := make(map[string]string)
	for _, pair := range strings.Split(raw, "\x1f") {
		k, v, ok := strings.Cut(pair, "=")
		if ok && k != "" {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
