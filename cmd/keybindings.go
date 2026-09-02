package cmd

import (
	"fmt"
	"os"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/tui/customization"
	"github.com/spf13/cobra"
)

var keybindingsCmd = &cobra.Command{
	Use:   "keybindings",
	Short: "Show the Kitty keybinding reference as an overlay",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := kitty.NewClient(kitty.NewRealRunner())
		if focusExistingKeybindings(client) {
			return nil
		}
		return customization.RunKeybindingsOverlay()
	},
}

func focusExistingKeybindings(client kitty.KittyClient) bool {
	osWindows, err := client.ListWindows()
	if err != nil {
		return false
	}
	loc, found := kitty.FindKeybindingsWindow(osWindows, windowIDFromEnv())
	if !found {
		return false
	}
	if err := client.FocusTab(kitty.MatchTabByID(loc.TabID)); err != nil {
		return false
	}
	_ = client.FocusWindow(kitty.MatchWindowByID(loc.WindowID))
	fmt.Fprintln(os.Stderr, "Keybinding reference already open — focused existing overlay.")
	return true
}
