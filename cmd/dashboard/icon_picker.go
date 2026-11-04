package dashboard

import (
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/tui/customization"
	"github.com/spf13/cobra"
)

var (
	loadIconPickerConfig = config.LoadDefault
	runIconPickerOverlay = customization.RunIconOverlay
)

func IconPickerCommand() *cobra.Command { return iconPickerCmd }

var iconPickerCmd = &cobra.Command{
	Use:   "icon-picker",
	Short: "Set the current tmux session's harness icon",
	Long: `Pick an icon for the tmux session named by --runtime-session, the
session this command runs in, or the only session running.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error { return runPortableIconPicker(cmd) },
}
