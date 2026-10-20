package debug

import (
	"fmt"
	"strings"
)

// Remediation is what an operator does about a finding, carried in its own
// field so a consumer reads it without parsing the message. It is additive to
// the doctor and audit reports (`remediation`, omitted when empty), and every
// message keeps its exact text: several already said what to do, and anything
// that matched on them still does. The text report prints the remediation on
// its own line unless the message already says it word for word.
//
// Failures and warnings carry one; informational findings are context and
// carry none. Every command, flag, and config key named here exists in this
// build; TestDoctorRemediationsNameRealCommands holds the commands to the
// command tree.
//
// The daemon-invariant remediations name the window the finding is about, so
// the export they point at is the one that holds its evidence.

// newDoctorFinding is a finding with its remediation filled in.
func newDoctorFinding(severity, check string, windowID int, message string) doctorFinding {
	finding := doctorFinding{Severity: severity, Check: check, WindowID: windowID, Message: message}
	finding.Remediation = doctorRemediation(finding)
	return finding
}

// doctorRemediation is the remediation for a finding whose remedy follows
// from its identifier (and, for a few, which of its messages it is). It is
// empty for informational findings and for the identifiers whose remedy comes
// from elsewhere: the backend problems carry `amx backend status`'s own hint,
// and the ASL dimensions their dimension's remediation (see
// attachDoctorRemediations).
func doctorRemediation(f doctorFinding) string {
	if f.Severity == severityInfo {
		return ""
	}
	export := "`amx daemon export-events --last 50`"
	if f.WindowID != 0 {
		export = fmt.Sprintf("`amx daemon export-events --window %d --last 50`", f.WindowID)
	}
	reportBug := "save `amx debug bundle --mode share`, then run `amx daemon restart` to rebuild render state from the reducer; if it returns on the installed build, report it with the bundle"
	switch f.Check {
	// Daemon reachability and identity.
	case "daemon_unreachable":
		return "run `amx daemon restart` (it starts the daemon when none is running), then `amx daemon status`"
	case "daemon_degraded":
		return "fix what the reason names, then run `amx daemon restart`; `amx daemon status` repeats the reason"
	case "daemon_export":
		return "run `amx daemon status`; if the export keeps failing, run `amx daemon restart`"
	case checkBuildMismatch:
		return "run `amx daemon restart` so the daemon runs the installed build"
	case "socket":
		if strings.Contains(f.Message, "installed amx binary is unavailable") {
			return "install amx on PATH (`just install` from a checkout), then run `amx daemon restart`"
		}
		return "run `amx daemon restart`"
	case "config_changed_since_daemon_start":
		return "run `amx daemon restart`, then `amx config daemon-status` to verify the settings were applied"

	// Notifications.
	case "notifications_unknown":
		return "fix config.toml (`amx config validate` lists every error), then run `amx daemon restart`"
	case "notifications_unreported":
		return "run `amx daemon restart` so the installed build reports notification capability"
	case "notifications_unavailable":
		return notificationRemediation(f.Message)

	// Screen patterns.
	case checkScreenPatternOverrideInvalid:
		return "correct or delete the named override file, then run `amx daemon restart` (docs/screen-classifier-health.md)"
	case checkScreenPersistentDecline:
		return "diff the reported status line against the shipped pattern file, install a corrected override, then run `amx daemon restart` (docs/screen-classifier-health.md)"

	// The agent-session-loop capability line.
	case "asl_starting":
		return "run `amx doctor` again in a few seconds"
	case "asl_unavailable":
		return "run `amx config asl-sync` to resolve and persist the asl path (it restarts the daemon and verifies the bridge)"
	case "asl_read_only":
		return "wait for a fresh snapshot; if the `asl:` line's snapshot age keeps growing, run `amx daemon restart` to replace the bridge"

	// Kitty.
	case "kitty":
		if strings.Contains(f.Message, "kitten @ ls failed") {
			return "confirm kitty.conf has `allow_remote_control yes` and a `listen_on` socket, then restart Kitty; or set [defaults] launch_backend = \"tmux\""
		}
		return "run doctor in a Kitty window to check it, or set [defaults] launch_backend = \"tmux\""
	case checkKittySnapshotStale:
		return "check that `kitten @ ls` answers; if it does, run `amx daemon restart`"
	case "renderer_drift":
		return "confirm `kitten @ ls` answers, then run `amx daemon restart`; render failures on a `fd:` socket mean a stale binary (`just install`, then `amx daemon restart`)"
	case checkCompanionBuildMismatch:
		return "rebuild or reinstall the app from the same checkout as amx (`just mac-install`)"

	// The tmux backend and the daemon's runtime sessions.
	case checkRuntimeReconcileFailing:
		return "run `amx backend status`: if the server answers there, run `amx daemon restart`; if it does not, follow that report's hint"
	case checkRuntimeReconcileStale:
		return "run `amx daemon restart` to restart the maintenance loop; `amx backend status` reads the server meanwhile"
	case checkScreenCaptureFailing:
		return "run `amx backend status`; if the server answers, `amx debug probe --runtime-session <id>` reads one session's screen, and `amx daemon restart` resets the daemon's readers"

	// Hooks and configuration.
	case "hooks":
		if strings.HasPrefix(f.Message, "hook status unavailable") {
			return "fix config.toml (`amx config validate` lists every error)"
		}
		if strings.HasPrefix(f.Message, "hook status binary unavailable") {
			return "install amx on PATH (`just install` from a checkout)"
		}
		return "run `amx config hooks-sync` (or `amx config hooks-sync PROFILE`), then `amx config hooks-status`"
	case "hooks_unverified":
		return "launch a session with the profile so its hooks fire, then run `amx doctor` again; `amx config hooks-status` shows activation per profile"
	case "hook_delivery_failures":
		return "keep the daemon running (`amx daemon status`); `amx debug tail --stage hook.delivery,hook.failure --after 1h` lists the dropped deliveries"
	case "project_directories":
		return "`amx project list --missing` lists them; fix each directory with `amx config edit`, or mark the project `archived = true`"
	case "native_logging":
		return "`amx debug status` shows native-log storage and coverage; set [defaults.diagnostics] native_logging = false to stop native logging"

	// Daemon invariants (shared with `amx daemon audit`).
	case checkASLActiveState, checkSubagentVisibility:
		return "inspect " + export + " for the facts behind the icon; then " + reportBug
	case checkShadowCurrentMismatch, checkRenderCausalBound:
		return reportBug
	case checkRenderGenerationDrift:
		return "a stuck terminal write is the usual cause: confirm the backend answers (the kitty and backend lines above); then " + reportBug
	case checkQueueSaturation:
		return "the counter is cumulative since the daemon started: find the burst with `amx debug tail` and `amx daemon audit --duration 10m`; `amx daemon restart` resets it"
	case checkActiveAfterTerminal:
		return "the source is still active after its terminal event: inspect " + export + " for the suppressed events, then check `amx config hooks-status` and resync with `amx config hooks-sync`"
	case checkOwnerMismatchPrompt, checkOwnerMismatchBurst, checkUnknownProducerFact:
		return "run `amx config hooks-status` and resync with `amx config hooks-sync`, then inspect " + export
	case checkAcknowledgedPressure:
		return "inspect shadow.facts[] in " + export + "; above 64 the daemon is not compacting, so run `amx daemon restart` from the installed build"
	case checkMalformedProcessIdentity:
		return "inspect shadow.facts[] in " + export + " and report it with `amx debug bundle --mode share`"
	case checkStopHoldStuck:
		return "inspect " + export + "; F8 clears the window's state icon"
	case checkHarnessDrift:
		return "the pane runs another harness than it was launched with: launch that harness from its own project, or inspect " + export
	}
	return ""
}

// notificationRemediation follows the reason the daemon's notifier gave
// (internal/daemon/notification resolveAlerter). The notifier is resolved
// once at daemon start, so every fix ends in a restart.
func notificationRemediation(message string) string {
	disable := "or set every [defaults.notifications] kind to false (permission, elicitation, stop, stop_failure, stale_active, hook_silence, error) to stop notifying"
	switch {
	case strings.HasSuffix(message, ": invalid_override"):
		return "set [defaults.notifications] alerter_path to the absolute path of an executable alerter, or remove it to search PATH and Homebrew/MacPorts, then run `amx daemon restart`; " + disable
	case strings.HasSuffix(message, ": unsupported_platform"):
		return "native notifications need macOS; " + disable
	default:
		return "install alerter 26 or newer (https://github.com/vjeantet/alerter) on PATH or in /opt/homebrew/bin, /usr/local/bin, or /opt/local/bin, or set [defaults.notifications] alerter_path, then run `amx daemon restart`; " + disable
	}
}

// attachDoctorRemediations fills the remediation of every finding that has
// none yet. A backend problem takes the hint `amx backend status` gives it,
// so the two reports advise one thing; an ASL dimension takes its
// dimension's remediation, which its message has always ended with.
func attachDoctorRemediations(report *doctorReport) {
	hints := make(map[string]string, len(report.Backend.Problems))
	for _, problem := range report.Backend.Problems {
		if problem.Hint != "" {
			hints[checkBackendPrefix+problem.Code] = problem.Hint
		}
	}
	aslRemedies := make(map[string]string, len(report.ASLIntegration.Dimensions))
	for _, dim := range report.ASLIntegration.Dimensions {
		if check := aslDimensionFinding[dim.Name]; check != "" && dim.Remediation != "" {
			aslRemedies[check] = dim.Remediation
		}
	}
	for i := range report.Findings {
		finding := &report.Findings[i]
		if finding.Remediation != "" || finding.Severity == severityInfo {
			continue
		}
		if hint, ok := hints[finding.Check]; ok {
			finding.Remediation = hint
		} else if remedy, ok := aslRemedies[finding.Check]; ok {
			finding.Remediation = remedy
		} else {
			finding.Remediation = doctorRemediation(*finding)
		}
	}
}

// remediationLine is the text report's line under a finding, or "" when the
// finding has no remediation or its message already says it.
func remediationLine(f doctorFinding) string {
	if f.Remediation == "" || strings.Contains(f.Message, f.Remediation) {
		return ""
	}
	return "  remediation: " + f.Remediation
}
