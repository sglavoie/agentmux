package session

import (
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/spf13/cobra"
)

// FocusPrevManagedCommand, FocusNextManagedCommand, and FocusNextCommand return
// the root-level focus leaves so the root command can register them.
func FocusPrevManagedCommand() *cobra.Command { return focusPrevManagedCmd }
func FocusNextManagedCommand() *cobra.Command { return focusNextManagedCmd }
func FocusNextCommand() *cobra.Command        { return focusNextCmd }

var focusPrevManagedWindowID int
var focusNextManagedWindowID int
var focusNextWindowID int

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

var focusNextCmd = &cobra.Command{
	Use:   "focus-next",
	Short: "Focus the next tab requiring attention",
	Long: `Cycles focus to the next Kitty tab whose state requires user interaction
(stop, permission, error, or elicitation) or that has a Kitty bell notification (🔔).
Wraps around from the end.

KITTY_WINDOW_ID must be set (provided automatically by harness hooks),
or supply --window-id explicitly.`,
	RunE: runFocusNext,
}

func init() {
	focusPrevManagedCmd.Flags().IntVar(&focusPrevManagedWindowID, "window-id", 0,
		"Kitty window ID of the current tab (defaults to $KITTY_WINDOW_ID)")
	focusNextManagedCmd.Flags().IntVar(&focusNextManagedWindowID, "window-id", 0,
		"Kitty window ID of the current tab (defaults to $KITTY_WINDOW_ID)")
	focusNextCmd.Flags().IntVar(&focusNextWindowID, "window-id", 0,
		"Kitty window ID of the current tab (defaults to $KITTY_WINDOW_ID)")
}

func runFocusManaged(windowID, direction int) error {
	if windowID == 0 {
		windowID = cli.WindowIDFromEnv()
	}

	client := cli.NewKittyClient()
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

var attentionStates = map[string]bool{
	"stop":     true,
	"key":      true,
	"error":    true,
	"question": true,
}

func runFocusNext(_ *cobra.Command, _ []string) error {
	windowID := cli.WindowIDOrEnv(focusNextWindowID)

	client := cli.NewKittyClient()
	osWindows, err := client.ListWindows()
	if err != nil {
		return err
	}

	var tabs []kitty.Tab
	currentTabIdx := -1
	for _, osw := range osWindows {
		found := false
		for i, tab := range osw.Tabs {
			for _, win := range tab.Windows {
				if win.ID == windowID {
					currentTabIdx = i
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if found {
			tabs = osw.Tabs
			break
		}
	}

	n := len(tabs)
	if n == 0 {
		return nil
	}

	start := 0
	if currentTabIdx >= 0 {
		start = (currentTabIdx + 1) % n
	}
	for i := 0; i < n; i++ {
		idx := (start + i) % n
		if idx == currentTabIdx {
			continue
		}
		if attentionStates[palette.DetectState(tabs[idx].Title)] || tabs[idx].NeedsAttention {
			return client.FocusTab(kitty.MatchTabByID(tabs[idx].ID))
		}
	}
	return nil
}
