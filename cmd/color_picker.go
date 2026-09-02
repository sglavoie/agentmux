package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/tui/customization"
	"github.com/spf13/cobra"
)

var colorPickerCmd = &cobra.Command{
	Use:   "color-picker",
	Short: "Set the current tab's color (overlay picker)",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := cli.NewKittyClient()
		if focusExistingColorPicker(client) {
			return nil
		}

		windowID := cli.WindowIDFromEnv()
		osWindows, err := client.ListWindows()
		if err != nil {
			return err
		}

		tab, _, ok := kitty.FindTabByWindowID(osWindows, windowID)
		if !ok {
			return fmt.Errorf("could not find tab for window %d", windowID)
		}

		ctx := resolveColorPickerContext(client, tab, windowID)
		return customization.RunColorOverlay(ctx)
	},
}

func focusExistingColorPicker(client kitty.KittyClient) bool {
	osWindows, err := client.ListWindows()
	if err != nil {
		return false
	}
	loc, found := kitty.FindColorPickerWindow(osWindows, cli.WindowIDFromEnv())
	if !found {
		return false
	}
	if err := client.FocusTab(kitty.MatchTabByID(loc.TabID)); err != nil {
		return false
	}
	_ = client.FocusWindow(kitty.MatchWindowByID(loc.WindowID))
	fmt.Fprintln(os.Stderr, "Color picker already open — focused existing overlay.")
	return true
}

func resolveColorPickerContext(client kitty.KittyClient, tab *kitty.Tab, overlayWindowID int) customization.ColorContext {
	ctx := customization.ColorContext{
		Client:       client,
		TabID:        tab.ID,
		DefaultColor: "gray",
	}

	for i := range tab.Windows {
		w := &tab.Windows[i]
		if w.ID == overlayWindowID {
			continue
		}
		if w.UserVars.EffectiveProfile() != "" {
			ctx.WindowID = w.ID
			cfg, err := config.LoadDefault()
			if err != nil {
				break
			}
			for _, a := range cfg.Accounts {
				if strings.EqualFold(a.Name, w.UserVars.EffectiveProfile()) {
					ctx.DefaultColor = a.Color
					break
				}
			}
			break
		}
	}

	return ctx
}
