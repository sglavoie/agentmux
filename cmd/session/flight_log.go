package session

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/flightlog"
	"github.com/spf13/cobra"
)

type flightLogDependencies struct {
	store     func() (flightlog.Store, error)
	now       func() time.Time
	harnesses func() []string
}

func defaultFlightLogDependencies() flightLogDependencies {
	return flightLogDependencies{store: flightlog.DefaultStore, now: time.Now, harnesses: func() []string {
		var names []string
		if cfg, err := sessionCommandsLoadConfig(); err == nil {
			for _, profile := range cfg.Accounts {
				if name := profile.EffectiveHarness(); !slices.Contains(names, name) {
					names = append(names, name)
				}
			}
		}
		return names
	}}
}

func newFlightLogCommand(deps flightLogDependencies) *cobra.Command {
	var filters flightlog.Filters
	jsonOutput := false
	cmd := &cobra.Command{Use: "flight-log", Aliases: []string{"stats"}, Short: "Explore local session statistics, milestones, and weekly recaps", Args: cobra.NoArgs,
		Long: `Flight Log counts successful launches, effective state changes, and turns
finished. Time totals sum observed session time, including overlapping
agents; missing observations remain unknown. Weeks start on Monday in the
local timezone. Achievements always use the whole log. No transcript text,
commands, or environment values are stored. Data is local and independent
of diagnostic journal retention.`,
		Example: `  amx session flight-log
  amx session flight-log --period all --project API
  amx session flight-log --session rs-example --period all --json
  amx session flight-log export --json > flight-log.json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !slices.Contains([]string{"today", "week", "all"}, filters.Period) {
				return cli.CommandFailure(cmd, jsonOutput, "invalid_arguments", fmt.Errorf("--period must be today, week, or all"))
			}
			store, err := deps.store()
			if err != nil {
				return cli.CommandFailure(cmd, jsonOutput, "flight_log_unavailable", err)
			}
			snapshot, err := store.Load(cli.CommandContext(cmd))
			if err != nil {
				return cli.CommandFailure(cmd, jsonOutput, "flight_log_unavailable", err)
			}
			report := flightlog.Build(snapshot, filters, deps.now(), deps.harnesses())
			if filters.Session != "" && len(report.Sessions) == 0 {
				return cli.CommandFailure(cmd, jsonOutput, "session_not_found", fmt.Errorf("no recorded session matches %q in this period", filters.Session))
			}
			if jsonOutput {
				return cli.WriteJSON(cmd, report)
			}
			return writeFlightLog(cmd.OutOrStdout(), report)
		}}
	cli.AddJSONFlag(cmd, &jsonOutput)
	cmd.Flags().StringVar(&filters.Period, "period", "week", "calendar period: today, week, all")
	cmd.Flags().StringVar(&filters.Project, "project", "", "exact project label")
	cmd.Flags().StringVar(&filters.Harness, "harness", "", "exact harness name")
	cmd.Flags().StringVar(&filters.Profile, "profile", "", "exact profile name")
	cmd.Flags().StringVar(&filters.Session, "session", "", "exact Flight Log key, runtime-session ID, or launch ID")
	for flag, values := range map[string][]string{"period": {"today", "week", "all"}, "harness": {"claude", "codex", "copilot"}} {
		_ = cmd.RegisterFlagCompletionFunc(flag, cobra.FixedCompletions(values, cobra.ShellCompDirectiveNoFileComp))
	}
	for _, action := range []string{"export", "settings", "reset", "import"} {
		cmd.AddCommand(newFlightLogAction(action, deps))
	}
	cmd.AddCommand(newFlightMissionCommand(deps))
	return cmd
}

func newFlightLogAction(action string, deps flightLogDependencies) *cobra.Command {
	jsonOutput, yes, enabled, celebrations := false, false, true, true
	short := map[string]string{"export": "Export the complete local ledger as versioned JSON", "settings": "Show or change tracking and milestone celebrations", "reset": "Delete Flight Log history and begin a new tracking period", "import": "Import partial retained history from before tracking started"}[action]
	cmd := &cobra.Command{Use: action, Short: short, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		store, err := deps.store()
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "flight_log_unavailable", err)
		}
		ctx := cli.CommandContext(cmd)
		switch action {
		case "reset":
			if !yes {
				return cli.CommandFailure(cmd, jsonOutput, "confirmation_required", fmt.Errorf("reset permanently deletes Flight Log history; export it first if needed, then pass --yes"))
			}
			err = store.Reset(ctx, deps.now())
		case "settings":
			var ep, cp *bool
			if cmd.Flags().Changed("enabled") {
				ep = &enabled
			}
			if cmd.Flags().Changed("celebrations") {
				cp = &celebrations
			}
			if ep != nil || cp != nil {
				err = store.Configure(ctx, ep, cp)
			}
		case "import":
			var result flightLogImportResult
			result, err = importFlightLog(ctx, store, deps.now())
			if err != nil {
				return cli.CommandFailure(cmd, jsonOutput, "flight_log_unavailable", err)
			}
			if jsonOutput {
				return cli.WriteJSON(cmd, result)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Imported %d partial historical events (%d unreadable journal records). Time coverage was not reconstructed.\n", result.Imported, result.CorruptLines)
			return err
		}
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "flight_log_unavailable", err)
		}
		snapshot, err := store.Load(ctx)
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "flight_log_unavailable", err)
		}
		if action == "export" {
			return cli.WriteJSON(cmd, snapshot)
		}
		if jsonOutput {
			return cli.WriteJSON(cmd, snapshot.Settings)
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Tracking: %t · Celebrations: %t\n", snapshot.Settings.Enabled, snapshot.Settings.Celebrations)
		return err
	}}
	cli.AddJSONFlag(cmd, &jsonOutput)
	if action == "reset" {
		cmd.Flags().BoolVar(&yes, "yes", false, "confirm permanent deletion of Flight Log history")
	}
	if action == "settings" {
		cmd.Flags().BoolVar(&enabled, "enabled", true, "record new activity (false pauses tracking)")
		cmd.Flags().BoolVar(&celebrations, "celebrations", true, "show newly unlocked milestone banners")
	}
	return cmd
}

type flightLogImportResult struct {
	Imported     int  `json:"imported"`
	CorruptLines int  `json:"corrupt_lines"`
	Partial      bool `json:"partial"`
}

func importFlightLog(ctx context.Context, store flightlog.Store, now time.Time) (flightLogImportResult, error) {
	result := flightLogImportResult{Partial: true}
	if err := store.Initialize(ctx, now); err != nil {
		return result, err
	}
	snapshot, err := store.Load(ctx)
	if err != nil {
		return result, err
	}
	if !snapshot.Settings.Enabled {
		return result, fmt.Errorf("enable tracking before importing history")
	}
	paths, err := diagnostics.DefaultPaths()
	if err != nil {
		return result, err
	}
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		return result, err
	}
	recorded, err := journal.Snapshot(ctx, diagnostics.Selector{Before: snapshot.TrackingSince, Stages: []string{"session.launch", "daemon.decision"}}, nil)
	if err != nil {
		return result, err
	}
	seen := map[string]bool{}
	for _, e := range snapshot.Events {
		seen[e.ID] = true
	}
	var imported []flightlog.Event
	for _, record := range recorded.Records {
		if !record.ObservedAt.Before(snapshot.TrackingSince) {
			continue
		}
		e, ok := flightlog.FromEnvelope(record)
		if ok && !seen[e.ID] {
			seen[e.ID] = true
			imported = append(imported, e)
		}
	}
	err = store.Append(ctx, imported...)
	result.Imported = len(imported)
	result.CorruptLines = recorded.Statistics.CorruptLines
	return result, err
}

func writeFlightLog(w io.Writer, r flightlog.Report) error {
	var b strings.Builder
	fmt.Fprintf(&b, "FLIGHT LOG · %s\n", r.Filters.Period)
	if !r.TrackingSince.IsZero() {
		fmt.Fprintf(&b, "Tracking since %s · %s\n", r.TrackingSince.Local().Format(time.DateTime), r.Timezone)
	}
	m := r.Totals
	fmt.Fprintf(&b, "\n%d launches · %d state changes · %d turns finished\n%d projects · %d conversations · %d ASL runs finished\n", m.Launches, m.StateChanges, m.TurnsFinished, m.Projects, m.Conversations, m.RunsFinished)
	fmt.Fprintf(&b, "Fresh %d · Resumed %d · Forked %d · Restarted %d\nObserved work %s · Waiting for you %s · Peak working %d\n", m.Fresh, m.Resumes, m.Forks, m.Restarts, flightDuration(m.WorkingSeconds), flightDuration(m.WaitingSeconds), m.PeakWorking)
	b.WriteString("\nLAST 28 DAYS (launches)\n")
	for _, day := range r.Days[max(0, len(r.Days)-28):] {
		level := min(day.Metrics.Launches, 4)
		fmt.Fprintf(&b, "%s %s %d\n", day.Date, []string{"·", "▂", "▄", "▆", "█"}[level], day.Metrics.Launches)
	}
	b.WriteString("\nMILESTONES (all time)\n")
	for _, a := range r.Achievements {
		status := fmt.Sprintf("%d/%d", a.Progress, a.Target)
		if a.UnlockedAt != nil {
			status = "unlocked " + a.UnlockedAt.Local().Format(time.DateOnly)
		}
		fmt.Fprintf(&b, "%s · %s\n", a.Title, status)
	}
	b.WriteString("\nMISSIONS (all time, project filter)\n")
	for _, m := range r.Missions {
		fmt.Fprintf(&b, "%s · %s · %s · %s\n", m.ID, m.Project, m.Title, m.Status)
	}
	b.WriteString("\nTHIS WEEK\n")
	for _, line := range r.Recap {
		fmt.Fprintln(&b, line)
	}
	if r.Filters.Session != "" {
		for _, s := range r.Sessions {
			fmt.Fprintf(&b, "\n%s · %s · %s\nLaunch: %s · %d questions · %d permissions\n", s.Key, s.Project, s.Harness, s.Operation, s.Metrics.Questions, s.Metrics.Permissions)
			for _, e := range s.Events {
				if e.Kind == "state" {
					fmt.Fprintf(&b, "%s  %s → %s (loop %t → %t)\n", e.At.Local().Format(time.DateTime), flightlog.Label(e.BeforeIcon), flightlog.Label(e.Icon), e.BeforeLooping, e.Looping)
				}
			}
		}
	} else {
		b.WriteString("\nSESSIONS\n")
		for _, s := range r.Sessions {
			fmt.Fprintf(&b, "%s  %s  %s  %d changes\n", s.Key, s.Project, s.Harness, s.Metrics.StateChanges)
		}
	}
	for _, warning := range r.Warnings {
		fmt.Fprintf(&b, "\n%s\n", warning)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func flightDuration(seconds float64) string { return (time.Duration(seconds) * time.Second).String() }

func init() { sessionCmd.AddCommand(newFlightLogCommand(defaultFlightLogDependencies())) }
