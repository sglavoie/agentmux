package cmd

import (
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/spf13/cobra"
)

var focusPrevManagedWindowID int
var focusNextManagedWindowID int

var focusPrevManagedCmd = &cobra.Command{
	Use:   "focus-prev-managed",
	Short: "Focus the previous managed tab",
	Long: `Cycles focus to the previous Agent Mux-managed Kitty tab within the
current Kitty OS window. Wraps around from the beginning.

KITTY_WINDOW_ID must be set (provided automatically by Kitty launches),
or supply --window-id explicitly.`,
	RunE: func(_ *cobra.Command, _ []string) error {
		return runFocusManaged(focusPrevManagedWindowID, -1)
	},
}

var focusNextManagedCmd = &cobra.Command{
	Use:   "focus-next-managed",
	Short: "Focus the next managed tab",
	Long: `Cycles focus to the next Agent Mux-managed Kitty tab within the current
Kitty OS window. Wraps around from the end.

KITTY_WINDOW_ID must be set (provided automatically by Kitty launches),
or supply --window-id explicitly.`,
	RunE: func(_ *cobra.Command, _ []string) error {
		return runFocusManaged(focusNextManagedWindowID, 1)
	},
}

func init() {
	rootCmd.AddCommand(focusPrevManagedCmd, focusNextManagedCmd)
	focusPrevManagedCmd.Flags().IntVar(&focusPrevManagedWindowID, "window-id", 0,
		"Kitty window ID of the current tab (defaults to $KITTY_WINDOW_ID)")
	focusNextManagedCmd.Flags().IntVar(&focusNextManagedWindowID, "window-id", 0,
		"Kitty window ID of the current tab (defaults to $KITTY_WINDOW_ID)")
}

func runFocusManaged(windowID, direction int) error {
	if windowID == 0 {
		windowID = windowIDFromEnv()
	}

	client := kitty.NewClient(kitty.NewRealRunner())
	return focusManagedTab(client, windowID, direction)
}

func focusManagedTab(client kitty.KittyClient, windowID, direction int) error {
	if windowID == 0 {
		return nil
	}

	osWindows, err := client.ListWindows()
	if err != nil {
		return err
	}

	tab, ok := session.FindManagedTabRelative(osWindows, windowID, direction)
	if !ok {
		return nil
	}

	return client.FocusTab(kitty.MatchTabByID(tab.ID))
}
