package dashboard

import (
	"errors"
	"fmt"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/spf13/cobra"
)

var (
	loadOpenDashboardConfig = func() (*config.Config, error) { return cli.LoadRunnableConfig() }
	openDashboardBinary     = cli.AmxBinary
	openDashboardStartPopup = startPopup
	openDashboardHere       = func() error { return Run("", View{}) }
	openDashboardJSON       bool
)

func OpenDashboardCommand() *cobra.Command { return openDashboardCmd }

var openDashboardCmd = &cobra.Command{
	Use:   "open-dashboard",
	Short: "Open the Agent Mux dashboard in a tmux popup or this terminal",
	Long: `Opens the dashboard over the last terminal attached to a tmux
session. With no attached terminal, it runs in this terminal.`,
	Args: cobra.NoArgs,
	RunE: runOpenDashboard,
}

func init() { cli.AddJSONFlag(openDashboardCmd, &openDashboardJSON) }

type openDashboardData struct {
	Opened bool   `json:"opened"`
	Client string `json:"client,omitempty"`
}

func runOpenDashboard(cmd *cobra.Command, _ []string) error {
	if _, err := loadOpenDashboardConfig(); err != nil {
		return cli.ConfigFailure(cmd, openDashboardJSON, err)
	}
	popupErr := errNoAttachedTerminal
	if opened, err := openTmux(); err == nil {
		server, ok := opened.(tmuxPopups)
		if !ok {
			return cli.CommandFailure(cmd, openDashboardJSON, "backend_unavailable", errors.New("tmux backend cannot open popups"))
		}
		bin, err := openDashboardBinary()
		if err != nil {
			return cli.CommandFailure(cmd, openDashboardJSON, "binary_unavailable", err)
		}
		var client string
		if client, popupErr = openDashboardPopup(server, bin, openDashboardStartPopup); popupErr == nil {
			if openDashboardJSON {
				return cli.WriteJSON(cmd, openDashboardData{Opened: true, Client: client})
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "Opened the Agent Mux dashboard over %s.\n", client)
			return err
		}
	}
	if !errors.Is(popupErr, errNoAttachedTerminal) {
		return cli.CommandFailure(cmd, openDashboardJSON, "launch_failed", popupErr)
	}
	if openDashboardJSON {
		return cli.CommandFailure(cmd, openDashboardJSON, "no_attached_terminal",
			errors.New("no terminal is attached to a tmux session, so there is no dashboard to bring forward; run `amx` in a terminal"))
	}
	return openDashboardHere()
}
