package session

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
)

var debugLog = newFileLogger("/tmp/amx-debug.log", "[restart] ")

// Restart relaunches a running session and then closes the original tab,
// preserving metadata from the running tab's user vars (profile, directory,
// args, env, groups). Launch happens before Close so that when the
// dashboard overlay is on top of the tab being closed, the new tab is
// already committed in Kitty before the dashboard process is torn down.
func Restart(client kitty.KittyClient, running RunningSession, cfg *config.Config) error {
	acc, found := findAccount(cfg, running.EffectiveProfile())
	if !found {
		return fmt.Errorf("profile %q not found in config", running.EffectiveProfile())
	}

	sess := sessionFromRunning(running)

	if err := Launch(client, sess, acc, cfg, LaunchOpt{SkipDedup: true}); err != nil {
		return err
	}

	closeErr := Close(client, running.TabID)
	// ErrCloseTimeout is non-fatal — the tab likely closed shortly after.
	if closeErr != nil && !errors.Is(closeErr, ErrCloseTimeout) {
		return fmt.Errorf("closing tab %d: %w", running.TabID, closeErr)
	}
	return nil
}

// RestartWithAccount relaunches a running session under a different profile's
// harness config and then closes the original tab. Title prefix and tab color
// update to match the target profile. If the target profile is already the current
// one, it is a no-op. Launch precedes Close for the same reason as Restart.
func RestartWithAccount(client kitty.KittyClient, running RunningSession, targetAcc config.Account, cfg *config.Config) error {
	debugLog.Printf("RestartWithAccount: running=%+v targetAcc=%+v", running, targetAcc)
	if running.EffectiveProfile() == targetAcc.Name {
		debugLog.Printf("RestartWithAccount: same account, no-op")
		return nil
	}

	sess := sessionFromRunning(running)
	sess.Profile = targetAcc.Name
	sess.Account = targetAcc.Name
	debugLog.Printf("RestartWithAccount: session=%+v", sess)

	launchErr := Launch(client, sess, targetAcc, cfg, LaunchOpt{SkipDedup: true})
	debugLog.Printf("RestartWithAccount: Launch err=%v", launchErr)
	if launchErr != nil {
		return launchErr
	}

	closeErr := Close(client, running.TabID)
	debugLog.Printf("RestartWithAccount: Close err=%v", closeErr)
	if closeErr != nil && !errors.Is(closeErr, ErrCloseTimeout) {
		return fmt.Errorf("closing tab %d: %w", running.TabID, closeErr)
	}
	return nil
}

func findAccount(cfg *config.Config, name string) (config.Account, bool) {
	for _, acc := range cfg.Accounts {
		if acc.Name == name {
			return acc, true
		}
	}
	return config.Account{}, false
}

// sessionFromRunning reconstructs a config.Session from a running tab's
// user vars. This preserves runtime overrides not written back to config.
func sessionFromRunning(rs RunningSession) config.Session {
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

// parseFlags splits a space-separated flags string into a slice.
func parseFlags(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	return strings.Fields(raw)
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
