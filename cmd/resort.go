package cmd

import (
	"fmt"

	"github.com/sglavoie/agentmux/internal/cli"
	ccerrors "github.com/sglavoie/agentmux/internal/errors"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/spf13/cobra"
)

var (
	resortLoadConfig = cli.LoadRunnableConfig
	resortNewClient  = func() kitty.KittyClient { return cli.NewKittyClient() }
	resortSessions   = session.Resort
)

var resortCmd = &cobra.Command{
	Use:   "resort",
	Short: "Reorder Kitty tabs to match the configured sort.",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := resortLoadConfig()
		if err != nil {
			ccerrors.Print(err)
			return err
		}
		client := resortNewClient()
		if err := resortSessions(client, cfg.Defaults.Sort); err != nil {
			return fmt.Errorf("resort: %w", err)
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "tabs resorted")
		return err
	},
}
