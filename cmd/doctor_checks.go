package cmd

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/debugger"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/screenstate"
)

func writeDoctorNotificationCapability(out io.Writer, capability *protocol.NotificationCapability, addFinding func(string, string, string, ...any)) error {
	if capability == nil {
		cfg, err := doctorLoadConfig()
		if err != nil {
			if _, writeErr := fmt.Fprintf(out, "notifications: unknown (older daemon; config unavailable: %s)\n", err); writeErr != nil {
				return writeErr
			}
			addFinding(severityWarning, "notifications_unknown", "notification capability is unreported and config cannot be loaded: %s", err)
			return nil
		}
		if _, err := fmt.Fprintln(out, "notifications: unknown (older daemon; restart required)"); err != nil {
			return err
		}
		if cfg.Defaults.Notifications.AnyEnabled() {
			addFinding(severityFailure, "notifications_unreported", "running daemon does not report notification capability; restart it before relying on click-to-focus")
		}
		return nil
	}

	state := "unavailable"
	if !capability.Enabled {
		state = "disabled"
	} else if capability.Available && capability.Interactive {
		state = "ready"
	}
	detail := capability.Backend
	if capability.Version != "" {
		detail += " " + capability.Version
	}
	if capability.Executable != "" {
		detail += " at " + capability.Executable
	}
	if capability.Reason != "" && capability.Reason != "disabled" {
		detail += "; " + capability.Reason
	}
	if _, err := fmt.Fprintf(out, "notifications: %s (%s)\n", state, detail); err != nil {
		return err
	}
	if capability.Enabled && (!capability.Available || !capability.Interactive) {
		addFinding(severityFailure, "notifications_unavailable", "interactive notifications are enabled but unavailable: %s", capability.Reason)
	}
	return nil
}

// checkScreenPatternOverrideInvalid is the stable identifier for a screen
// pattern override the daemon could not use. It is WARNING severity on
// purpose: the daemon kept classifying with the embedded default, so this is
// degraded-but-working and must not trip `--strict` on its own. That severity
// choice is also why this identifier required no doctorReportSchemaVersion bump
// of its own — see the policy comment on that constant, which bumps for new
// failure identifiers because a strict consumer treats an unknown failure as
// fatal.
const checkScreenPatternOverrideInvalid = "screenstate_override_invalid"

// writeDoctorScreenPatterns reports which UI-copy pattern set the classifier is
// actually running per pattern set, and raises a warning for any override the
// daemon declined. The active source matters on its own: a stale override that
// still parses is the most likely explanation for a classifier that silently
// stopped matching, and it is invisible everywhere else.
//
// A pattern set's name is not always a harness — internal/contextlimit owns one
// under the name "contextlimit" — so this reports "pattern sets" rather than
// claiming each name is a harness.
func writeDoctorScreenPatterns(out io.Writer, patterns *protocol.ScreenPatternsSnapshot, addFinding func(string, string, string, ...any)) error {
	if patterns == nil {
		_, err := fmt.Fprintln(out, "screen patterns: unreported (older daemon; restart required)")
		return err
	}
	if len(patterns.Sources) == 0 {
		if _, err := fmt.Fprintln(out, "screen patterns: none installed"); err != nil {
			return err
		}
	} else {
		names := make([]string, 0, len(patterns.Sources))
		for name := range patterns.Sources {
			names = append(names, name)
		}
		sort.Strings(names)
		if _, err := fmt.Fprintln(out, "screen patterns:"); err != nil {
			return err
		}
		for _, name := range names {
			source := patterns.Sources[name]
			// Anything that is not the embedded default is a file on disk, so
			// print the path: "override" alone would leave the operator hunting
			// for which file is in force.
			if source == screenstate.SourceEmbedded {
				if _, err := fmt.Fprintf(out, "- %s: embedded\n", name); err != nil {
					return err
				}
				continue
			}
			if _, err := fmt.Fprintf(out, "- %s: override (%s)\n", name, source); err != nil {
				return err
			}
		}
	}
	for _, warning := range patterns.Warnings {
		// override_applied is not a problem — it is the source line above, said
		// again as an event. Every other kind means the daemon fell back to the
		// embedded copy for a reason the operator needs to fix.
		if warning.Kind == string(screenstate.WarnOverrideApplied) {
			continue
		}
		detail := warning.Kind
		if warning.Harness != "" {
			detail += " for " + warning.Harness
		}
		if warning.Path != "" {
			detail += " at " + warning.Path
		}
		if warning.Detail != "" {
			detail += ": " + warning.Detail
		}
		if _, err := fmt.Fprintf(out, "- pattern load warning: %s\n", detail); err != nil {
			return err
		}
		addFinding(severityWarning, checkScreenPatternOverrideInvalid,
			"screen pattern override was not applied; embedded patterns are in force (%s)", detail)
	}
	return nil
}

// writeDoctorASLCapability reports the optional agent-session-loop
// integration. An absent or unavailable bridge is a failure only when the
// operator required it: in auto mode a missing ASL is a supported
// configuration, not a health problem.
func writeDoctorASLCapability(out io.Writer, status *aslbridge.Status, addFinding func(string, string, string, ...any)) error {
	if status == nil {
		if _, err := fmt.Fprintln(out, "asl: unreported (older daemon)"); err != nil {
			return err
		}
		return nil
	}
	if _, err := fmt.Fprintf(out, "asl: %s\n", aslStatusLine(status)); err != nil {
		return err
	}
	if status.Mode == string(aslbridge.ModeRequired) && !status.Available {
		addFinding(severityFailure, "asl_unavailable", "asl integration is required but unavailable: %s", status.Reason)
	}
	if status.Available && !status.MutationsEnabled {
		addFinding(severityWarning, "asl_read_only", "asl integration is connected but actions are disabled pending a fresh snapshot")
	}
	return nil
}

func writeDoctorNativeLogs(out io.Writer, windows []kitty.OSWindow, kittyOK bool, reportWarning func(string, ...any)) error {
	cfg, err := doctorLoadConfig()
	if err != nil {
		if _, writeErr := fmt.Fprintf(out, "native logs: config unavailable (%s)\n", err); writeErr != nil {
			return writeErr
		}
		reportWarning("native logging status unavailable: %s", err)
		return nil
	}
	policy := cfg.Defaults.Diagnostics
	if !policy.NativeLoggingValue() {
		_, err := fmt.Fprintln(out, "native logs: disabled by defaults.diagnostics.native_logging")
		return err
	}
	paths, err := doctorDiagnosticPaths()
	if err != nil {
		if _, writeErr := fmt.Fprintf(out, "native logs: state path unavailable (%s)\n", err); writeErr != nil {
			return writeErr
		}
		reportWarning("native logging state path unavailable: %s", err)
		return nil
	}
	native := debugger.InspectNative(paths, policy, windows, kittyOK, debugger.Selector{}, doctorNow())
	if native.Error != "" {
		if _, writeErr := fmt.Fprintf(out, "native logs: unavailable (%s)\n", native.Error); writeErr != nil {
			return writeErr
		}
		reportWarning("native log storage cannot be inspected: %s", native.Error)
		return nil
	}
	if _, err := fmt.Fprintf(out, "native logs: %s (%s across %d launches; %d active)\n",
		paths.NativeDir, formatDoctorBytes(native.Storage.TotalBytes), native.Storage.Launches, native.Storage.ActiveLaunches); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, "native privacy: raw local logs; managed directories are 0700 and files are created with umask 077; bundles redact them"); err != nil {
		return err
	}

	if !kittyOK {
		if _, err := fmt.Fprintln(out, "native coverage: unavailable (Kitty is unreachable)"); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(out, "native coverage: managed=%d external=%d degraded=%d disabled=%d unmanaged=%d\n",
			native.Coverage.Managed, native.Coverage.External, native.Coverage.Degraded, native.Coverage.Disabled, native.Coverage.Unmanaged); err != nil {
			return err
		}
		if native.Coverage.Degraded > 0 {
			reportWarning("%d managed session(s) have degraded native logging controls", native.Coverage.Degraded)
		}
		if native.Coverage.Unmanaged > 0 {
			reportWarning("%d harness session(s) are externally launched or lack Agent Mux native logging metadata; coverage cannot be guaranteed", native.Coverage.Unmanaged)
		}
	}
	if native.Storage.ActiveExcessBytes > 0 {
		reportWarning("active native log writers exceed per-launch targets by %s; pruning is deferred until exit", formatDoctorBytes(native.Storage.ActiveExcessBytes))
	}
	if native.Storage.ExcessBytes > 0 {
		reportWarning("native log storage exceeds the aggregate target by %s; active launches remain protected", formatDoctorBytes(native.Storage.ExcessBytes))
	}
	if len(native.Storage.Errors) > 0 {
		reportWarning("native log storage has %d unreadable or unexpected entries", len(native.Storage.Errors))
	}
	return nil
}

func formatDoctorBytes(value int64) string {
	const mib = int64(1 << 20)
	if value < mib {
		return fmt.Sprintf("%d B", value)
	}
	return fmt.Sprintf("%.1f MiB", float64(value)/float64(mib))
}

// writeDoctorFindings renders the structured findings grouped by severity.
func writeDoctorFindings(out io.Writer, findings []doctorFinding) error {
	for _, group := range []struct {
		severity string
		heading  string
	}{
		{severityFailure, "health failures:"},
		{severityWarning, "warnings:"},
		{severityInfo, "notes:"},
	} {
		if countFindings(findings, group.severity) == 0 {
			continue
		}
		if _, err := fmt.Fprintln(out, group.heading); err != nil {
			return err
		}
		for _, finding := range findings {
			if finding.Severity != group.severity {
				continue
			}
			location := ""
			if finding.WindowID != 0 {
				location = fmt.Sprintf("window %d: ", finding.WindowID)
			}
			if _, err := fmt.Fprintf(out, "- [%s] %s%s\n", finding.Check, location, finding.Message); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeDoctorSocketStatus(out io.Writer, reportIssue func(string, ...any)) error {
	socketPath := doctorSocketPath()
	socketInfo, socketErr := doctorStat(socketPath)
	if socketErr != nil {
		_, err := fmt.Fprintf(out, "socket: unavailable (%s)\n", socketErr)
		reportIssue("daemon socket is unavailable: %s", socketErr)
		return err
	}

	binary, binaryErr := doctorExecutable()
	if binaryErr != nil {
		_, err := fmt.Fprintf(out, "socket: %s (binary mtime unavailable: %s)\n", socketPath, binaryErr)
		reportIssue("daemon binary mtime is unavailable: %s", binaryErr)
		return err
	}
	binaryInfo, binaryStatErr := doctorStat(binary)
	if binaryStatErr != nil {
		_, err := fmt.Fprintf(out, "socket: %s (binary mtime unavailable: %s)\n", socketPath, binaryStatErr)
		reportIssue("daemon binary mtime is unavailable: %s", binaryStatErr)
		return err
	}
	state := "current"
	if binaryInfo.ModTime().After(socketInfo.ModTime()) {
		state = "older than binary; restart daemon"
		reportIssue("daemon socket is older than the installed binary")
	}
	_, err := fmt.Fprintf(out, "socket: %s (%s)\n", socketPath, state)
	return err
}

func writeDoctorHooks(out io.Writer, reportIssue func(string, ...any), reportUnverified func(string, ...any)) error {
	cfg, err := doctorLoadConfig()
	if err != nil {
		if _, writeErr := fmt.Fprintf(out, "hooks: config unavailable (%s)\n", err); writeErr != nil {
			return writeErr
		}
		reportIssue("hook status unavailable: %s", err)
		return nil
	}
	binary, err := doctorExecutable()
	if err != nil {
		if _, writeErr := fmt.Fprintf(out, "hooks: binary unavailable (%s)\n", err); writeErr != nil {
			return writeErr
		}
		reportIssue("hook status binary unavailable: %s", err)
		return nil
	}
	report := debugger.InspectHooks(cfg, binary, doctorNow())
	if len(report.Profiles) == 0 {
		if _, err := fmt.Fprintln(out, "hooks: no configured profiles"); err != nil {
			return err
		}
	}
	for _, status := range report.Profiles {
		if status.CheckError != "" {
			if _, err := fmt.Fprintf(out, "hooks: %s (%s): error (%s)\n", status.Profile, status.Harness, status.CheckError); err != nil {
				return err
			}
			reportIssue("hooks for %s cannot be checked: %s", status.Profile, status.CheckError)
			continue
		}
		if _, err := fmt.Fprintf(out, "hooks: %s (%s): sync=%s activation=%s\n", status.Profile, status.Harness, status.Sync, status.Activation); err != nil {
			return err
		}
		switch status.Verdict {
		case debugger.VerdictFailure:
			reportIssue("hooks for %s require attention: %s", status.Profile, status.Detail())
		case debugger.VerdictWarning:
			reportUnverified("hooks for %s are unverified: %s", status.Profile, status.Detail())
		}
	}
	if failures := report.DeliveryFailures; failures != nil && failures.Count > 0 {
		if _, err := fmt.Fprintf(out, "hooks: %d delivery failure(s) recorded in the trailing hour\n", failures.Count); err != nil {
			return err
		}
		reportUnverified("%d hook delivery failure(s) recorded in the trailing hour; deliveries during daemon restarts are dropped", failures.Count)
	}
	if report.Error != "" {
		reportIssue("hook status errors: %s", report.Error)
	}
	return nil
}

func writeDoctorRenderer(out io.Writer, snapshot protocol.EventsExport, liveByID map[int]protocol.WindowLiveMetadata, kittyOK bool, reportIssue func(string, ...any)) error {
	sockets := make(map[string]struct{})
	ids := daemonExportWindowIDs(snapshot, 0)
	for _, id := range ids {
		if socket := strings.TrimSpace(snapshot.WindowMetadata[id].Renderer.KittyListenOn); socket != "" {
			sockets[socket] = struct{}{}
		}
	}
	if len(sockets) == 0 {
		if _, err := fmt.Fprintln(out, "kitty sockets: none reported"); err != nil {
			return err
		}
	} else {
		values := make([]string, 0, len(sockets))
		for socket := range sockets {
			values = append(values, socket)
		}
		sort.Strings(values)
		if _, err := fmt.Fprintf(out, "kitty sockets: %s\n", strings.Join(values, ", ")); err != nil {
			return err
		}
	}

	if !kittyOK {
		_, err := fmt.Fprintln(out, "renderer drift: unavailable (Kitty is unreachable)")
		return err
	}
	type tabKey struct {
		osWindowID int
		tabID      int
	}
	// Kitty exposes one title per tab, not per pane. When multiple exported
	// windows share a tab, independent renderer states can legitimately differ;
	// the visible title is converged if it matches any asserted pane state.
	convergedTabs := make(map[tabKey]bool)
	for _, id := range ids {
		renderer := snapshot.WindowMetadata[id].Renderer
		live := liveByID[id]
		if !live.Found || !renderer.StateSet && !renderer.LoopingSet {
			continue
		}
		stateMatches := !renderer.StateSet || renderer.DesiredIcon == live.IconState
		loopingMatches := !renderer.LoopingSet || renderer.Looping == live.Looping
		if stateMatches && loopingMatches {
			convergedTabs[tabKey{osWindowID: live.OSWindowID, tabID: live.TabID}] = true
		}
	}
	drift := make([]string, 0)
	for _, id := range ids {
		renderer := snapshot.WindowMetadata[id].Renderer
		live := liveByID[id]
		if !live.Found {
			// A tombstoned window was garbage-collected by the daemon after
			// Kitty stopped reporting it; its metadata is retained purely for
			// post-mortem export and never counts as renderer drift.
			if snapshot.WindowMetadata[id].Watchdog.ClosedAt != 0 {
				continue
			}
			if (renderer.StateSet && renderer.DesiredIcon != "") || (renderer.LoopingSet && renderer.Looping) {
				drift = append(drift, fmt.Sprintf("window %d is no longer live", id))
			}
			continue
		}
		tabConverged := convergedTabs[tabKey{osWindowID: live.OSWindowID, tabID: live.TabID}]
		if !tabConverged && ((renderer.StateSet && renderer.DesiredIcon != live.IconState) || (renderer.LoopingSet && renderer.Looping != live.Looping)) {
			drift = append(drift, fmt.Sprintf("window %d wants icon=%q looping=%t; has icon=%q looping=%t", id, renderer.DesiredIcon, renderer.Looping, live.IconState, live.Looping))
		}
		if renderer.RenderFailures > 0 {
			detail := fmt.Sprintf("window %d has %d consecutive render failures", id, renderer.RenderFailures)
			if renderer.LastRenderError != "" {
				detail += ": " + renderer.LastRenderError
			}
			drift = append(drift, detail)
		}
	}
	if len(drift) == 0 {
		if _, err := fmt.Fprintln(out, "renderer drift: none"); err != nil {
			return err
		}
		return nil
	}
	if _, err := fmt.Fprintln(out, "renderer drift:"); err != nil {
		return err
	}
	for _, detail := range drift {
		if _, err := fmt.Fprintf(out, "- %s\n", detail); err != nil {
			return err
		}
		reportIssue("renderer drift: %s", detail)
	}
	return nil
}

func writeDoctorSuppressions(out io.Writer, snapshot protocol.EventsExport, now time.Time) error {
	cutoff := now.Add(-doctorSuppressionWindow).Unix()
	counts := make(map[string]int)
	for _, events := range snapshot.EventsByWindow {
		for _, event := range events {
			if event.At >= cutoff && event.Suppressed && event.SuppressionReason != "" {
				counts[event.SuppressionReason]++
			}
		}
	}
	if len(counts) == 0 {
		_, err := fmt.Fprintf(out, "recent suppressions (last %s): none\n", doctorSuppressionWindow)
		return err
	}
	reasons := make([]string, 0, len(counts))
	for reason := range counts {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	if _, err := fmt.Fprintf(out, "recent suppressions (last %s):\n", doctorSuppressionWindow); err != nil {
		return err
	}
	for _, reason := range reasons {
		if _, err := fmt.Fprintf(out, "- %s: %d\n", reason, counts[reason]); err != nil {
			return err
		}
	}
	return nil
}
