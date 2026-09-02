package cmd

import (
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/spf13/cobra"
)

var focusNextWindowID int

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
	rootCmd.AddCommand(focusNextCmd)
	focusNextCmd.Flags().IntVar(&focusNextWindowID, "window-id", 0,
		"Kitty window ID of the current tab (defaults to $KITTY_WINDOW_ID)")
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
