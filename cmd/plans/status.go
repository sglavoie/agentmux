package plans

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/planrun"
	"github.com/spf13/cobra"
)

func newStatusCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "status PLAN", Short: "Show ASL session progress and recorded verification for a plan",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonOutput, _ := cmd.Flags().GetBool("json")
			cfg, err := planLoadConfig()
			if err != nil {
				return cli.CommandFailure(cmd, jsonOutput, "config_invalid", err)
			}
			if cfg.Defaults.ASL.ASLModeValue() == "disabled" {
				return cli.CommandFailure(cmd, jsonOutput, "asl_disabled", errors.New("ASL is disabled in the configuration"))
			}
			plan, err := selectPlan(cfg, args[0])
			if err != nil {
				return planActionError(cmd, jsonOutput, err)
			}
			asl := planASL
			if asl == nil {
				asl = planrun.Exec{Command: cfg.Defaults.ASL.Command}
			}
			ctx, cancel := context.WithTimeout(cli.CommandContext(cmd), 20*time.Second)
			defer cancel()
			status, err := planrun.ReadStatus(ctx, asl, plan.DirPath, cfg.Defaults.ASL.StateRoot)
			if err != nil {
				return cli.CommandFailure(cmd, jsonOutput, "asl_status_failed", err)
			}
			if jsonOutput {
				return cli.WriteJSON(cmd, status)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s: %d complete, %d pending\nRun: %s\n", status.Title, status.Counts["complete"], status.Counts["pending"], status.Runner.RunID)
			return err
		},
	}
	cmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	return cmd
}

func init() { planCmd.AddCommand(newStatusCommand()) }
