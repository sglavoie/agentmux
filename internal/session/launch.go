package session

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/flightlog"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/shellquote"
)

var launchDebug = newFileLogger("/tmp/amx-debug.log", "[launch] ")

// LaunchPolicy controls whether launch honors the duplicate-session guard.
type LaunchPolicy int

const (
	// LaunchPolicyDedupExisting is the default idempotent behavior: if the
	// same profile/project key is already running, launch returns without
	// creating another session.
	LaunchPolicyDedupExisting LaunchPolicy = iota
	// LaunchPolicyAlwaysNewInstance bypasses the duplicate-session guard and
	// always creates a new runtime session.
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

// BuildUserVars constructs the metadata written onto a launched pane.
func BuildUserVars(sess config.Session, acc config.Account, defaults config.Defaults) map[string]string {
	args := ResolveArgs(sess, acc, defaults)
	explicitArgs := shellquote.Join(sess.EffectiveArgs())
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
		// Presence matters: an empty value means this session had no
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

// LaunchOn launches a single session from config. The default policy is
// LaunchPolicyDedupExisting, which returns nil silently if the session is
// already running.
func LaunchOn(rt Runtime, sess config.Session, acc config.Account, cfg *config.Config, policies ...LaunchPolicy) (LaunchResult, error) {
	return launchFresh(rt, sess, acc, cfg, "", lineage{}, policies...)
}

// LaunchFreshOn starts a new harness conversation with an optional initial
// instruction. It deliberately bypasses the project-key deduplication guard:
// callers use it for a new handoff or another explicitly requested fresh
// session, where an existing session for the same project is not a substitute.
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
	return launchTmux(rt, sess, acc, cfg, formattedTitle, build, policy, lin)
}

// LaunchContinuityOn launches a native resume or fork. A
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
	var lin lineage
	if kind == harness.InvocationResume {
		if identity, ok := conversation.New(acc.EffectiveHarness(), sessionID); ok {
			lin.conversation = &identity
		}
	}
	return launchTmux(rt, sess, acc, cfg, formattedTitle, build, LaunchPolicyAlwaysNewInstance, lin)
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

type launchFlightIdentity struct {
	runtimeID string
	operation string
}

func recordHarnessLaunch(cfg *config.Config, sess config.Session, acc config.Account, invocation harness.Invocation, outcome, reason string, nativeReport diagnostics.NativeReport, identity ...launchFlightIdentity) {
	if outcome == "success" {
		e := flightlog.Event{ID: "launch:" + invocation.LaunchID, At: time.Now().UTC(), Kind: "launch", Key: "launch:" + invocation.LaunchID, LaunchID: invocation.LaunchID, Harness: invocation.Harness, Profile: effectiveSessionProfile(sess, acc), Project: sess.Title, Operation: string(invocation.Kind)}
		if len(identity) > 0 {
			e.RuntimeSessionID = identity[0].runtimeID
			if identity[0].operation != "" {
				e.Operation = identity[0].operation
			}
		}
		if cfg != nil {
			for _, profile := range cfg.Accounts {
				name := profile.EffectiveHarness()
				if !slices.Contains(e.Fleet, name) {
					e.Fleet = append(e.Fleet, name)
				}
			}
		}
		if invocation.SessionID != "" && invocation.Kind != harness.InvocationFork {
			e.ConversationID = invocation.Harness + ":" + invocation.SessionID
		}
		if store, err := flightlog.DefaultStore(); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			err = store.Append(ctx, e)
			cancel()
			if err != nil {
				slog.Warn("flight log: launch could not be recorded", "error", err)
			}
		}
	}
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
	if len(identity) > 0 {
		envelope.Evidence["runtime_session_id"] = identity[0].runtimeID
		if identity[0].operation != "" {
			envelope.Evidence["operation"] = identity[0].operation
		}
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
