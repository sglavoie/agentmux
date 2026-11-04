package session

import "github.com/spf13/cobra"

func FocusPrevManagedCommand() *cobra.Command { return focusPrevManagedCmd }
func FocusNextManagedCommand() *cobra.Command { return focusNextManagedCmd }
func FocusNextCommand() *cobra.Command        { return focusNextCmd }

const focusRuntimeHelp = `

The command works on the runtime session it runs in
(AMX_RUNTIME_SESSION_ID, or --runtime-session). Sessions follow the order the
dashboard lists them. The terminal attached to the current session switches
to the selected session. --client names that terminal when needed.`

var focusPrevManagedCmd = &cobra.Command{
	Use:     "focus-prev-managed",
	Short:   "Focus the previous managed tmux session",
	Long:    "Cycles focus to the previous managed tmux session." + focusRuntimeHelp,
	Example: "  amx focus-prev-managed",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		id := focusRuntimeSession(cmd)
		return runFocusRuntime(cmd, id, neighborSession(-1))
	},
}

var focusNextManagedCmd = &cobra.Command{
	Use:     "focus-next-managed",
	Short:   "Focus the next managed tmux session",
	Long:    "Cycles focus to the next managed tmux session." + focusRuntimeHelp,
	Example: "  amx focus-next-managed",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		id := focusRuntimeSession(cmd)
		return runFocusRuntime(cmd, id, neighborSession(1))
	},
}

var focusNextCmd = &cobra.Command{
	Use:     "focus-next",
	Short:   "Focus the next tmux session requiring attention",
	Long:    "Cycles focus to the next tmux session needing user interaction." + focusRuntimeHelp,
	Example: "  amx focus-next",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		id := focusRuntimeSession(cmd)
		return runFocusRuntime(cmd, id, nextAttentionSession)
	},
}

func init() {
	for _, cmd := range []*cobra.Command{focusPrevManagedCmd, focusNextManagedCmd, focusNextCmd} {
		addFocusRuntimeFlags(cmd)
	}
}
