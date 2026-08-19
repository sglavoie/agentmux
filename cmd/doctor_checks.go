package cmd

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/debugger"
	"github.com/sglavoie/agentmux/internal/kitty"
)

func writeDoctorNotificationCapability(out io.Writer, capability *daemon.NotificationCapability, addFinding func(string, string, string, ...any)) error {
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

func writeDoctorRenderer(out io.Writer, snapshot daemon.EventsExport, liveByID map[int]daemon.WindowLiveMetadata, kittyOK bool, reportIssue func(string, ...any)) error {
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

func writeDoctorSuppressions(out io.Writer, snapshot daemon.EventsExport, now time.Time) error {
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
