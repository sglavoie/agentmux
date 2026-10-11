package dashboard

import (
	"fmt"
	"os"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/tui/customization"
	"github.com/spf13/cobra"
)

var (
	newIconPickerClient = func() kitty.KittyClient {
		return cli.NewKittyClient()
	}
	loadIconPickerConfig = config.LoadDefault
	runIconPickerOverlay = customization.RunIconOverlay
)

// IconPickerCommand returns the `icon-picker` overlay command so the root
// command can register it. Root assembly is the only consumer.
func IconPickerCommand() *cobra.Command { return iconPickerCmd }

var iconPickerCmd = &cobra.Command{
	Use:   "icon-picker",
	Short: "Set or clear the current tab's harness icon (overlay picker)",
	Long: `Pick the harness icon of the Kitty tab this is opened on, as an overlay.

Outside Kitty the picker opens in this terminal and edits the title of a tmux
session instead: the one named by --runtime-session, else the one the command
is typed in, else the only one running.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if portablePickerRequested(cmd) {
			return runPortableIconPicker(cmd)
		}
		client := newIconPickerClient()
		if focusExistingIconPicker(client) {
			return nil
		}

		windowID := cli.WindowIDFromEnv()
		osWindows, err := client.ListWindows()
		if err != nil {
			return fmt.Errorf("list Kitty windows: %w", err)
		}

		tab, _, ok := kitty.FindTabByWindowID(osWindows, windowID)
		if !ok {
			return fmt.Errorf("could not find tab for window %d", windowID)
		}

		cfg, err := loadIconPickerConfig()
		if err != nil {
			return fmt.Errorf("load Agent Mux config: %w", err)
		}

		return runIconPickerOverlay(customization.IconContext{
			Client:        client,
			TabID:         tab.ID,
			CurrentTitle:  tab.Title,
			HarnessEmojis: cfg.HarnessEmojis,
		})
	},
}

func focusExistingIconPicker(client kitty.KittyClient) bool {
	osWindows, err := client.ListWindows()
	if err != nil {
		return false
	}
	loc, found := kitty.FindIconPickerWindow(osWindows, cli.WindowIDFromEnv())
	if !found {
		return false
	}
	if err := client.FocusTab(kitty.MatchTabByID(loc.TabID)); err != nil {
		return false
	}
	_ = client.FocusWindow(kitty.MatchWindowByID(loc.WindowID))
	fmt.Fprintln(os.Stderr, "Harness icon picker already open — focused existing overlay.")
	return true
}
