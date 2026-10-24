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

// hooksStatusJSON switches hooks-status to the schema-v1 envelope.
var hooksStatusJSON bool

// hooksSyncJSON is hooks-sync's --json.
var hooksSyncJSON bool

func init() {
	configHooksSyncCmd.Flags().BoolVar(&hooksSyncThisBinary, "this-binary", false, "write hooks that run this amx even though it is not the amx on PATH")
	cli.AddJSONFlag(configHooksStatusCmd, &hooksStatusJSON)
	cli.AddJSONFlag(configHooksSyncCmd, &hooksSyncJSON)
}

// hooksStatusData is the hooks-status --json payload: the report doctor also
// reads, the remedies a person would be shown, and which amx hook files were
// judged against.
type hooksStatusData struct {
	debugger.HookReport
	JudgedBinary string   `json:"judged_binary"`
	Foreign      bool     `json:"foreign"`
	Hints        []string `json:"hints"`
}

// hooksSyncProfile is one profile's result in the `hooks-sync --json` data:
// status is created, updated, or skipped (already up to date), or "error"
// with the error that stopped it.
type hooksSyncProfile struct {
	Profile string `json:"profile"`
	Path    string `json:"path,omitempty"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
}

// hooksSyncData is the `hooks-sync --json` payload. Binary is the amx every
// written hook runs.
type hooksSyncData struct {
	Binary   string             `json:"binary"`
	Profiles []hooksSyncProfile `json:"profiles"`
}

// writeHooksSyncJSON reports every profile's result. A profile that failed
// keeps the others' results: the data is written with a nonzero quiet exit,
// as the text mode exits nonzero.
func writeHooksSyncJSON(cmd *cobra.Command, bin string, results []hooks.MergeResult) error {
	data := hooksSyncData{Binary: bin, Profiles: make([]hooksSyncProfile, 0, len(results))}
	var failed []string
	for _, r := range results {
		row := hooksSyncProfile{Profile: r.Account, Path: r.Path, Status: string(r.Status)}
		if r.Err != nil {
			row.Status, row.Error = "error", r.Err.Error()
			failed = append(failed, r.Account)
		}
		data.Profiles = append(data.Profiles, row)
	}
	if len(failed) > 0 {
		return cli.WriteJSONPartial(cmd, data, fmt.Errorf("hooks-sync failed for %s: %s", cli.CountOf(len(failed), "profile", "profiles"), strings.Join(failed, ", ")))
	}
	return cli.WriteJSON(cmd, data)
}

// hooksStatusIsTerminal decides between the table for a person and the
// tab-separated rows for a pipe; tests swap it, since they cannot hand a
// command a terminal.
var hooksStatusIsTerminal = cli.IsTerminal

var configHooksSyncCmd = &cobra.Command{
	Use:   "hooks-sync [PROFILE]",
	Short: "Sync Agent Mux hooks for configured harness profiles",
	Long: `Write the Agent Mux hook entries into each configured profile's harness
settings, so the daemon hears about prompts, permissions, and stops. Only the
Agent Mux entries are merged; anything else in the file is kept. With PROFILE,
only that profile is synced.

Hooks name the amx on PATH. A different build refuses to write them unless
--this-binary is passed, which points every hook at that build instead.
A profile that fails to sync makes the command exit nonzero after the others
are written.`,
	Example: `  amx config hooks-sync
  amx config hooks-sync codex-work
  amx config hooks-sync --json
  amx config hooks-status`,
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: cli.CompleteProfileFirstArg,
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput := hooksSyncJSON
		cfg, err := cli.LoadRunnableConfig()
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "config_error", err)
		}
		bin, err := hooksBinary()
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "binary_unresolved", err)
		}
		if err := cli.RefuseTransientBinary(bin, "write hook commands"); err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "transient_binary", err)
		}
		if installed, foreign := hooks.ExpectedBinary(bin, hooksLookPath); foreign && !hooksSyncThisBinary {
			return cli.CommandFailure(cmd, jsonOutput, "foreign_binary", fmt.Errorf("refusing to write hook commands that run %s: it is not the amx on PATH (%s), which is the one hooks are expected to name; run `%s config hooks-sync`, or pass --this-binary to point every hook at this build", cli.DisplayPath(bin), cli.DisplayPath(installed), cli.DisplayPath(installed)))
		}
		filter := ""
		if len(args) > 0 {
			filter = args[0]
		}
		results, err := hooks.MergeAll(cfg, filter, bin)
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "hooks_sync_failed", err)
		}
		if jsonOutput {
			return writeHooksSyncJSON(cmd, bin, results)
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
	Long: `Report, for each configured profile, whether its hook files match what
hooks-sync would write (SYNC) and whether the harness has been seen running
them (ACTIVATION), with the reason in DETAIL. Hints below the table name the
remedy for each state some profile is in.

On a terminal the report is a table; piped, it is headerless tab-separated
rows with the hints on stderr. A build other than the installed amx judges the
installed one, and says so.`,
	Example: `  amx config hooks-status
  amx config hooks-status --json
  amx config hooks-status | awk -F'\t' '$3 != "in_sync"'`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := cli.LoadRunnableConfig()
		if err != nil {
			return cli.CommandFailure(cmd, hooksStatusJSON, "config_error", err)
		}
		running, err := hooksBinary()
		if err != nil {
			return cli.CommandFailure(cmd, hooksStatusJSON, "binary_unresolved", err)
		}
		// A build other than the installed amx judges the installation, not
		// itself: against itself every hook file would read as drifted.
		bin, foreign := hooks.ExpectedBinary(running, hooksLookPath)
		report := debugger.InspectHooks(cfg, bin, time.Now())
		if hooksStatusJSON {
			data := hooksStatusData{HookReport: report, JudgedBinary: bin, Foreign: foreign, Hints: hooksStatusHints(report.Profiles)}
			if data.Hints == nil {
				data.Hints = []string{}
			}
			if report.Error != "" {
				return cli.WriteJSONPartial(cmd, data, fmt.Errorf("%s", report.Error))
			}
			return cli.WriteJSON(cmd, data)
		}
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
