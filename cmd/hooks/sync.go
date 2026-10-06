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
		bin, err := cli.AmxBinary()
		if err != nil {
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
			return fmt.Errorf("hooks-sync failed for %d profile(s): %s", len(failed), strings.Join(failed, ", "))
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
		if _, err := fmt.Fprintf(out, "%-20s %-10s %-10s %-12s %s\n", "ACCOUNT", "HARNESS", "SYNC", "ACTIVATION", "DETAIL"); err != nil {
			return err
		}
		needsAttention := false
		for _, s := range report.Profiles {
			if s.CheckError != "" {
				if _, err := fmt.Fprintf(out, "%-20s %-10s %-10s %-12s %s\n", s.Profile, s.Harness, "error", hooks.ActivationFailure, s.CheckError); err != nil {
					return err
				}
				needsAttention = true
				continue
			}
			if _, err := fmt.Fprintf(out, "%-20s %-10s %-10s %-12s %s\n", s.Profile, s.Harness, s.Sync, s.Activation, s.Detail()); err != nil {
				return err
			}
			if s.Verdict != debugger.VerdictHealthy {
				needsAttention = true
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
			if _, err := fmt.Fprintln(out); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(out, "hint: run `amx config hooks-sync` for drifted or missing files; activation details identify disabling settings, launch flags, and trust records, while CLI-version or last-seen gaps remain unverified."); err != nil {
				return err
			}
		}
		if report.Error != "" {
			return fmt.Errorf("%s", report.Error)
		}
		return nil
	},
}
