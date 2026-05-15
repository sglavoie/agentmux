package session

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

var launchDebug = newFileLogger("/tmp/amx-debug.log", "[launch] ")

const (
	colorMaxRetries   = 5
	colorInitialDelay = 100 * time.Millisecond
	colorBackoffMul   = 1.5
)

// LaunchOpt controls optional Launch behavior.
type LaunchOpt struct {
	SkipDedup bool // skip duplicate-session check (used by restart)
}

// Launch launches a single session from config.
// Returns nil silently if the session is already running (dedup).
func Launch(client kitty.KittyClient, sess config.Session, acc config.Account, cfg *config.Config, opts ...LaunchOpt) error {
	formattedTitle := FormatTitle(sess)
	launchDebug.Printf("Launch: title=%q acc=%q dir=%q opts=%+v", formattedTitle, acc.Name, sess.Directory, opts)

	running, err := List(client)
	if err != nil {
		return fmt.Errorf("listing running sessions: %w", err)
	}
	launchDebug.Printf("Launch: %d running sessions", len(running))
	for _, r := range running {
		launchDebug.Printf("  running: base=%q account=%q tabID=%d", r.Base, r.Account, r.TabID)
	}

	skipDedup := len(opts) > 0 && opts[0].SkipDedup
	if !skipDedup {
		if _, exists := FindByKey(running, SessionKey(acc.Name, sess.Title)); exists {
			launchDebug.Printf("Launch: dedup hit, returning nil")
			return nil
		}
	} else {
		launchDebug.Printf("Launch: dedup skipped")
	}

	// Record pre-launch tab IDs for color retry later.
	beforeIDs := tabIDSet(running)

	// Compute insert location for sorted insertion.
	loc, prevTabID, err := ComputeInsertLocation(client, cfg.Defaults.Sort, acc.Name, formattedTitle)
	if err != nil {
		// Non-fatal: fall back to default insertion.
		loc = ""
		prevTabID = 0
	}

	// Focus the preceding tab so Kitty inserts the new tab after it.
	if loc == "after" && prevTabID != 0 {
		_ = client.FocusTab(kitty.MatchTabByID(prevTabID))
		time.Sleep(100 * time.Millisecond)
	}

	// Build env, user vars, and shell command.
	env := ResolveEnv(sess, acc)
	args := ResolveArgs(sess, acc, cfg.Defaults)
	explicitArgs := strings.Join(sess.EffectiveArgs(), " ")
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
		"cc_explicit_flags": explicitArgs,
		"cc_env":        ccEnv,
		"cc_groups":     groups,
	}

	shellCmd := "exec " + harness.Executable(harnessName)
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
	if color, err := palette.Lookup(acc.Color); err == nil {
		applyColorWithRetry(client, beforeIDs, formattedTitle, color)
	}

	return nil
}

// locationArg converts a logical location ("first"/"after"/"") to the
// kitten @ launch --location value. Returns "" for default behavior.
func locationArg(loc string) string {
	switch loc {
	case "first", "after":
		return loc
	default:
		return ""
	}
}

// tabIDSet returns the set of tab IDs from running sessions.
func tabIDSet(sessions []RunningSession) map[int]bool {
	ids := make(map[int]bool, len(sessions))
	for _, s := range sessions {
		ids[s.TabID] = true
	}
	return ids
}

// applyColorWithRetry polls for the newly created tab and sets its color.
// It retries with exponential backoff to handle Kitty's registration delay.
func applyColorWithRetry(client kitty.KittyClient, beforeIDs map[int]bool, formattedTitle string, color palette.Color) {
	delay := colorInitialDelay
	for range colorMaxRetries {
		osWindows, err := client.ListWindows()
		if err == nil {
			for _, osw := range osWindows {
				for _, tab := range osw.Tabs {
					if !beforeIDs[tab.ID] && palette.StripIcons(tab.Title) == formattedTitle {
						matchExpr := kitty.MatchTabByID(tab.ID)
						err := client.SetTabColor(matchExpr, color.ActiveBg, color.InactiveBg, palette.ActiveFg, palette.InactiveFg)
						if err == nil {
							return
						}
					}
				}
			}
		}
		time.Sleep(delay)
		delay = time.Duration(float64(delay) * colorBackoffMul)
	}
}
