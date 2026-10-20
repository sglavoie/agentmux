package debug

// `amx daemon audit` samples the daemon's export snapshot on a fixed cadence
// for a bounded duration and evaluates the same correctness invariants as
// `amx doctor`. It exits nonzero when any failure-severity violation was
// observed in any sample, so CI and the install-restart smoke can gate on it.

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/spf13/cobra"
)

// auditSampleInterval is the fixed cadence between invariant samples.
const auditSampleInterval = 2 * time.Second

// auditReportSchemaVersion versions the `amx daemon audit --json` payload.
const auditReportSchemaVersion = 1

var (
	auditDuration time.Duration
	auditJSON     bool
	auditNow      = time.Now
	auditSleep    = time.Sleep
)

// auditReport is the structured audit outcome. Findings are deduplicated
// across samples (a violation persisting for the whole run appears once).
type auditReport struct {
	SchemaVersion int             `json:"schema_version"`
	StartedAt     int64           `json:"started_at"`
	FinishedAt    int64           `json:"finished_at"`
	Samples       int             `json:"samples"`
	Findings      []doctorFinding `json:"findings"`
	// Healthy is true when no failure-severity finding was observed.
	Healthy bool `json:"healthy"`
}

// AuditCommand returns the `audit` leaf so the daemon command family can
// register it under `amx daemon`. It lives here rather than with the rest of
// that family because it is a doctor surface wearing a daemon subcommand name:
// it evaluates the same invariants, reuses the same daemon/Kitty seams, and
// prints findings with the same writer.
func AuditCommand() *cobra.Command { return daemonAuditCmd }

var daemonAuditCmd = &cobra.Command{
	Use:   "audit",
	Short: "Sample daemon invariants for a duration; exit nonzero on violations",
	Long: `Audit the running daemon: every two seconds, capture the current-schema export
snapshot and evaluate the correctness invariants (active ASL work must be
visible, live subagents must show their icon, producer routes must not suppress
prompts or burst mismatches, fact retention must remain bounded, reducer and
renderer must agree, persisted process identities must be well formed, render
generations must not drift, queues must not saturate, and the daemon must run
the same artifact as this CLI). Any failure
observed in any sample makes the command exit nonzero.`,
	Example: `  amx daemon audit
  amx daemon audit --duration 2m
  amx daemon audit --duration 30s --json`,
	Args: cobra.NoArgs,
	// A violation exit is a structured result (cli.ExitError), not a usage problem;
	// Execute owns the stderr line and process status.
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE:          runDaemonAudit,
}

func runDaemonAudit(cmd *cobra.Command, _ []string) error {
	if auditDuration <= 0 {
		return fmt.Errorf("--duration must be > 0")
	}

	out := cmd.OutOrStdout()
	client := newDoctorDaemonClient()
	cliBuild := doctorCLIBuild()

	report := auditReport{
		SchemaVersion: auditReportSchemaVersion,
		StartedAt:     auditNow().Unix(),
		Findings:      make([]doctorFinding, 0),
	}
	seen := make(map[string]struct{})
	record := func(findings ...doctorFinding) {
		for _, finding := range findings {
			key := fmt.Sprintf("%s|%s|%d|%s", finding.Severity, finding.Check, finding.WindowID, finding.Message)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			report.Findings = append(report.Findings, finding)
		}
	}

	deadline := auditNow().Add(auditDuration)
	for {
		now := auditNow()
		health := client.Health()
		if !health.Alive {
			record(newDoctorFinding(severityFailure, "daemon_unreachable", 0, "daemon is unreachable"))
		} else {
			snapshot, err := client.ExportEvents(0)
			if err != nil {
				record(newDoctorFinding(severityFailure, "daemon_export", 0, fmt.Sprintf("daemon export is unavailable: %s", err)))
			} else {
				var liveByID map[int]protocol.WindowLiveMetadata
				if windows, kittyErr := newDoctorKitty().ListWindows(); kittyErr == nil {
					liveByID = ExportLiveMetadata(windows, snapshot, 0, doctorGetenv("KITTY_LISTEN_ON"))
				}
				record(evaluateDaemonInvariants(snapshot, health, cliBuild, liveByID, now)...)
			}
		}
		report.Samples++
		if !auditNow().Add(auditSampleInterval).After(deadline) {
			auditSleep(auditSampleInterval)
			continue
		}
		break
	}
	report.FinishedAt = auditNow().Unix()

	failures := countFindings(report.Findings, severityFailure)
	report.Healthy = failures == 0

	if auditJSON {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return fmt.Errorf("encode audit report: %w", err)
		}
		data = append(data, '\n')
		if _, err := out.Write(data); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(out, "Agent Mux audit: %d samples over %s\n", report.Samples, auditDuration); err != nil {
			return err
		}
		if err := writeDoctorFindings(out, report.Findings, true); err != nil {
			return err
		}
		verdict := "audit: clean"
		if failures > 0 {
			verdict = fmt.Sprintf("audit: %s", cli.CountOf(failures, "invariant violation", "invariant violations"))
		}
		if _, err := fmt.Fprintln(out, verdict); err != nil {
			return err
		}
	}

	if failures > 0 {
		return &cli.ExitError{
			Code:  cli.ExitHealthFailure,
			Err:   fmt.Errorf("audit: %s detected", cli.CountOf(failures, "invariant violation", "invariant violations")),
			Quiet: auditJSON,
		}
	}
	return nil
}

func init() {
	daemonAuditCmd.Flags().DurationVar(&auditDuration, "duration", 10*time.Second, "how long to sample (e.g. 30s, 2m)")
	cli.AddReportJSONFlag(daemonAuditCmd, &auditJSON, "the audit report")
}
