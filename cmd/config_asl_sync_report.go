package cmd

// The preflight checks, verification, and reporting that `amx config asl-sync`
// is assembled from. The command itself, and the ordered mutation it drives,
// are in config_asl_sync.go.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/aslcontract"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/spf13/cobra"
)

// aslSyncFailure carries a structured code alongside a failure that still has a
// report worth printing. A rollback is exactly that case: the command failed,
// and what it found on the way is the most useful thing an operator can read.
type aslSyncFailure struct {
	code string
	err  error
}

func applySurveyToReport(report *aslSyncReport, survey aslPairingReport) {
	report.ASLVersion = survey.ASLVersion
	report.ContractSetID = survey.ContractSetID
	report.BridgeProtocol = survey.BridgeProtocol
	report.StateRoot = survey.StateRoot
	report.Plans = survey.Plans
	report.ActiveIncompatibleRuns = survey.ActiveIncompatibleRuns
	report.Migratable = survey.Plans["migratable"]
}

// checkASLContract compares identity before anything else is believed. The
// contract set covers the bridge protocol, the lifecycle-hook schema, and the
// projection together, so a mismatch means the two halves disagree about all
// three and no amount of configuration fixes it.
func checkASLContract(survey aslPairingReport) error {
	if survey.ContractSetID != aslcontract.ContractSetID {
		return fmt.Errorf(
			"agent-session-loop speaks contract set %s; this build of amx speaks %s\n"+
				"remediation: upgrade whichever half is older so both report the same set",
			displayOrUnknown(survey.ContractSetID), aslcontract.ContractSetID)
	}
	if survey.BridgeProtocol != aslbridge.ProtocolVersion {
		return fmt.Errorf(
			"agent-session-loop serves bridge protocol %d; this build of amx speaks %d\n"+
				"remediation: upgrade whichever half is older",
			survey.BridgeProtocol, aslbridge.ProtocolVersion)
	}
	return nil
}

func displayOrUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "an unnamed set"
	}
	return value
}

// activeRunRefusal renders the refusal with every blocking run named. The
// identities are what make the message actionable: an operator can only stop a
// process they can find.
func activeRunRefusal(survey aslPairingReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d active agent-session-loop run(s) still carry state this build cannot pair with; "+
		"nothing was migrated and nothing was stopped", len(survey.ActiveIncompatibleRuns))
	for _, run := range survey.ActiveIncompatibleRuns {
		fmt.Fprintf(&b, "\n  - pid %d  run %s\n    plan %s\n    %s",
			run.PID, run.RunID, run.PlanDir, run.Detail)
	}
	b.WriteString("\nremediation: let these runs finish or stop them, then re-run `amx config asl-sync`")
	return b.String()
}

func readConfigBytes(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		return data, true, nil
	case os.IsNotExist(err):
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("reading %s: %w", path, err)
	}
}

// waitForPairedDaemon polls until the restarted daemon is ready, connected to
// the executable that was just persisted, and past the handshake.
//
// The executable comparison is the whole point of the wait. A daemon that came
// up ready with a bridge on a *different* path is not a paired daemon: it found
// something else, and accepting that would make the command's promise false in
// exactly the situation it was written for.
func waitForPairedDaemon(client aslSyncDaemon, executable string) (aslSyncVerification, error) {
	deadline := aslSyncNow().Add(aslSyncReadyTimeout)
	var last aslSyncVerification
	for {
		health := client.Health()
		last = aslSyncVerification{Ready: health.Alive && health.Ready}
		if health.ASL != nil {
			last.Available = health.ASL.Available
			last.MutationsEnabled = health.ASL.MutationsEnabled
			last.Executable = health.ASL.Executable
			last.ExecutableSource = health.ASL.ExecutableSource
			last.ContractSetID = health.ASL.ContractSetID
			last.Reason = health.ASL.Reason
		}
		if last.Ready && last.Available && last.MutationsEnabled && last.Executable == executable {
			return last, nil
		}
		if !aslSyncNow().Before(deadline) {
			return last, aslSyncVerificationError(last, executable)
		}
		aslSyncSleep(250 * time.Millisecond)
	}
}

// aslSyncVerificationError names the first thing that did not converge, in the
// order the operator would have to fix them.
func aslSyncVerificationError(v aslSyncVerification, executable string) error {
	switch {
	case !v.Ready:
		return errors.New("the daemon did not become ready after the restart")
	case !v.Available && v.Reason != "":
		return fmt.Errorf("the daemon restarted but could not reach agent-session-loop: %s", v.Reason)
	case !v.Available:
		return errors.New("the daemon restarted but never connected to agent-session-loop")
	case v.Executable != executable:
		return fmt.Errorf("the daemon paired with %s, not the %s that was persisted",
			displayOrUnknown(v.Executable), executable)
	default:
		return errors.New("the daemon connected to agent-session-loop but never completed a handshake")
	}
}

func emitASLSyncReport(cmd *cobra.Command, jsonOut bool, report aslSyncReport, failure *aslSyncFailure) error {
	if jsonOut {
		if failure != nil {
			// The report is durable evidence of a mutation that happened and was
			// undone, so it is emitted rather than replaced by the error alone.
			if err := cli.WriteJSON(cmd, report); err != nil {
				return err
			}
			return &cli.ExitError{Code: 1, Err: failure.err, Quiet: true}
		}
		return cli.WriteJSON(cmd, report)
	}
	if err := writeASLSyncText(cmd.OutOrStdout(), report); err != nil {
		return err
	}
	if failure != nil {
		return failure.err
	}
	return nil
}

func writeASLSyncText(out io.Writer, report aslSyncReport) error {
	lines := []string{
		fmt.Sprintf("executable  %s (%s)", report.Executable, report.ExecutableSource),
		fmt.Sprintf("contract    %s, bridge protocol %d, asl %s",
			report.ContractSetID, report.BridgeProtocol, report.ASLVersion),
		fmt.Sprintf("state root  %s", report.StateRoot),
	}
	if len(report.Plans) > 0 {
		lines = append(lines, fmt.Sprintf("plans       %d total, %d compatible, %d migratable",
			report.Plans["total"], report.Plans["compatible"], report.Plans["migratable"]))
	}
	if report.Mode == "check" {
		lines = append(lines,
			fmt.Sprintf("daemon      %s", reachableWord(report.DaemonReachable)),
			"",
			fmt.Sprintf("check only: %s is unchanged", report.ConfigPath),
		)
		if report.Migratable > 0 {
			lines = append(lines, fmt.Sprintf("            an apply would migrate %d inactive run(s)", report.Migratable))
		}
	} else {
		if report.Migrated > 0 {
			lines = append(lines, fmt.Sprintf("migrated    %d inactive run(s)", report.Migrated))
		}
		switch {
		case report.RolledBack:
			lines = append(lines, fmt.Sprintf("config      %s restored to its previous contents", report.ConfigPath))
		case report.Applied:
			lines = append(lines, fmt.Sprintf("config      %s updated", report.ConfigPath))
		}
		if v := report.Verification; v != nil && v.Available && v.MutationsEnabled {
			lines = append(lines, "bridge      live and handshaked")
		}
	}
	_, err := fmt.Fprintln(out, strings.Join(lines, "\n"))
	return err
}

func reachableWord(reachable bool) string {
	if reachable {
		return "reachable"
	}
	return "not running (the restart will start one)"
}

// runASLPairingProbe runs one pairing survey and decodes it.
//
// stdout and stderr are captured separately because the survey writes its JSON
// to stdout and its diagnostics to stderr; merging them would make a warning
// indistinguishable from a corrupt document. A nonzero exit is not itself a
// failure here — the survey exits nonzero precisely when it has something to
// report, such as an active incompatible run — so the document is decoded first
// and the exit status consulted only when there is nothing to read.
func runASLPairingProbe(ctx context.Context, executable string, migrate bool, timeout time.Duration) (aslPairingReport, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{"agent-mux", "pairing", "--json"}
	if migrate {
		args = append(args, "--migrate")
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	var report aslPairingReport
	if decodeErr := json.Unmarshal([]byte(stdout.String()), &report); decodeErr != nil {
		if ctx.Err() != nil {
			return report, fmt.Errorf("the pairing survey did not finish within %s", timeout)
		}
		if runErr != nil {
			return report, fmt.Errorf("%w: %s", runErr, firstLine(stderr.String()))
		}
		return report, fmt.Errorf("the pairing survey emitted no readable JSON: %w", decodeErr)
	}
	return report, nil
}

func firstLine(text string) string {
	trimmed := strings.TrimSpace(text)
	if idx := strings.IndexByte(trimmed, '\n'); idx >= 0 {
		return trimmed[:idx]
	}
	if trimmed == "" {
		return "no diagnostics"
	}
	return trimmed
}
