package dashboard

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

// ColorPickerCommand returns the `color-picker` overlay command so the root
// command can register it. Root assembly is the only consumer.
func ColorPickerCommand() *cobra.Command { return colorPickerCmd }

var colorPickerCmd = &cobra.Command{
	Use:   "color-picker",
	Short: "Set the current tab's color (overlay picker)",
	Long: `Pick the color of the Kitty tab this is opened on, as an overlay.

Outside Kitty the picker opens in this terminal and colors a tmux session
instead: the one named by --runtime-session, else the one the command is typed
in, else the only one running. The dashboard draws that session in the color,
and Agent Mux.app draws its sidebar line on it.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if portablePickerRequested(cmd) {
			return runPortableColorPicker(cmd)
		}
		client := newColorPickerClient()
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
		return runColorPickerOverlay(ctx)
	},
}

var (
	newColorPickerClient  = func() kitty.KittyClient { return cli.NewKittyClient() }
	loadPickerConfig      = config.LoadDefault
	runColorPickerOverlay = customization.RunColorOverlay
)

func init() {
	addPickerSessionFlag(colorPickerCmd)
	addPickerSessionFlag(iconPickerCmd)
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
