package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/daemon"
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
	Alive          bool                           `json:"alive"`
	Ready          bool                           `json:"ready"`
	DegradedReason string                         `json:"degraded_reason,omitempty"`
	PID            int                            `json:"pid,omitempty"`
	UptimeSeconds  int64                          `json:"uptime_seconds,omitempty"`
	Build          *buildinfo.Info                `json:"build,omitempty"`
	Metrics        *daemon.MetricsSnapshot        `json:"metrics,omitempty"`
	Notification   *daemon.NotificationCapability `json:"notification,omitempty"`
	ASL            *aslbridge.Status              `json:"asl,omitempty"`
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
		Notification:   health.Notification,
		ASL:            health.ASL,
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
	if health.Alive {
		if err := writeDoctorNotificationCapability(textOut, health.Notification, addFinding); err != nil {
			return err
		}
		if err := writeDoctorASLCapability(textOut, health.ASL, addFinding); err != nil {
			return err
		}
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

func init() {
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "Emit a structured JSON report instead of text")
	doctorCmd.Flags().BoolVar(&doctorStrict, "strict", false, "Exit nonzero when any health failure is detected")
	rootCmd.AddCommand(doctorCmd)
}
