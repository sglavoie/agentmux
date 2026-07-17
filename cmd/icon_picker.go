package cmd

import (
	"fmt"
	"os"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/tui"
	"github.com/spf13/cobra"
)

var (
	newIconPickerClient = func() kitty.KittyClient {
		return kitty.NewClient(kitty.NewRealRunner())
	}
	loadIconPickerConfig = config.LoadDefault
	runIconPickerOverlay = tui.RunIconPickerOverlay
)

var iconPickerCmd = &cobra.Command{
	Use:   "icon-picker",
	Short: "Set the current tab's harness icon (overlay picker)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		client := newIconPickerClient()
		if focusExistingIconPicker(client) {
			return nil
		}

		windowID := windowIDFromEnv()
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

		return runIconPickerOverlay(tui.IconPickerContext{
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
	loc, found := kitty.FindIconPickerWindow(osWindows, windowIDFromEnv())
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
