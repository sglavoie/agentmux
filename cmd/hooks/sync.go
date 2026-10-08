package hooks

import (
	"fmt"
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
		bin, err := cli.AmxBinary()
		if err != nil {
			return err
		}
		report := debugger.InspectHooks(cfg, bin, time.Now())
		out := cmd.OutOrStdout()
		errOut := cmd.ErrOrStderr()
		needsAttention := false
		rows := make([][]string, 0, len(report.Profiles))
		for _, s := range report.Profiles {
			if s.CheckError != "" {
				rows = append(rows, []string{s.Profile, s.Harness, "error", string(hooks.ActivationFailure), s.CheckError})
				needsAttention = true
				continue
			}
			activation := string(s.Activation)
			if s.Unused {
				activation = "unused"
			}
			rows = append(rows, []string{s.Profile, s.Harness, string(s.Sync), activation, s.Detail()})
			if s.Verdict != debugger.VerdictHealthy {
				needsAttention = true
			}
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
		if needsAttention {
			if terminal {
				if _, err := fmt.Fprintln(out); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintln(hintOut, "hint: run `amx config hooks-sync` for drifted or missing files; activation details identify disabling settings, launch flags, and trust records, while CLI-version or last-seen gaps remain unverified."); err != nil {
				return err
			}
		}
		if report.Error != "" {
			return fmt.Errorf("%s", report.Error)
		}
		return nil
	},
}
