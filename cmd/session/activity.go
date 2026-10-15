package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sglavoie/agentmux/internal/activityfeed"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/spf13/cobra"
)

func newActivityCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "activity", Short: "Read recorded state changes, exits, and ASL lifecycle events", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			jsonOutput, _ := cmd.Flags().GetBool("json")
			days, _ := cmd.Flags().GetInt("days")
			limit, _ := cmd.Flags().GetInt("limit")
			if days < 1 || days > 90 || limit < 1 || limit > 1000 {
				return cli.CommandFailure(cmd, jsonOutput, "invalid_arguments", errors.New("days must be 1–90 and limit must be 1–1000"))
			}
			paths, err := diagnostics.DefaultPaths()
			if err != nil {
				return cli.CommandFailure(cmd, jsonOutput, "activity_unavailable", err)
			}
			journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
			if err != nil {
				return cli.CommandFailure(cmd, jsonOutput, "activity_unavailable", err)
			}
			ctx, cancel := context.WithTimeout(cli.CommandContext(cmd), 15*time.Second)
			defer cancel()
			report, err := activityfeed.Read(ctx, journal, time.Now().Add(-time.Duration(days)*24*time.Hour), limit)
			if err != nil {
				return cli.CommandFailure(cmd, jsonOutput, "activity_unavailable", err)
			}
			if jsonOutput {
				return cli.WriteJSON(cmd, report)
			}
			for _, entry := range report.Entries {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %s  %s\n", entry.At.Format(time.RFC3339), entry.Harness, entry.Title, entry.Detail); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	cmd.Flags().Int("days", 7, "look back this many days within retained journal history")
	cmd.Flags().Int("limit", 500, "maximum activity records (1–1000)")
	return cmd
}

func init() { sessionCmd.AddCommand(newActivityCommand()) }
