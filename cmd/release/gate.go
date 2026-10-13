// Package release holds the hidden `release-gate` operator command, which
// drives the paired ASL/Agent Mux release procedure implemented in
// internal/release.
package release

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/release"
	"github.com/spf13/cobra"
)

// This file exposes the paired ASL/Agent Mux release gate through a hidden
// Cobra command, following the same convention as tui-test: the command parses
// flags, delegates the entire procedure to internal/release, and translates the
// outcome into stable output plus a specific process exit code.
//
// It is hidden because it is not a user-facing feature. It is the release
// operator's procedure, normally reached through
// `just paired-release-gate asl_root=... signing_key=...`, and it has real side
// effects: it installs both programs, restarts the running daemon, and writes
// annotated tags. Nothing about a session, a transcript, or the dashboard goes
// through here.

var (
	releaseGateASLRoot     string
	releaseGateAgentMux    string
	releaseGateSigningKey  string
	releaseGateEvidenceDir string
	releaseGateTag         bool
	releaseGateForce       bool
	releaseGateJSON        bool
)

// Command returns the hidden `release-gate` command so the root command can
// register it. Root assembly is the only consumer.
func Command() *cobra.Command { return releaseGateCmd }

var releaseGateCmd = &cobra.Command{
	Use:   "release-gate",
	Short: "Run the paired agent-session-loop / Agent Mux release gate",
	Long: "release-gate runs the full paired release procedure over an Agent Mux " +
		"checkout and an agent-session-loop checkout: it requires both source " +
		"revisions to be clean, builds and hashes the exact artifacts, runs both " +
		"full suites plus lint, the paired bridge integration, the reliability " +
		"stress and crash/restart gates, installs the pair in the documented " +
		"order, restarts the real daemon, drives a real tmux cockpit smoke, and " +
		"writes a signed JSON evidence record.\n\n" +
		"It has real side effects on the machine that runs it. It is a release " +
		"operator's tool, not a diagnostic.",
	Hidden:        true,
	Args:          cobra.NoArgs,
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runReleaseGate(cmd)
	},
}

func runReleaseGate(cmd *cobra.Command) error {
	evidenceDir, err := releaseGateEvidenceDirectory()
	if err != nil {
		return &cli.ExitError{Code: cli.ExitInvalidInput, Err: err}
	}

	opts := release.Options{
		AgentMuxRoot: releaseGateAgentMux,
		ASLRoot:      config.ExpandTilde(strings.TrimSpace(releaseGateASLRoot)),
		SigningKey:   config.ExpandTilde(strings.TrimSpace(releaseGateSigningKey)),
		EvidenceDir:  evidenceDir,
		Tag:          releaseGateTag,
		Overwrite:    releaseGateForce,
		// Progress goes to stderr unconditionally so a `--json` run still shows
		// an operator which of a dozen multi-minute steps is currently running.
		Progress: cmd.ErrOrStderr(),
	}

	ev, runErr := release.Run(cmd.Context(), opts)
	if ev == nil {
		return &cli.ExitError{Code: cli.ExitInvalidInput, Err: runErr}
	}

	if err := writeReleaseGateResult(cmd, ev); err != nil {
		return err
	}
	if runErr == nil {
		return nil
	}
	// A pair that did not pass is a gate result, not a bad invocation: the
	// record was written and the exit code says which of the two happened.
	if errors.Is(runErr, release.ErrGateFailed) {
		return &cli.ExitError{Code: cli.ExitScenarioFailure, Err: runErr, Quiet: releaseGateJSON}
	}
	// A stranded pair tag is also a gate result rather than a bad invocation --
	// re-running unchanged will not help until the ref is cleared. It is never
	// quiet, even under --json: it is the one outcome that left durable state
	// outside the evidence directory, and it must not be reduced to an exit code
	// a script might ignore.
	if errors.Is(runErr, release.ErrPartialTagging) {
		return &cli.ExitError{Code: cli.ExitScenarioFailure, Err: runErr}
	}
	return &cli.ExitError{Code: cli.ExitInvalidInput, Err: runErr}
}

// releaseGateEvidenceDirectory resolves where the record is written. The flag
// wins, then the config, then the default under the Agent Mux data directory.
//
// A config that cannot be loaded is not fatal here. The release gate does not
// otherwise depend on the operator's configuration, and refusing to cut a
// release because an unrelated `[[projects]]` entry is malformed would be a
// coupling nobody asked for.
func releaseGateEvidenceDirectory() (string, error) {
	if dir := strings.TrimSpace(releaseGateEvidenceDir); dir != "" {
		return config.ExpandTilde(dir), nil
	}
	if cfg, err := config.LoadDefault(); err == nil {
		if dir := strings.TrimSpace(cfg.Defaults.Release.EvidenceDir); dir != "" {
			return config.ExpandTilde(dir), nil
		}
	}
	return release.DefaultEvidenceDir()
}

// writeReleaseGateResult emits the record itself under --json, and a compact
// per-step summary otherwise. Both go to stdout and both are written before the
// failure exit, so a failed gate always leaves the operator something to read
// without opening the evidence file.
func writeReleaseGateResult(cmd *cobra.Command, ev *release.Evidence) error {
	out := cmd.OutOrStdout()
	if releaseGateJSON {
		data, err := json.MarshalIndent(ev, "", "  ")
		if err != nil {
			return &cli.ExitError{Code: cli.ExitInvalidInput, Err: fmt.Errorf("release-gate: encoding result: %w", err)}
		}
		if _, err := out.Write(append(data, '\n')); err != nil {
			return err
		}
		return nil
	}

	_, _ = fmt.Fprintf(out, "pair %s (%s)\n", ev.PairID, ev.Result)
	for _, repo := range ev.Repositories {
		state := "clean"
		if !repo.Clean {
			state = fmt.Sprintf("dirty (%d paths)", len(repo.Dirty))
		}
		_, _ = fmt.Fprintf(out, "  %-9s %s %s\n", repo.Repo, shortRevision(repo.Revision), state)
	}
	for _, artifact := range ev.Artifacts {
		_, _ = fmt.Fprintf(out, "  artifact %-20s %s\n", artifact.Name, artifact.SHA256)
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
	for _, tag := range ev.Tags {
		_, _ = fmt.Fprintf(out, "  tagged %s %s\n", tag.Repo, tag.Tag)
	}
	writeReleaseGateTagFailure(out, ev.TagFailure)
	return nil
}

// writeReleaseGateTagFailure prints an incomplete tagging attempt.
//
// A rolled-back attempt is reported for completeness; a stranded tag is reported
// because the operator has to act on it. The recovery commands are printed
// verbatim so the action does not require reading the evidence file first --
// though the file has them too, which is what makes the state recoverable after
// this terminal is closed.
//
// The two kinds of leftover ref are printed differently on purpose. "clear it
// with" introduces a compare-and-delete the operator can paste unread: it is
// bound to the object this run created, so the worst it can do is refuse. A ref
// the run could not identify gets no such line -- it gets the read-only
// inspection and the thing that has to be established first, because a command
// under "clear it with" that deleted by name would be exactly as pasteable and
// would take whatever now sits under that name.
func writeReleaseGateTagFailure(out io.Writer, failure *release.TagFailure) {
	if failure == nil {
		return
	}
	_, _ = fmt.Fprintf(out, "  tagging did not complete: %s\n", failure.Error)
	for _, tag := range failure.RolledBack {
		_, _ = fmt.Fprintf(out, "  rolled back %s %s\n", tag.Repo, tag.Tag)
	}
	for _, tag := range failure.Stranded {
		_, _ = fmt.Fprintf(out, "  STRANDED %s %s (this pair is not released)\n", tag.Repo, tag.Tag)
	}
	if failure.RollbackError != "" {
		_, _ = fmt.Fprintf(out, "  rollback failed: %s\n", failure.RollbackError)
	}
	for _, command := range failure.Recovery {
		_, _ = fmt.Fprintf(out, "  clear it with: %s\n", command)
	}
	for _, tag := range failure.Unidentified {
		_, _ = fmt.Fprintf(out, "  UNIDENTIFIED %s %s: %s\n", tag.Repo, tag.Tag, tag.Reason)
		for _, command := range tag.Inspect {
			_, _ = fmt.Fprintf(out, "  inspect it with: %s\n", command)
		}
		_, _ = fmt.Fprintf(out, "  before removing it: %s\n", tag.Requirement)
	}
}

func shortRevision(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

func formatStepDuration(step release.StepResult) string {
	if step.Status == release.StatusNotRun {
		return "-"
	}
	return fmt.Sprintf("%.1fs", float64(step.DurationMS)/1000)
}

func init() {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	releaseGateCmd.Flags().StringVar(&releaseGateASLRoot, "asl-root", "", "agent-session-loop checkout to release with this one (required)")
	releaseGateCmd.Flags().StringVar(&releaseGateAgentMux, "amx-root", cwd, "Agent Mux checkout to release")
	releaseGateCmd.Flags().StringVar(&releaseGateSigningKey, "signing-key", "", "SSH private key used to sign the evidence record; omitted leaves it unsigned (and cannot be combined with --tag)")
	releaseGateCmd.Flags().StringVar(&releaseGateEvidenceDir, "evidence-dir", "", "where to write the evidence record (overrides [defaults.release] evidence_dir)")
	releaseGateCmd.Flags().BoolVar(&releaseGateTag, "tag", false, "write the annotated pair tag into both repositories after a passing run; requires --signing-key")
	releaseGateCmd.Flags().BoolVar(&releaseGateForce, "force", false, "replace an existing evidence record for the same pair")
	releaseGateCmd.Flags().BoolVar(&releaseGateJSON, "json", false, "emit the evidence record as JSON")
	_ = releaseGateCmd.MarkFlagRequired("asl-root")
}
