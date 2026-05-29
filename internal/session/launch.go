package session

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

var launchDebug = newFileLogger("/tmp/amx-debug.log", "[launch] ")

// Color-retry tuning is exposed as package-level vars (not consts) so tests
// can shrink the delays without sleeping for ~1.3s of real time.
var (
	colorMaxRetries   = 5
	colorInitialDelay = 100 * time.Millisecond
	colorBackoffMul   = 1.5
)

// LaunchOpt controls optional Launch behavior.
type LaunchOpt struct {
	SkipDedup bool // skip duplicate-session check (used by restart)
}

// BuildUserVars constructs the user-var map written onto a launched (or
// re-anchored) tab's window. The same map shape must be produced by both
// Launch and any future re-anchoring helper so hook-side consumers
// (EffectiveProfile, EffectiveHarness, ...) see a consistent contract.
func BuildUserVars(sess config.Session, acc config.Account, defaults config.Defaults) map[string]string {
	args := ResolveArgs(sess, acc, defaults)
	explicitArgs := shellQuoteArgs(sess.EffectiveArgs())
	groups := strings.Join(sess.Groups, "\x1f")
	ccEnv := SerializeSessionEnv(sess.Env)
	profileName := sess.EffectiveProfile()
	if profileName == "" {
		profileName = acc.Name
	}
	homeDir := config.ExpandTilde(acc.EffectiveHomeDir())
	projectDir := config.ExpandTilde(sess.Directory)
	harnessName := acc.EffectiveHarness()

	userVars := map[string]string{
		"amx_profile":   profileName,
		"amx_harness":   harnessName,
		"amx_home_dir":  homeDir,
		"amx_project":   sess.Title,
		"amx_directory": projectDir,
		"amx_args":      args,
		"amx_env":       ccEnv,
		"amx_groups":    groups,
		"cc_account":    profileName,
		"cc_config_dir": homeDir,
		"cc_cwd":        projectDir,
		"cc_flags":      args,
		"cc_env":        ccEnv,
		"cc_groups":     groups,
	}
	if explicitArgs != "" {
		userVars["cc_explicit_flags"] = explicitArgs
	}
	return userVars
}

// Launch launches a single session from config.
// Returns nil silently if the session is already running (dedup).
func Launch(client kitty.KittyClient, sess config.Session, acc config.Account, cfg *config.Config, opts ...LaunchOpt) error {
	formattedTitle := FormatTitle(sess)
	launchDebug.Printf("Launch: title=%q acc=%q dir=%q opts=%+v", formattedTitle, acc.Name, sess.Directory, opts)

	osWindows, err := client.ListWindows()
	if err != nil {
		return fmt.Errorf("listing running sessions: %w", err)
	}
	running, _ := runningSessionsFromOSWindows(osWindows, nil)
	launchDebug.Printf("Launch: %d running sessions", len(running))
	for _, r := range running {
		launchDebug.Printf("  running: base=%q account=%q tabID=%d", r.Base, r.Account, r.TabID)
	}

	skipDedup := len(opts) > 0 && opts[0].SkipDedup
	if !skipDedup {
		profileName := sess.EffectiveProfile()
		if profileName == "" {
			profileName = acc.Name
		}
		if _, exists := FindByKey(running, SessionKey(profileName, sess.Title)); exists {
			launchDebug.Printf("Launch: dedup hit, returning nil")
			return nil
		}
	} else {
		launchDebug.Printf("Launch: dedup skipped")
	}

	// Record pre-launch tab IDs for color retry later. We include *every* tab
	// (managed and unmanaged) so a pre-existing tab whose title coincides with
	// formattedTitle cannot be misidentified as the newly launched tab and
	// repainted by applyColorWithRetry.
	beforeIDs := allTabIDs(osWindows)

	// Compute insert location for sorted insertion.
	loc, prevTabID, err := ComputeInsertLocation(client, cfg.Defaults.Sort, acc.Name, formattedTitle)
	if err != nil {
		// Non-fatal: fall back to default insertion.
		loc = ""
		prevTabID = 0
	}

	// Focus the reference tab so Kitty inserts relative to it.
	if (loc == "after" || loc == "before") && prevTabID != 0 {
		_ = client.FocusTab(kitty.MatchTabByID(prevTabID))
		time.Sleep(100 * time.Millisecond)
	}

	// Build env, user vars, and shell command.
	env := ResolveEnv(sess, acc)
	args := ResolveArgs(sess, acc, cfg.Defaults)
	projectDir := config.ExpandTilde(sess.Directory)
	harnessName := acc.EffectiveHarness()
	userVars := BuildUserVars(sess, acc, cfg.Defaults)

	shellCmd := "exec " + shellQuoteWord(harness.Executable(harnessName))
	if args != "" {
		shellCmd += " " + args
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}

	launchOpts := kitty.LaunchOpts{
		Title:     formattedTitle,
		Directory: projectDir,
		Location:  locationArg(loc),
		Env:       env,
		UserVars:  userVars,
		Cmdline:   []string{shell, "-lic", shellCmd},
	}

	launchDebug.Printf("Launch: calling LaunchTab: title=%q dir=%q harness=%q loc=%q env=%v cmd=%v", launchOpts.Title, launchOpts.Directory, harnessName, launchOpts.Location, launchOpts.Env, launchOpts.Cmdline)
	if err := client.LaunchTab(launchOpts); err != nil {
		launchDebug.Printf("Launch: LaunchTab error: %v", err)
		return fmt.Errorf("launching session %q: %w", formattedTitle, err)
	}
	launchDebug.Printf("Launch: LaunchTab succeeded")

	// Apply tab color with exponential backoff retry.
	// Kitty may not register the new tab immediately for color changes.
	colorName := cfg.HarnessColor(acc.EffectiveHarness())
	if color, err := palette.Lookup(colorName); err == nil {
		if colorErr := applyColorWithRetry(client, beforeIDs, formattedTitle, color); colorErr != nil {
			// Tab color is cosmetic — degrade to a Warn-level log so the
			// failure is visible in `amx daemon start --debug` output
			// rather than silently swallowed. The session itself is fine.
			slog.Warn("applying tab color after launch",
				"title", formattedTitle,
				"account", acc.Name,
				"harness", acc.EffectiveHarness(),
				"error", colorErr,
			)
		}
	}

	return nil
}

// locationArg converts a logical location to the kitten @ launch --location
// value. Returns "" for default behavior.
func locationArg(loc string) string {
	switch loc {
	case "first", "after", "before":
		return loc
	default:
		return ""
	}
}

// allTabIDs returns the set of every tab ID present in osWindows, including
// unmanaged tabs. The color-retry path uses this set to detect "tabs that
// existed before Launch" so it never repaints a pre-existing tab whose title
// happens to match the new session's formattedTitle.
func allTabIDs(osWindows []kitty.OSWindow) map[int]bool {
	ids := make(map[int]bool)
	for _, osw := range osWindows {
		for _, tab := range osw.Tabs {
			ids[tab.ID] = true
		}
	}
	return ids
}

// applyColorWithRetry polls for the newly created tab and sets its color.
// It retries with exponential backoff to handle Kitty's registration delay.
// Returns nil once the color is applied or the last error encountered if
// every retry attempt fails; callers should treat the error as cosmetic.
func applyColorWithRetry(client kitty.KittyClient, beforeIDs map[int]bool, formattedTitle string, color palette.Color) error {
	delay := colorInitialDelay
	var lastErr error
	for i := range colorMaxRetries {
		osWindows, err := client.ListWindows()
		if err != nil {
			lastErr = fmt.Errorf("listing windows for color apply: %w", err)
		} else {
			lastErr = fmt.Errorf("new tab %q not visible in any kitty window", formattedTitle)
			for _, osw := range osWindows {
				for _, tab := range osw.Tabs {
					if !beforeIDs[tab.ID] && palette.StripIcons(tab.Title) == formattedTitle {
						matchExpr := kitty.MatchTabByID(tab.ID)
						if setErr := client.SetTabColor(matchExpr, color.ActiveBg, color.InactiveBg, palette.ActiveFg, palette.InactiveFg); setErr != nil {
							lastErr = fmt.Errorf("setting tab color: %w", setErr)
						} else {
							return nil
						}
					}
				}
			}
		}
		// Skip the sleep on the final iteration — we're about to return.
		// Previously the function slept the full backoff after the last attempt
		// (~500 ms at default tuning) before returning, padding every failed
		// launch with wasted wall time the caller cannot reclaim.
		if i == colorMaxRetries-1 {
			break
		}
		time.Sleep(delay)
		delay = time.Duration(float64(delay) * colorBackoffMul)
	}
	return lastErr
}
