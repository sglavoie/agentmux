package session

import (
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
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

// focusRuntimeHelp is the part of each focus command's help about sessions
// outside Kitty.
const focusRuntimeHelp = `

A session on the tmux backend has no Kitty tab. From one, the command works on
the runtime session it runs in (AMX_RUNTIME_SESSION_ID, or --runtime-session):
the sessions are taken in the order the dashboard lists them, and the terminal
attached to the current one is switched to the session chosen. --client names
that terminal, as the keys bound in the private tmux configuration do. The
terminal never lands on an agent that exited or was lost, and the session it
arrives at has no other terminal left attached.`

var focusPrevManagedCmd = &cobra.Command{
	Use:   "focus-prev-managed",
	Short: "Focus the previous managed tab",
	Long: `Cycles focus to the previous Agent Mux-managed Kitty tab within the
current Kitty OS window. Wraps around from the beginning.

KITTY_WINDOW_ID must be set (provided automatically by Kitty launches),
or supply --window-id explicitly.` + focusRuntimeHelp,
	Example: `  amx focus-prev-managed
  amx focus-prev-managed --runtime-session rs-4f1c0b9e2a7d4c3b8e6f1a2b3c4d5e6f`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if id, ok := focusRuntimeSession(cmd, focusPrevManagedWindowID); ok {
			return runFocusRuntime(cmd, id, neighborSession(-1))
		}
		return runFocusManaged(focusPrevManagedWindowID, -1)
	},
}

var focusNextManagedCmd = &cobra.Command{
	Use:   "focus-next-managed",
	Short: "Focus the next managed tab",
	Long: `Cycles focus to the next Agent Mux-managed Kitty tab within the current
Kitty OS window. Wraps around from the end.

KITTY_WINDOW_ID must be set (provided automatically by Kitty launches),
or supply --window-id explicitly.` + focusRuntimeHelp,
	Example: `  amx focus-next-managed
  amx focus-next-managed --window-id 12`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if id, ok := focusRuntimeSession(cmd, focusNextManagedWindowID); ok {
			return runFocusRuntime(cmd, id, neighborSession(1))
		}
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
or supply --window-id explicitly.` + focusRuntimeHelp,
	Example: `  amx focus-next
  amx focus-next --runtime-session rs-4f1c0b9e2a7d4c3b8e6f1a2b3c4d5e6f --client /dev/ttys004`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if id, ok := focusRuntimeSession(cmd, focusNextWindowID); ok {
			return runFocusRuntime(cmd, id, nextAttentionSession)
		}
		return runFocusNext(cmd, args)
	},
}

func init() {
	focusPrevManagedCmd.Flags().IntVar(&focusPrevManagedWindowID, "window-id", 0,
		"Kitty window ID of the current tab (defaults to $KITTY_WINDOW_ID)")
	focusNextManagedCmd.Flags().IntVar(&focusNextManagedWindowID, "window-id", 0,
		"Kitty window ID of the current tab (defaults to $KITTY_WINDOW_ID)")
	focusNextCmd.Flags().IntVar(&focusNextWindowID, "window-id", 0,
		"Kitty window ID of the current tab (defaults to $KITTY_WINDOW_ID)")
	for _, cmd := range []*cobra.Command{focusPrevManagedCmd, focusNextManagedCmd, focusNextCmd} {
		addFocusRuntimeFlags(cmd)
	}
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

func runFocusNext(_ *cobra.Command, _ []string) error {
	windowID := cli.WindowIDOrEnv(focusNextWindowID)

	client := cli.NewKittyClient()
	osWindows, err := client.ListWindows()
	if err != nil {
		return err
	}

	tab, ok := nextAttentionTab(osWindows, windowID)
	if !ok {
		return nil
	}
	return client.FocusTab(kitty.MatchTabByID(tab.ID))
}

// nextAttentionTab picks the tab focus-next moves to: the first tab after the
// one holding windowID, in the same OS window and wrapping around, whose title
// carries a protocol.AttentionStates icon or which Kitty itself flags. The
// current tab is never chosen. The predicate is the one the companion snapshot
// sets its attention bit with, so the two cannot disagree about a tab.
func nextAttentionTab(osWindows []kitty.OSWindow, windowID int) (kitty.Tab, bool) {
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
		return kitty.Tab{}, false
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
		if protocol.AttentionStates[palette.DetectState(tabs[idx].Title)] || tabs[idx].NeedsAttention {
			return tabs[idx], true
		}
	}
	return kitty.Tab{}, false
}
