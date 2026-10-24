package config

import (
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/spf13/cobra"
)

var confirmationCmd = &cobra.Command{
	Use: "confirmation", Short: "Report the effective destructive-action confirmation policy", Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		policy, _ := cmd.Flags().GetString("policy")
		jsonOutput, _ := cmd.Flags().GetBool("json")
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
