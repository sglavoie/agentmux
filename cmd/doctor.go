package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/debugger"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/spf13/cobra"
)

const doctorSuppressionWindow = 15 * time.Minute

// doctorReportSchemaVersion versions the `amx doctor --json` payload.
const doctorReportSchemaVersion = 1

// exitHealthFailure is the strict-mode exit status when any failure-severity
// finding is present (shared with `amx daemon audit`).
const exitHealthFailure = 2

var (
	newDoctorDaemonClient = func() daemonExportClient { return daemon.NewClient() }
	newDoctorKitty        = func() kittyWindowLister { return kitty.NewClient(kitty.NewRealRunner()) }
	doctorLoadConfig      = loadRunnableConfig
	doctorExecutable      = os.Executable
	doctorSocketPath      = daemon.SocketPath
	doctorStat            = os.Stat
	doctorNow             = time.Now
	doctorCLIBuild        = buildinfo.Current
	doctorDiagnosticPaths = diagnostics.DefaultPaths
)

var (
	doctorJSON   bool
	doctorStrict bool
)

// doctorReport is the structured `amx doctor --json` payload.
type doctorReport struct {
	SchemaVersion int                `json:"schema_version"`
	GeneratedAt   int64              `json:"generated_at"`
	Daemon        doctorDaemonStatus `json:"daemon"`
	CLIBuild      buildinfo.Info     `json:"cli_build"`
	Findings      []doctorFinding    `json:"findings"`
	// Healthy is true when no failure-severity finding is present; warnings
	// and informational findings do not affect it.
	Healthy bool `json:"healthy"`
}

type doctorDaemonStatus struct {
	Alive          bool                    `json:"alive"`
	Ready          bool                    `json:"ready"`
	DegradedReason string                  `json:"degraded_reason,omitempty"`
	PID            int                     `json:"pid,omitempty"`
	UptimeSeconds  int64                   `json:"uptime_seconds,omitempty"`
	Build          *buildinfo.Info         `json:"build,omitempty"`
	Metrics        *daemon.MetricsSnapshot `json:"metrics,omitempty"`
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Diagnose daemon, Kitty, hook, renderer, and invariant health",
	Args:  cobra.NoArgs,
	// A strict-mode health failure is a structured result (ExitError), not a
	// usage problem; Execute owns the stderr line and process status.
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE:          runDoctor,
}

func runDoctor(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	textOut := out
	if doctorJSON {
		textOut = io.Discard
	}

	now := doctorNow()
	report := doctorReport{
		SchemaVersion: doctorReportSchemaVersion,
		GeneratedAt:   now.Unix(),
		CLIBuild:      doctorCLIBuild(),
		Findings:      make([]doctorFinding, 0),
	}
	addFinding := func(severity, check string, format string, args ...any) {
		report.Findings = append(report.Findings, doctorFinding{
			Severity: severity,
			Check:    check,
			Message:  fmt.Sprintf(format, args...),
		})
	}
	reportIssue := func(check string) func(string, ...any) {
		return func(format string, args ...any) {
			addFinding(severityFailure, check, format, args...)
		}
	}

	if _, err := fmt.Fprintln(textOut, "Agent Mux doctor"); err != nil {
		return err
	}

	client := newDoctorDaemonClient()
	health := client.Health()
	report.Daemon = doctorDaemonStatus{
		Alive:          health.Alive,
		Ready:          health.Ready,
		DegradedReason: health.DegradedReason,
		PID:            health.PID,
		UptimeSeconds:  int64(health.Uptime / time.Second),
		Build:          health.Build,
		Metrics:        health.Metrics,
	}
	if health.Alive {
		if _, err := fmt.Fprintf(textOut, "daemon: running (pid %d, uptime %s)\n", health.PID, health.Uptime.Round(time.Second)); err != nil {
			return err
		}
		if !health.Ready {
			addFinding(severityWarning, "daemon_degraded", "daemon started degraded: %s", health.DegradedReason)
		}
	} else {
		if _, err := fmt.Fprintln(textOut, "daemon: unreachable"); err != nil {
			return err
		}
		addFinding(severityFailure, "daemon_unreachable", "daemon is unreachable")
	}

	if err := writeDoctorSocketStatus(textOut, reportIssue("socket")); err != nil {
		return err
	}

	var (
		snapshot daemon.EventsExport
		windows  []kitty.OSWindow
		kittyOK  bool
	)
	if health.Alive {
		var err error
		snapshot, err = client.ExportEvents(0)
		if err != nil {
			if _, writeErr := fmt.Fprintf(textOut, "daemon export: unavailable (%s)\n", err); writeErr != nil {
				return writeErr
			}
			addFinding(severityFailure, "daemon_export", "daemon export is unavailable: %s", err)
		} else if _, err := fmt.Fprintln(textOut, "daemon export: ok"); err != nil {
			return err
		}
	}

	var kittyErr error
	windows, kittyErr = newDoctorKitty().ListWindows()
	if kittyErr != nil {
		if _, err := fmt.Fprintf(textOut, "kitty: unavailable (%s)\n", kittyErr); err != nil {
			return err
		}
		addFinding(severityFailure, "kitty", "kitten @ ls failed: %s", kittyErr)
	} else {
		kittyOK = true
		if _, err := fmt.Fprintf(textOut, "kitty: ok (%d OS windows)\n", len(windows)); err != nil {
			return err
		}
	}
	if err := writeDoctorNativeLogs(textOut, windows, kittyOK, func(format string, args ...any) {
		addFinding(severityWarning, "native_logging", format, args...)
	}); err != nil {
		return err
	}

	if err := writeDoctorHooks(textOut, reportIssue("hooks"), func(format string, args ...any) {
		addFinding(severityWarning, "hooks_unverified", format, args...)
	}); err != nil {
		return err
	}

	var liveByID map[int]daemon.WindowLiveMetadata
	if kittyOK {
		liveByID = daemonExportLiveMetadata(windows, snapshot, 0)
	}
	if err := writeDoctorRenderer(textOut, snapshot, liveByID, kittyOK, reportIssue("renderer_drift")); err != nil {
		return err
	}
	if err := writeDoctorSuppressions(textOut, snapshot, now); err != nil {
		return err
	}

	if health.Alive {
		report.Findings = append(report.Findings,
			evaluateDaemonInvariants(snapshot, health, report.CLIBuild, liveByID, now)...)
	}

	if err := writeDoctorFindings(textOut, report.Findings); err != nil {
		return err
	}

	failures := countFindings(report.Findings, severityFailure)
	report.Healthy = failures == 0
	if report.Healthy {
		if _, err := fmt.Fprintln(textOut, "overall: clean"); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintln(textOut, "overall: issues detected"); err != nil {
		return err
	}

	if doctorJSON {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return fmt.Errorf("encode doctor report: %w", err)
		}
		data = append(data, '\n')
		if _, err := out.Write(data); err != nil {
			return err
		}
	}

	if doctorStrict && failures > 0 {
		return &ExitError{
			Code:  exitHealthFailure,
			Err:   fmt.Errorf("doctor: %d health failure(s) detected", failures),
			Quiet: doctorJSON,
		}
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

func init() {
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "Emit a structured JSON report instead of text")
	doctorCmd.Flags().BoolVar(&doctorStrict, "strict", false, "Exit nonzero when any health failure is detected")
	rootCmd.AddCommand(doctorCmd)
}
