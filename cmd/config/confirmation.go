package config

import (
	"fmt"
	"slices"
	"strings"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/spf13/cobra"
)

var confirmationCmd = &cobra.Command{
	Use:   "confirmation",
	Short: "Report the effective destructive-action confirmation policy",
	Long: `Reports whether the actions that end or replace a live harness process
(End Session, restart, and restart under another profile) stop for a y/n
confirmation under --policy. follow-cli (the default) reads
[defaults] confirm_destructive from config.toml; always and never answer
without reading it. Agent Mux.app asks this to apply its own preference to
the views it embeds.

With --json it reports {required}; an unknown --policy is a structured error
(invalid_arguments).`,
	Example: `  amx config confirmation
  amx config confirmation --policy always --json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		policy, _ := cmd.Flags().GetString("policy")
		jsonOutput, _ := cmd.Flags().GetBool("json")
		if !slices.Contains(config.ConfirmationPolicies(), strings.TrimSpace(policy)) {
			return cli.CommandFailure(cmd, jsonOutput, "invalid_arguments", fmt.Errorf("--policy must be one of %s, got %q",
				strings.Join(config.ConfirmationPolicies(), ", "), policy))
		}
		required, err := config.ConfirmationRequired(policy)
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "confirmation_unavailable", err)
		}
		if jsonOutput {
			return cli.WriteJSON(cmd, struct {
				Required bool `json:"required"`
			}{required})
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Confirm destructive actions: %t (%s)\n", required, policy)
		return err
	},
}

func init() {
	confirmationCmd.Flags().String("policy", "follow-cli", "confirmation policy: "+strings.Join(config.ConfirmationPolicies(), ", "))
	_ = confirmationCmd.RegisterFlagCompletionFunc("policy", cli.FixedCompletions(config.ConfirmationPolicies()...))
	cli.AddJSONFlag(confirmationCmd, nil)
	configCmd.AddCommand(confirmationCmd)
}
