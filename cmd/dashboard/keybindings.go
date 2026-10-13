package dashboard

import (
	"fmt"
	"os"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/kittykeys"
	"github.com/sglavoie/agentmux/tui/customization"
	"github.com/spf13/cobra"
)

// KeybindingsCommand returns the `keybindings` overlay command so the root
// command can register it. Root assembly is the only consumer.
func KeybindingsCommand() *cobra.Command { return keybindingsCmd }

var keybindingsCmd = &cobra.Command{
	Use:   "keybindings",
	Short: "Show the Kitty keybinding reference as an overlay",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Outside Kitty there is no other instance to find: the reference
		// simply opens in this terminal, and the keys it lists are the ones
		// the private tmux configuration binds.
		if !insideKitty() {
			return runKeybindingsReference(kittykeys.Tmux)
		}
		client := cli.NewKittyClient()
		if focusExistingKeybindings(client) {
			return nil
		}
		return runKeybindingsOverlay()
	},
}

// The reference in this terminal: Kitty's keys, or a table of its own.
var (
	runKeybindingsOverlay   = customization.RunKeybindingsOverlay
	runKeybindingsReference = customization.RunKeybindingsReference
)

func focusExistingKeybindings(client kitty.KittyClient) bool {
	osWindows, err := client.ListWindows()
	if err != nil {
		return false
	}
	loc, found := kitty.FindKeybindingsWindow(osWindows, cli.WindowIDFromEnv())
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
