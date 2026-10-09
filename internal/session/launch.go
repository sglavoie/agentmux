package session

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

var launchDebug = newFileLogger("/tmp/amx-debug.log", "[launch] ")

// Color-retry tuning is exposed as package-level vars (not consts) so tests
// can shrink the delays without sleeping for ~1.3s of real time.
var (
	colorMaxRetries   = 5
	colorInitialDelay = 100 * time.Millisecond
	colorBackoffMul   = 1.5
)

// LaunchPolicy controls whether Launch should honor the duplicate-session
// guard or deliberately create another tab for the same session key.
type LaunchPolicy int

const (
	// LaunchPolicyDedupExisting is the default idempotent behavior: if the
	// same profile/project key is already running, Launch returns without
	// creating another tab.
	LaunchPolicyDedupExisting LaunchPolicy = iota
	// LaunchPolicyAlwaysNewInstance bypasses the duplicate-session guard and
	// always asks Kitty to create a new tab.
	LaunchPolicyAlwaysNewInstance
)

func (p LaunchPolicy) String() string {
	switch p {
	case LaunchPolicyDedupExisting:
		return "dedup-existing"
	case LaunchPolicyAlwaysNewInstance:
		return "always-new-instance"
	default:
		return "unknown"
	}
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
	profileName := effectiveSessionProfile(sess, acc)
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
		// Presence matters: an empty value means this modern tab had no
		// project-explicit args, so restart/profile-switch paths may recompute
		// profile/default args instead of pinning inherited ones.
		"cc_explicit_flags": explicitArgs,
		"cc_env":            ccEnv,
		"cc_groups":         groups,
	}
	return userVars
}

func buildLaunchUserVars(sess config.Session, acc config.Account, defaults config.Defaults, invocation harness.Invocation) map[string]string {
	vars := BuildUserVars(sess, acc, defaults)
	vars["amx_launch_id"] = invocation.LaunchID
	vars["amx_native_coverage"] = string(invocation.NativeLogging.Coverage)
	vars["amx_native_log_path"] = invocation.NativeLogging.Path
	vars["amx_native_log_level"] = invocation.NativeLogging.Level
	vars["amx_native_override"] = invocation.NativeLogging.Override
	vars["amx_harness_session"] = invocation.SessionID
	vars["amx_asl_run_id"] = invocationRunID(invocation)
	return vars
}

// Launch launches a single session from config. The default policy is
// LaunchPolicyDedupExisting, which returns nil silently if the session is
// already running.
func Launch(client kitty.KittyClient, sess config.Session, acc config.Account, cfg *config.Config, policies ...LaunchPolicy) error {
	_, err := LaunchOn(KittyRuntime(client), sess, acc, cfg, policies...)
	return err
}

// LaunchOn is Launch on the backend rt selects.
func LaunchOn(rt Runtime, sess config.Session, acc config.Account, cfg *config.Config, policies ...LaunchPolicy) (LaunchResult, error) {
	return launchFresh(rt, sess, acc, cfg, "", lineage{}, policies...)
}

// LaunchFresh starts a new harness conversation with an optional initial
// instruction. It deliberately bypasses the project-key deduplication guard:
// callers use it for a new handoff or another explicitly requested fresh
// session, where an existing tab for the same project is not a substitute.
func LaunchFresh(client kitty.KittyClient, sess config.Session, acc config.Account, cfg *config.Config, initialPrompt string) error {
	_, err := LaunchFreshOn(KittyRuntime(client), sess, acc, cfg, initialPrompt)
	return err
}

// LaunchFreshOn is LaunchFresh on the backend rt selects.
func LaunchFreshOn(rt Runtime, sess config.Session, acc config.Account, cfg *config.Config, initialPrompt string) (LaunchResult, error) {
	return launchFresh(rt, sess, acc, cfg, initialPrompt, lineage{}, LaunchPolicyAlwaysNewInstance)
}

func launchFresh(rt Runtime, sess config.Session, acc config.Account, cfg *config.Config, initialPrompt string, lin lineage, policies ...LaunchPolicy) (LaunchResult, error) {
	formattedTitle := FormatTitle(sess, acc, cfg)
	policy := LaunchPolicyDedupExisting
	if len(policies) > 0 {
		policy = policies[0]
	}
	launchDebug.Printf("Launch: title=%q acc=%q dir=%q policy=%s backend=%s", formattedTitle, acc.Name, sess.Directory, policy, rt.Kind())
	build := func() (harness.Invocation, error) {
		// Build the structured fresh invocation before rendering it through
		// the shared shell-quoting boundary. Resume/fork callers use the same
		// seam.
		invocation, err := buildLaunchInvocation(sess, acc, cfg.Defaults, harness.InvocationFresh, "", initialPrompt)
		if err != nil {
			return harness.Invocation{}, fmt.Errorf("building fresh invocation for %q: %w", formattedTitle, err)
		}
		return invocation, nil
	}
	if rt.Kind() == runtimesession.BackendTmux {
		return launchTmux(rt, sess, acc, cfg, formattedTitle, build, policy, lin)
	}
	result := LaunchResult{Backend: runtimesession.BackendKitty}
	client := rt.Kitty
	if client == nil {
		return result, errNoKittyClient
	}

	osWindows, err := client.ListWindows()
	if err != nil {
		return result, fmt.Errorf("listing running sessions: %w", err)
	}
	running, _ := runningSessionsFromOSWindows(osWindows, cfg)
	launchDebug.Printf("Launch: %d running sessions", len(running))
	for _, r := range running {
		launchDebug.Printf("  running: base=%q account=%q tabID=%d", r.Base, r.Account, r.TabID)
	}

	if policy != LaunchPolicyAlwaysNewInstance {
		profileName := effectiveSessionProfile(sess, acc)
		if existing, exists := FindByKey(running, SessionKey(profileName, sess.Title)); exists {
			launchDebug.Printf("Launch: dedup hit, returning nil")
			return result.deduplicated(existing), nil
		}
	} else {
		launchDebug.Printf("Launch: dedup skipped")
	}

	invocation, err := build()
	if err != nil {
		return result, err
	}
	return result, launchInvocation(client, sess, acc, cfg, formattedTitle, invocation, policy)
}

// LaunchContinuity launches a native resume or fork in a new Kitty tab. The
// harness adapter owns the exact argument order; this helper owns the same
// Kitty environment, title, user-vars, placement, and cosmetic color behavior
// as a normal launch. Continuity deliberately bypasses project-key dedup: a
// previously indexed conversation may not be live even when another instance
// of that project is open.
func LaunchContinuity(
	client kitty.KittyClient,
	sess config.Session,
	acc config.Account,
	cfg *config.Config,
	kind harness.InvocationKind,
	sessionID string,
	initialPrompt string,
) error {
	_, err := LaunchContinuityOn(KittyRuntime(client), sess, acc, cfg, kind, sessionID, initialPrompt)
	return err
}

// LaunchContinuityOn is LaunchContinuity on the backend rt selects. On tmux a
// resume registers the resumed conversation with its runtime session, so a
// conversation already live in another runtime session is refused instead of
// running twice.
func LaunchContinuityOn(
	rt Runtime,
	sess config.Session,
	acc config.Account,
	cfg *config.Config,
	kind harness.InvocationKind,
	sessionID string,
	initialPrompt string,
) (LaunchResult, error) {
	result := LaunchResult{Backend: rt.Kind()}
	if kind != harness.InvocationResume && kind != harness.InvocationFork {
		return result, fmt.Errorf("launching continuity session: unsupported operation %q", kind)
	}
	if cfg == nil {
		return result, fmt.Errorf("launching continuity session: configuration is required")
	}
	formattedTitle := FormatTitle(sess, acc, cfg)
	build := func() (harness.Invocation, error) {
		invocation, err := buildLaunchInvocation(sess, acc, cfg.Defaults, kind, sessionID, initialPrompt)
		if err != nil {
			return harness.Invocation{}, fmt.Errorf("building %s invocation for %q: %w", kind, formattedTitle, err)
		}
		return invocation, nil
	}
	if rt.Kind() == runtimesession.BackendTmux {
		var lin lineage
		if kind == harness.InvocationResume {
			if identity, ok := conversation.New(acc.EffectiveHarness(), sessionID); ok {
				lin.conversation = &identity
			}
		}
		return launchTmux(rt, sess, acc, cfg, formattedTitle, build, LaunchPolicyAlwaysNewInstance, lin)
	}
	if rt.Kitty == nil {
		return result, errNoKittyClient
	}
	invocation, err := build()
	if err != nil {
		return result, err
	}
	return result, launchInvocation(rt.Kitty, sess, acc, cfg, formattedTitle, invocation, LaunchPolicyAlwaysNewInstance)
}

// launchInvocation carries the Kitty-facing half shared by normal and
// continuity launches. The caller must have already built the harness-owned
// invocation, so this function never reconstructs CLI arguments.
func launchInvocation(client kitty.KittyClient, sess config.Session, acc config.Account, cfg *config.Config, formattedTitle string, invocation harness.Invocation, policy LaunchPolicy) error {
	osWindows, err := client.ListWindows()
	if err != nil {
		return fmt.Errorf("listing running sessions: %w", err)
	}

	// Record pre-launch tab IDs for color retry later. We include *every* tab
	// (managed and unmanaged) so a pre-existing tab whose title coincides with
	// formattedTitle cannot be misidentified as the newly launched tab and
	// repainted by applyColorWithRetry.
	beforeGroups := allGroupRefs(osWindows)
	term := kitty.NewBackend(client)

	// Compute insert location for sorted insertion.
	loc, prevTabID, err := ComputeInsertLocation(client, cfg.Defaults.Sort, effectiveSessionProfile(sess, acc), formattedTitle)
	if err != nil {
		// Non-fatal: fall back to default insertion.
		loc = ""
		prevTabID = 0
	}

	// Focus the reference tab so Kitty inserts relative to it.
	focusInsertReference(term, loc, prevTabID)

	harnessName := acc.EffectiveHarness()
	userVars := buildLaunchUserVars(sess, acc, cfg.Defaults, invocation)
	nativeReport := maintainNativeBeforeLaunch(cfg, osWindows, invocation.LaunchID)
	shellCmd := RenderInvocation(invocation)
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	spec := backend.LaunchSpec{
		Title:     formattedTitle,
		Directory: invocation.CWD,
		Placement: placementArg(loc),
		Env:       invocation.Environment,
		Vars:      userVars,
		Argv:      []string{shell, "-lic", shellCmd},
	}

	launchDebug.Printf("Launch: calling LaunchTab: title=%q dir=%q harness=%q loc=%q env=%v cmd=%v", spec.Title, spec.Directory, harnessName, spec.Placement, spec.Env, spec.Argv)
	if err := term.Launch(spec); err != nil {
		launchDebug.Printf("Launch: LaunchTab error: %v", err)
		recordHarnessLaunch(cfg, sess, acc, invocation, "error", err.Error(), nativeReport)
		return fmt.Errorf("launching session %q: %w", formattedTitle, err)
	}
	launchDebug.Printf("Launch: LaunchTab succeeded")
	recordHarnessLaunch(cfg, sess, acc, invocation, "success", "launched", nativeReport)

	// Apply tab color with exponential backoff retry. Agent Mux owns the
	// inactive profile accent; active colors reset to kitty.conf so the focused
	// tab remains prominent even when the tab has a profile color.
	if color, err := palette.Lookup(acc.Color); err == nil {
		if colorErr := applyColorWithRetry(term, beforeGroups, formattedTitle, color); colorErr != nil {
			slog.Warn("applying tab color after launch",
				"title", formattedTitle,
				"account", acc.Name,
				"harness", acc.EffectiveHarness(),
				"profile_color", acc.Color,
				"error", colorErr,
			)
		}
	}
	return nil
}

func maintainNativeBeforeLaunch(cfg *config.Config, windows []kitty.OSWindow, launchID string) diagnostics.NativeReport {
	return maintainNativeForActive(cfg, activeLaunchIDs(windows), launchID)
}

// maintainNativeForActive prunes native logs while keeping every launch in
// active, plus the one about to start.
func maintainNativeForActive(cfg *config.Config, active map[string]struct{}, launchID string) diagnostics.NativeReport {
	if cfg == nil || !cfg.Defaults.Diagnostics.NativeLoggingValue() {
		return diagnostics.NativeReport{}
	}
	paths, err := diagnostics.DefaultPaths()
	if err != nil {
		return diagnostics.NativeReport{Errors: []string{err.Error()}}
	}
	if launchID != "" {
		active[launchID] = struct{}{}
	}
	report, err := diagnostics.MaintainNative(paths, nativeOptions(cfg.Defaults.Diagnostics), active)
	if err != nil {
		report.Errors = append(report.Errors, err.Error())
	}
	return report
}

func nativeOptions(policy config.DiagnosticsConfig) diagnostics.NativeOptions {
	return diagnostics.NativeOptions{
		KeepDays:       policy.NativeKeepDays,
		MaxBytes:       int64(policy.NativeMaxMiB) << 20,
		LaunchMaxBytes: int64(policy.NativeSessionMaxMiB) << 20,
	}
}

func activeLaunchIDs(windows []kitty.OSWindow) map[string]struct{} {
	active := make(map[string]struct{})
	for _, osWindow := range windows {
		for _, tab := range osWindow.Tabs {
			for _, window := range tab.Windows {
				if window.UserVars.LaunchID != "" {
					active[window.UserVars.LaunchID] = struct{}{}
				}
			}
		}
	}
	return active
}

func recordHarnessLaunch(cfg *config.Config, sess config.Session, acc config.Account, invocation harness.Invocation, outcome, reason string, nativeReport diagnostics.NativeReport) {
	if cfg == nil || !cfg.Defaults.Diagnostics.JournalEnabledValue() {
		return
	}
	paths, err := diagnostics.DefaultPaths()
	if err != nil {
		return
	}
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{
		KeepDays: cfg.Defaults.Diagnostics.JournalKeepDays,
		MaxBytes: int64(cfg.Defaults.Diagnostics.JournalMaxMiB) << 20,
	})
	if err != nil {
		return
	}
	envelope := diagnostics.NewEnvelope("session.launch")
	envelope.LaunchID = invocation.LaunchID
	envelope.Harness = invocation.Harness
	envelope.Profile = effectiveSessionProfile(sess, acc)
	envelope.SessionID = invocation.SessionID
	envelope.RunID = invocationRunID(invocation)
	envelope.Outcome = outcome
	envelope.Reason = reason
	envelope.Evidence = map[string]string{
		"project":                    sess.Title,
		"operation":                  string(invocation.Kind),
		"native_coverage":            string(invocation.NativeLogging.Coverage),
		"native_path":                invocation.NativeLogging.Path,
		"native_level":               invocation.NativeLogging.Level,
		"native_override":            invocation.NativeLogging.Override,
		"native_total_bytes":         strconv.FormatInt(nativeReport.TotalBytes, 10),
		"native_excess_bytes":        strconv.FormatInt(nativeReport.ExcessBytes, 10),
		"native_active_excess_bytes": strconv.FormatInt(nativeReport.ActiveExcessBytes, 10),
		"native_maintenance_errors":  strconv.Itoa(len(nativeReport.Errors)),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	journal.TryAppend(ctx, envelope)
}

func invocationRunID(invocation harness.Invocation) string {
	for _, key := range []string{"AMX_ASL_RUN_ID", "ASL_RUN_ID", "AGENT_SESSION_LOOP_RUN_ID"} {
		if value := strings.TrimSpace(invocation.Environment[key]); value != "" {
			return value
		}
	}
	return ""
}

func effectiveSessionProfile(sess config.Session, acc config.Account) string {
	if profileName := sess.EffectiveProfile(); profileName != "" {
		return profileName
	}
	return acc.Name
}

// placementArg converts a logical insert location to a backend placement.
// Returns the default placement for anything unrecognized.
func placementArg(loc string) backend.Placement {
	switch loc {
	case "first":
		return backend.PlacementFirst
	case "after":
		return backend.PlacementAfter
	case "before":
		return backend.PlacementBefore
	default:
		return backend.PlacementDefault
	}
}

// focusInsertReference focuses the tab a relative placement is computed
// against, then gives the terminal a moment to apply it before the launch.
func focusInsertReference(term backend.Presenter, loc string, prevTabID int) {
	if (loc == "after" || loc == "before") && prevTabID != 0 {
		_ = term.Focus(kitty.GroupRef(prevTabID))
		time.Sleep(100 * time.Millisecond)
	}
}

// allGroupRefs returns every tab present in osWindows as a backend group,
// including unmanaged tabs. The color-retry path uses this set to detect
// "tabs that existed before Launch" so it never repaints a pre-existing tab
// whose title happens to match the new session's formattedTitle.
func allGroupRefs(osWindows []kitty.OSWindow) map[backend.Ref]bool {
	refs := make(map[backend.Ref]bool)
	for _, osw := range osWindows {
		for _, tab := range osw.Tabs {
			refs[kitty.GroupRef(tab.ID)] = true
		}
	}
	return refs
}

// colorTarget is what the post-launch color pass needs from a backend.
type colorTarget interface {
	backend.Discoverer
	backend.Presenter
}

// applyColorWithRetry polls for the newly created tab and sets its color.
// It retries with exponential backoff to handle Kitty's registration delay.
// Returns nil once the color is applied or the last error encountered if
// every retry attempt fails; callers should treat the error as cosmetic.
func applyColorWithRetry(term colorTarget, beforeGroups map[backend.Ref]bool, formattedTitle string, color palette.Color) error {
	delay := colorInitialDelay
	var lastErr error
	colors := backend.Colors{
		ActiveBg:   backend.ColorReset,
		InactiveBg: color.InactiveBg,
		ActiveFg:   backend.ColorReset,
		InactiveFg: palette.InactiveFg,
	}
	for i := range colorMaxRetries {
		panes, err := term.Discover()
		if err != nil {
			lastErr = fmt.Errorf("listing windows for color apply: %w", err)
		} else {
			lastErr = fmt.Errorf("new tab %q not visible in any kitty window", formattedTitle)
			tried := make(map[backend.Ref]bool)
			for _, pane := range panes {
				if beforeGroups[pane.Group] || tried[pane.Group] || palette.StripIcons(pane.GroupTitle) != formattedTitle {
					continue
				}
				tried[pane.Group] = true
				if setErr := term.SetColors(pane.Group, colors); setErr != nil {
					lastErr = fmt.Errorf("setting tab color: %w", setErr)
				} else {
					return nil
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
