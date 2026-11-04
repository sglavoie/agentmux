package dashboard

import (
	"github.com/sglavoie/agentmux/internal/keybindings"
	"github.com/sglavoie/agentmux/tui/customization"
	"github.com/spf13/cobra"
)

func KeybindingsCommand() *cobra.Command { return keybindingsCmd }

var keybindingsCmd = &cobra.Command{
	Use:     "keybindings",
	Short:   "Show tmux session keybindings",
	Long:    "Show the keys available while attached to a session on the private tmux server.",
	Example: "  amx keybindings",
	Args:    cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		return runKeybindingsReference(keybindings.Tmux)
	},
}

var runKeybindingsReference = customization.RunKeybindingsReference
