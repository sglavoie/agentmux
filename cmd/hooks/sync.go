package hooks

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/debugger"
	"github.com/sglavoie/agentmux/internal/hooks"
	"github.com/spf13/cobra"
)

// SyncCommand and StatusCommand return the two hook maintenance leaves. They
// are surfaced to the user under `amx config`, so the config command family
// registers them; the behavior itself belongs to this package because it is
// about hook files, not about configuration.
func SyncCommand() *cobra.Command { return configHooksSyncCmd }

// StatusCommand returns the `hooks-status` leaf. See SyncCommand.
func StatusCommand() *cobra.Command { return configHooksStatusCmd }

// hooksBinary is the seam tests swap to stand in for the running executable.
var hooksBinary = cli.AmxBinary

// hooksLookPath finds the installed amx, the one hook files are expected to
// name; tests swap it so the operator's own amx never decides a result.
var hooksLookPath = exec.LookPath

// hooksSyncThisBinary lets hooks-sync point every hook at a build other than
// the installed amx, which it otherwise refuses to do.
var hooksSyncThisBinary bool

func init() {
	configHooksSyncCmd.Flags().BoolVar(&hooksSyncThisBinary, "this-binary", false, "write hooks that run this amx even though it is not the amx on PATH")
}

// hooksStatusIsTerminal decides between the table for a person and the
// tab-separated rows for a pipe; tests swap it, since they cannot hand a
// command a terminal.
var hooksStatusIsTerminal = cli.IsTerminal

var configHooksSyncCmd = &cobra.Command{
	Use:               "hooks-sync [PROFILE]",
	Short:             "Sync Agent Mux hooks for configured harness profiles",
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: cli.CompleteProfileFirstArg,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := cli.LoadRunnableConfig()
		if err != nil {
			return err
		}
		bin, err := hooksBinary()
		if err != nil {
			return err
		}
		if err := cli.RefuseTransientBinary(bin, "write hook commands"); err != nil {
			return err
		}
		if installed, foreign := hooks.ExpectedBinary(bin, hooksLookPath); foreign && !hooksSyncThisBinary {
			return fmt.Errorf("refusing to write hook commands that run %s: it is not the amx on PATH (%s), which is the one hooks are expected to name; run `%s config hooks-sync`, or pass --this-binary to point every hook at this build", cli.DisplayPath(bin), cli.DisplayPath(installed), cli.DisplayPath(installed))
		}
		filter := ""
		if len(args) > 0 {
			filter = args[0]
		}
		results, err := hooks.MergeAll(cfg, filter, bin)
		if err != nil {
			return err
		}
		var failed []string
		for _, r := range results {
			if r.Err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  error (%s): %v\n", r.Account, r.Err)
				failed = append(failed, r.Account)
				continue
			}
			switch r.Status {
			case hooks.MergeCreated:
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  created  %s\n", r.Path)
			case hooks.MergeUpdated:
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  updated  %s\n", r.Path)
			case hooks.MergeSkipped:
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  skipped  %s (already up to date)\n", r.Path)
			}
		}
		// Surface partial-failure scenarios with a non-zero exit code so callers
		// (humans and CI) notice that one or more profiles' hooks were not
		// updated. Previously the per-account error was only printed to stderr
		// and the process exited 0, masking the failure.
		if len(failed) > 0 {
			return fmt.Errorf("hooks-sync failed for %s: %s", cli.CountOf(len(failed), "profile", "profiles"), strings.Join(failed, ", "))
		}
		return nil
	},
}

var configHooksStatusCmd = &cobra.Command{
	Use:   "hooks-status",
	Short: "Show hook sync and activation status per profile",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := cli.LoadRunnableConfig()
		if err != nil {
			return err
		}
		running, err := hooksBinary()
		if err != nil {
			return err
		}
		// A build other than the installed amx judges the installation, not
		// itself: against itself every hook file would read as drifted.
		bin, foreign := hooks.ExpectedBinary(running, hooksLookPath)
		report := debugger.InspectHooks(cfg, bin, time.Now())
		out := cmd.OutOrStdout()
		errOut := cmd.ErrOrStderr()
		rows := make([][]string, 0, len(report.Profiles))
		for _, s := range report.Profiles {
			if s.CheckError != "" {
				rows = append(rows, []string{s.Profile, s.Harness, "error", string(hooks.ActivationFailure), s.CheckError})
				continue
			}
			activation := string(s.Activation)
			if s.Unused {
				activation = "unused"
			}
			rows = append(rows, []string{s.Profile, s.Harness, string(s.Sync), activation, s.Detail()})
		}
		// A person gets an aligned table and the hint below it; a pipe gets
		// headerless tab-separated rows, as project, session, and worktree list
		// give it, with the hint on stderr so it is never read as a row.
		terminal := hooksStatusIsTerminal(out)
		hintOut := out
		if terminal {
			if err := cli.WriteTable(out, []string{"PROFILE", "HARNESS", "SYNC", "ACTIVATION", "DETAIL"}, rows); err != nil {
				return err
			}
		} else {
			hintOut = errOut
			for _, row := range rows {
				if _, err := fmt.Fprintln(out, strings.Join(row, "\t")); err != nil {
					return err
				}
			}
		}
		if report.Error != "" {
			if _, err := fmt.Fprintln(errOut); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(errOut, "error: %s\n", report.Error); err != nil {
				return err
			}
		}
		hints := hooksStatusHints(report.Profiles)
		if foreign {
			hints = append(hints, fmt.Sprintf("note: this is %s, so hook files were judged against the installed amx at %s.", cli.DisplayPath(running), cli.DisplayPath(bin)))
		}
		if len(hints) > 0 && terminal {
			if _, err := fmt.Fprintln(out); err != nil {
				return err
			}
		}
		for _, hint := range hints {
			if _, err := fmt.Fprintln(hintOut, hint); err != nil {
				return err
			}
		}
		if report.Error != "" {
			return fmt.Errorf("%s", report.Error)
		}
		return nil
	},
}

// hooksStatusHints names the remedy for each state some profile is actually
// in, so a table whose files are all in sync is never told to sync them. A row
// whose check failed gets no hint: its error is the message.
func hooksStatusHints(profiles []debugger.HookProfile) []string {
	var drifted, inactive, unverified bool
	for _, p := range profiles {
		switch {
		case p.CheckError != "":
		case p.Sync != hooks.SyncInSync:
			drifted = true
		case p.Activation == hooks.ActivationFailure:
			inactive = true
		case p.Verdict != debugger.VerdictHealthy:
			unverified = true
		}
	}
	var hints []string
	if drifted {
		hints = append(hints, "hint: run `amx config hooks-sync` to install the drifted or missing hook files.")
	}
	if inactive {
		hints = append(hints, "hint: hooks that are not active are installed but switched off; DETAIL names the setting, launch flag, or trust record responsible.")
	}
	if unverified {
		hints = append(hints, "hint: unverified hooks are installed but have not been seen working; a session started with that profile records the hook event that verifies them.")
	}
	return hints
}
