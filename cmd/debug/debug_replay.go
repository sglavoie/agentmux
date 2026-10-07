package debug

import (
	"fmt"
	"io"
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/debugger"
	"github.com/spf13/cobra"
)

var debugReplayJSON bool

var debugReplayCmd = &cobra.Command{
	Use:          "replay <journal-or-bundle>",
	Short:        "Recompute recorded routing and reducer outcomes entirely offline",
	Long:         "Recompute recorded routing, reducer, reset, duplicate, and render-cause outcomes from an immutable journal, diagnostic bundle, or events export. Replay never connects to Kitty or the daemon and has no live-apply mode.",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE:         runDebugReplay,
	// The argument is a file, so it keeps the shell's file completion that
	// root assembly turns off for every command that does not ask for it.
	ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return nil, cobra.ShellCompDirectiveDefault
	},
}

func init() {
	debugReplayCmd.Flags().BoolVar(&debugReplayJSON, "json", false, "write complete schema-versioned comparisons as JSON")
	debugCmd.AddCommand(debugReplayCmd)
}

func runDebugReplay(cmd *cobra.Command, args []string) error {
	selector, err := debugSelector(debugNow().UTC())
	if err != nil {
		return err
	}
	report, err := debugger.Replay(cmd.Context(), config.ExpandTilde(args[0]), debugger.ReplayOptions{Selector: selector})
	if err != nil {
		return err
	}
	if debugReplayJSON {
		return writeIndentedJSON(cmd.OutOrStdout(), report)
	}
	return writeDebugReplayText(cmd.OutOrStdout(), report)
}

func writeDebugReplayText(out io.Writer, report debugger.ReplayReport) error {
	verdict := "matched"
	if !report.Success {
		verdict = "mismatch"
	} else if !report.Complete {
		verdict = "incomplete"
	}
	if _, err := fmt.Fprintf(out,
		"Agent Mux offline replay (schema %d)\ninput: %s (%s schema %d)\nverdict: %s\ndecisions: %d routes: %d render_groups: %d restarts: %d\nmatched: %d mismatched: %d incomplete: %d\n",
		report.SchemaVersion, report.Input.Path, report.Input.Format, report.Input.SchemaVersion, verdict,
		report.Summary.DecisionComparisons, report.Summary.RouteComparisons, report.Summary.RenderGroups,
		report.Summary.DaemonRestarts, report.Summary.Matched, report.Summary.Mismatched, report.Summary.Incomplete,
	); err != nil {
		return err
	}
	for _, comparison := range report.Comparisons {
		if _, err := fmt.Fprintf(out, "- decision #%d window=%s event=%s source=%s/%s seq=%d recorded=%s recomputed=%s state=%s\n",
			comparison.Index, emptyDash(comparison.WindowID), emptyDash(comparison.Event), emptyDash(comparison.SourceKind),
			emptyDash(comparison.SourceKey), comparison.SourceSequence, emptyDash(string(comparison.RecordedDecision)),
			emptyDash(string(comparison.RecomputedDecision)), comparison.Status); err != nil {
			return err
		}
	}
	for _, route := range report.Routes {
		if _, err := fmt.Fprintf(out, "- route window=%s recorded=%s/%s recomputed=%s/%s status=%s\n",
			emptyDash(route.WindowID), emptyDash(route.RecordedRoute), emptyDash(route.RecordedTargetID),
			emptyDash(route.RecomputedRoute), emptyDash(route.RecomputedTarget), route.Status); err != nil {
			return err
		}
	}
	for _, render := range report.Renders {
		if _, err := fmt.Fprintf(out, "- render window=%s generation=%d sources=%v terminal=%s status=%s\n",
			emptyDash(render.WindowID), render.Generation, render.SourceGenerations, emptyDash(render.TerminalStage), render.Status); err != nil {
			return err
		}
	}
	if len(report.Diagnostics) > 0 {
		if _, err := fmt.Fprintln(out, "diagnostics:"); err != nil {
			return err
		}
		for _, diagnostic := range report.Diagnostics {
			message := strings.ReplaceAll(diagnostic.Message, "\n", " ")
			if _, err := fmt.Fprintf(out, "- [%s/%s] %s\n", diagnostic.Severity, diagnostic.Code, message); err != nil {
				return err
			}
		}
	}
	return nil
}
