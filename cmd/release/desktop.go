package release

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/release"
	"github.com/spf13/cobra"
)

// The hidden `desktop-release-gate` command: the app's release procedure,
// normally reached through `just mac-release-gate`. It is a separate command
// from `release-gate` because it is a separate gate: it needs Xcode, which the
// paired gate must never need, and it installs, restarts, and tags nothing.
//
// Its exit statuses are the paired gate's, with one addition in meaning: a gate
// that had nothing to build or test with exits ExitScenarioFailure too. That is
// not a pass, and a caller that only reads the status must not take it for one;
// the record's "result" is what tells pending from failed.

var (
	desktopGateAgentMux    string
	desktopGateSigningKey  string
	desktopGateEvidenceDir string
	desktopGateNotarized   bool
	desktopGateRehearsal   bool
	desktopGateForce       bool
	desktopGateJSON        bool
)

// DesktopCommand returns the hidden `desktop-release-gate` command so the root
// command can register it.
func DesktopCommand() *cobra.Command { return desktopGateCmd }

var desktopGateCmd = &cobra.Command{
	Use:   "desktop-release-gate",
	Short: "Run the Agent Mux.app release gate",
	Long: "desktop-release-gate builds the Release Agent Mux.app from an Agent Mux " +
		"checkout, checks the bundle's seal, its tmux runtime, and that its amx is " +
		"the one the checkout built, runs the hosted suite against the Debug and " +
		"the Release app, and writes a JSON evidence record.\n\n" +
		"It needs Xcode, on this Mac or on the one AMX_MAC_TEST_HOST names. When " +
		"there is nothing to build with, the record says pending and the command " +
		"exits nonzero. It installs nothing and is separate from release-gate, " +
		"which never needs Xcode.",
	Hidden:        true,
	Args:          cobra.NoArgs,
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDesktopGate(cmd)
	},
}

func runDesktopGate(cmd *cobra.Command) error {
	evidenceDir := config.ExpandTilde(strings.TrimSpace(desktopGateEvidenceDir))
	if evidenceDir == "" {
		dir, err := releaseGateEvidenceDirectory()
		if err != nil {
			return &cli.ExitError{Code: cli.ExitInvalidInput, Err: err}
		}
		evidenceDir = dir
	}

	ev, runErr := release.RunDesktop(cmd.Context(), release.DesktopOptions{
		AgentMuxRoot: desktopGateAgentMux,
		Notarized:    desktopGateNotarized,
		Rehearsal:    desktopGateRehearsal,
		SigningKey:   config.ExpandTilde(strings.TrimSpace(desktopGateSigningKey)),
		EvidenceDir:  evidenceDir,
		Overwrite:    desktopGateForce,
		Progress:     cmd.ErrOrStderr(),
	})
	if ev == nil {
		return &cli.ExitError{Code: cli.ExitInvalidInput, Err: runErr}
	}
	if err := writeDesktopGateResult(cmd, ev); err != nil {
		return err
	}
	if runErr == nil {
		return nil
	}
	if errors.Is(runErr, release.ErrDesktopGateFailed) || errors.Is(runErr, release.ErrDesktopGatePending) {
		return &cli.ExitError{Code: cli.ExitScenarioFailure, Err: runErr, Quiet: desktopGateJSON}
	}
	return &cli.ExitError{Code: cli.ExitInvalidInput, Err: runErr}
}

func writeDesktopGateResult(cmd *cobra.Command, ev *release.DesktopEvidence) error {
	out := cmd.OutOrStdout()
	if desktopGateJSON {
		data, err := json.MarshalIndent(ev, "", "  ")
		if err != nil {
			return &cli.ExitError{Code: cli.ExitInvalidInput, Err: fmt.Errorf("desktop-release-gate: encoding result: %w", err)}
		}
		_, err = out.Write(append(data, '\n'))
		return err
	}

	kind := "release"
	if ev.Rehearsal {
		kind = "rehearsal"
	}
	_, _ = fmt.Fprintf(out, "desktop %s %s (%s)\n", kind, ev.RecordID, ev.Result)
	if ev.PendingReason != "" {
		_, _ = fmt.Fprintf(out, "  pending: %s\n", ev.PendingReason)
	}
	state := "clean"
	if !ev.Repository.Clean {
		state = fmt.Sprintf("dirty (%d paths), tree %s", len(ev.Repository.Dirty), shortRevision(ev.TreeSHA256))
	}
	_, _ = fmt.Fprintf(out, "  %-9s %s %s\n", ev.Repository.Repo, shortRevision(ev.Repository.Revision), state)
	for _, fact := range ev.Environment.Toolchain {
		_, _ = fmt.Fprintf(out, "  toolchain %-24s %s\n", fact.Name, fact.Value)
	}
	for _, fact := range ev.Bundle {
		_, _ = fmt.Fprintf(out, "  bundle %-27s %s\n", fact.Name, fact.Value)
	}
	for _, step := range ev.Steps {
		_, _ = fmt.Fprintf(out, "  %-8s %-20s %s\n", step.Status, step.Name, formatStepDuration(step))
	}
	if ev.Path != "" {
		_, _ = fmt.Fprintf(out, "  evidence %s\n", ev.Path)
	}
	if ev.Signature.Signed {
		_, _ = fmt.Fprintf(out, "  signature %s (namespace %s)\n", filepath.Base(ev.Signature.Path), ev.Signature.Namespace)
	} else {
		_, _ = fmt.Fprintln(out, "  signature none (unsigned record)")
	}
	return nil
}

func init() {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	desktopGateCmd.Flags().StringVar(&desktopGateAgentMux, "amx-root", cwd, "Agent Mux checkout whose app is released")
	desktopGateCmd.Flags().StringVar(&desktopGateSigningKey, "signing-key", "", "SSH private key used to sign the evidence record; omitted leaves it unsigned")
	desktopGateCmd.Flags().StringVar(&desktopGateEvidenceDir, "evidence-dir", "", "where to write the evidence record (overrides [defaults.release] evidence_dir)")
	desktopGateCmd.Flags().BoolVar(&desktopGateNotarized, "notarized", false, "require a Developer ID signature and a stapled notarization ticket")
	desktopGateCmd.Flags().BoolVar(&desktopGateRehearsal, "rehearsal", false, "run over uncommitted changes and mark the record as a rehearsal, not a release")
	desktopGateCmd.Flags().BoolVar(&desktopGateForce, "force", false, "replace an existing evidence record for the same revision")
	desktopGateCmd.Flags().BoolVar(&desktopGateJSON, "json", false, "emit the evidence record as JSON")
}
