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
//
// v3 adds the `screenstate_persistent_decline` finding, raised when the screen
// classifier has refused the same unchanging frame on a live ⚡ window long
// enough that a harness UI change is the only ordinary explanation. Its
// severity is failure, which is exactly the case the policy below bumps for.
//
// v2 adds `daemon.asl_reconcile`, `daemon.asl_hook_delivery`, and the
// `asl_integration` block, and with it six new stable finding identifiers.
// The bump is not for the added fields — those are additive — but for the
// findings: a strict-mode consumer that treats every failure identifier it does
// not recognize as fatal would start failing on conditions a v1 daemon never
// reported. The two identifiers that predate it, `asl_unavailable` and
// `asl_read_only`, keep their exact names and meanings.
const doctorReportSchemaVersion = 3

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
	// ASLIntegration answers the six pairing dimensions separately. It is
	// always present, because "there is no integration configured" is one of
	// the answers rather than a reason to omit the block.
	ASLIntegration doctorASLIntegration `json:"asl_integration"`
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
	// ASLReconcile and ASLHookDelivery are the two directions of the pairing
	// that exist without a bridge. Both are omitted by a daemon that predates
	// them, which the integration block reports as unknown rather than zero.
	ASLReconcile    *daemon.ASLReconcileSnapshot    `json:"asl_reconcile,omitempty"`
	ASLHookDelivery *daemon.ASLHookDeliverySnapshot `json:"asl_hook_delivery,omitempty"`
	// ScreenPatterns reports the classifier's installed UI-copy pattern sources
	// and the warnings the startup load produced. Omitted by a daemon that
	// predates the block.
	ScreenPatterns *daemon.ScreenPatternsSnapshot `json:"screen_patterns,omitempty"`
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

		ASLReconcile:    health.ASLReconcile,
		ASLHookDelivery: health.ASLHookDelivery,
		ScreenPatterns:  health.ScreenPatterns,
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
		// Only meaningful from a live daemon: the sources reported here are what
		// the RUNNING process loaded at startup, which is precisely what a
		// re-read of the config directory by this CLI would fail to show.
		if err := writeDoctorScreenPatterns(textOut, health.ScreenPatterns, addFinding); err != nil {
			return err
		}
	}
	// The integration section runs whether or not the daemon answered: with the
	// daemon down, shell resolution is still answerable and is often the thing
	// the operator needs to see.
	report.ASLIntegration = evaluateASLIntegration(gatherASLIntegrationInputs(health))
	if err := writeDoctorASLIntegration(textOut, report.ASLIntegration, addFinding); err != nil {
		return err
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
