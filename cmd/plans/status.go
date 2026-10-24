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
		Long: `Ask agent-session-loop (` + "`asl status`" + `) how far the split plan PLAN has got:
its sessions complete and pending, and the run working on it. PLAN is a plan
directory or a plan name, as for ` + "`amx plans run`" + `. --json carries the
complete status, including each session's recorded verification.`,
		Example: `  amx plans status 2e774ec64570
  amx plans status ~/.local/share/agent-plans/2e774ec64570-1a09b1daef1c --json`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completePlans,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonOutput, _ := cmd.Flags().GetBool("json")
			if len(args) == 0 {
				return cli.MissingParameter(cmd, jsonOutput, "PLAN", "a plan directory, or the name of one under the configured plans directory")
			}
			cfg, err := planLoadConfig()
			if err != nil {
				return cli.ConfigFailure(cmd, jsonOutput, err)
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
	cli.AddJSONFlag(cmd, nil)
	return cmd
}

func init() { planCmd.AddCommand(newStatusCommand()) }
