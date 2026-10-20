package session

import (
	"fmt"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/spf13/cobra"
)

// ResortCommand returns the root-level resort leaf so the root command can
// register it.
func ResortCommand() *cobra.Command { return resortCmd }

var (
	resortLoadConfig = cli.LoadRunnableConfig
	resortNewClient  = func() kitty.KittyClient { return cli.NewKittyClient() }
	resortSessions   = session.Resort
)

var resortCmd = &cobra.Command{
	Use:   "resort",
	Short: "Reorder Kitty tabs to match the configured sort",
	Long: `Moves the Agent Mux tabs in Kitty into the order [defaults] sort names.
Only Kitty tabs are moved; tmux sessions have no tab order to change. It
needs a reachable Kitty.`,
	Example: `  amx resort`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := resortLoadConfig()
		if err != nil {
			return err
		}
		client := resortNewClient()
		if err := resortSessions(client, cfg.Defaults.Sort); err != nil {
			if cli.KittyUnreachable(err) {
				return fmt.Errorf("resort reorders Kitty tabs, and %s", cli.KittyUnreachableMessage)
			}
			return fmt.Errorf("resort: %w", err)
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "tabs resorted")
		return err
	},
}
