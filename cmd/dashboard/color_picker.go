package dashboard

import (
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/tui/customization"
	"github.com/spf13/cobra"
)

func ColorPickerCommand() *cobra.Command { return colorPickerCmd }

var colorPickerCmd = &cobra.Command{
	Use:   "color-picker",
	Short: "Set the current tmux session's color",
	Long: `Pick a color for the tmux session named by --runtime-session, the
session this command runs in, or the only session running.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error { return runPortableColorPicker(cmd) },
}

var (
	loadPickerConfig      = config.LoadDefault
	runColorPickerOverlay = customization.RunColorOverlay
)

func init() {
	addPickerSessionFlag(colorPickerCmd)
	addPickerSessionFlag(iconPickerCmd)
}
