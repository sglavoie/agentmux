package cmd

import (
	"fmt"

	"github.com/sglavoie/agentmux/internal/config"
	ccerrors "github.com/sglavoie/agentmux/internal/errors"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/spf13/cobra"
)

var resortCmd = &cobra.Command{
	Use:   "resort",
	Short: "Reorder Kitty tabs to match the configured sort.",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadDefault()
		if err != nil {
			ccerrors.Print(err)
			return fmt.Errorf("loading config: %w", err)
		}
		client := kitty.NewClient(kitty.NewRealRunner())
		if err := session.Resort(client, cfg.Defaults.Sort); err != nil {
			return fmt.Errorf("resort: %w", err)
		}
		fmt.Println("tabs resorted")
		return nil
	},
}
