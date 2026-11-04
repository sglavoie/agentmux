package dashboard

import (
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/tui/overview"
	"github.com/spf13/cobra"
)

var (
	loadOverviewConfig = func() (*config.Config, error) { return cli.LoadRunnableConfig() }
	runOverview        = func(cfg *config.Config, selected int) error { return overview.Run(cfg, nil, selected) }
	overviewBackend    string
	overviewView       View
)

func OverviewCommand() *cobra.Command { return overviewCmd }

var overviewCmd = &cobra.Command{
	Use:   "overview",
	Short: "Open the live tmux session overview",
	Long:  "Open a grid of live tmux sessions and a preview of each pane.",
	Args:  cobra.NoArgs,
	RunE:  func(_ *cobra.Command, _ []string) error { return runOverviewCommand(0) },
}

func init() {
	overviewCmd.Flags().StringVar(&overviewBackend, cli.BackendFlag, "", BackendFlagUsage)
	AddViewFlags(overviewCmd, &overviewView, "")
}

func runOverviewCommand(_ int) error {
	requested := strings.ToLower(strings.TrimSpace(overviewBackend))
	if requested != "" && requested != config.LaunchBackendTmux {
		return fmt.Errorf("unsupported --backend %q: use tmux", overviewBackend)
	}
	cfg, err := loadOverviewConfig()
	if err != nil {
		return err
	}
	if err := selectTerminalsForConfig(overviewBackend, cfg); err != nil {
		return err
	}
	selected, err := terminalView(overviewView)
	if err != nil {
		return err
	}
	return runOverview(cfg, selected)
}
