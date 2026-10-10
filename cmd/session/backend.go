package session

// `amx backend` reports and controls the terminal backend setup: where
// launches go, which tmux the private server runs and where it came from, and
// what an operator has to do when something is missing. It is what the
// desktop app shows as its setup diagnostics and what the uninstaller asks
// before it leaves agents running.

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/tmux"
)

// The seams the tests replace: the configuration, the machine probe, and the
// server that would be stopped.
var (
	backendLoadConfig = cli.LoadRunnableConfig
	backendProbe      = func() cli.BackendProbe { return cli.BackendProbe{} }
	backendStopServer = func() error {
		server, err := tmux.NewServer(tmux.Options{})
		if err != nil {
			return err
		}
		return server.Kill()
	}

	backendStatusJSON  bool
	backendStatusCheck bool
	backendStopJSON    bool
	backendStopYes     bool
)

var backendCmd = &cobra.Command{
	Use:   "backend",
	Short: "Show or stop the terminal backend agents run on",
	Long: `Show or stop the terminal backend agents run on.

Agents launch into Kitty tabs or into AgentMux's private tmux server, whose
agents outlive every terminal and the desktop app. ` + "`amx backend status`" + ` says
which one a launch uses, which tmux the private server runs, and what to do
when something is missing.`,
}

var backendStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Report the launch backend, the tmux in use, and any setup problem",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		status := collectBackendStatus()
		if backendStatusJSON {
			if err := cli.WriteJSON(cmd, status); err != nil {
				return err
			}
		} else {
			writeBackendStatus(cmd.OutOrStdout(), status)
		}
		if backendStatusCheck && !status.Usable() {
			return &cli.ExitError{Code: 1, Err: errors.New("the tmux backend is not usable"), Quiet: true}
		}
		return nil
	},
}

var backendStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "End the private tmux server and every agent running in it",
	Long: `End the private tmux server and every agent running in it.

Nothing else ends them: closing a terminal, quitting or updating the desktop
app, and restarting the daemon all leave agents running. This is the explicit
way to end them all at once, and it asks for --yes because their work in
progress is lost. Their conversations stay on disk and can be resumed.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		status := collectBackendStatus()
		result := backendStopResult{Running: status.Server.Running, Sessions: status.Server.Sessions, Agents: status.Server.Agents}
		if !status.Server.Running {
			if backendStopJSON {
				return cli.WriteJSON(cmd, result)
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "No private tmux server is running; there is nothing to stop.")
			return err
		}
		if !backendStopYes {
			return cli.CommandFailure(cmd, backendStopJSON, "confirmation_required", fmt.Errorf(
				"this ends %s; pass --yes to confirm",
				cli.CountOf(status.Server.Agents, "running agent", "running agents"),
			))
		}
		if err := backendStopServer(); err != nil {
			return cli.CommandFailure(cmd, backendStopJSON, "backend_unavailable", fmt.Errorf("stopping the private tmux server: %w", err))
		}
		result.Stopped = true
		if backendStopJSON {
			return cli.WriteJSON(cmd, result)
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "Stopped the private tmux server and %s.\n",
			cli.CountOf(status.Server.Agents, "running agent", "running agents"))
		return err
	},
}

type backendStopResult struct {
	// Running is whether a server was running when the command started.
	Running  bool `json:"running"`
	Stopped  bool `json:"stopped"`
	Sessions int  `json:"sessions"`
	Agents   int  `json:"agents"`
}

// collectBackendStatus loads the configuration and reads the machine. A
// configuration that cannot be loaded is part of the report.
func collectBackendStatus() cli.BackendStatus {
	cfg, err := backendLoadConfig()
	var missing *config.MissingConfigError
	if errors.As(err, &missing) {
		// No configuration yet is the state of a fresh install, not a fault
		// of the backend: launches would go to the default.
		cfg, err = nil, nil
	}
	if cfg != nil {
		cli.ConfigureTmux(cfg)
	}
	return cli.CollectBackendStatus(cfg, err, backendProbe())
}

func writeBackendStatus(w io.Writer, status cli.BackendStatus) {
	p := func(format string, args ...any) { _, _ = fmt.Fprintf(w, format+"\n", args...) }
	p("launch backend: %s", status.LaunchBackend)
	if status.Tmux.Available {
		verified := ""
		if status.Tmux.Verified {
			verified = ", checksums verified"
		}
		p("tmux: %s (%s, from %s%s)", status.Tmux.Path, status.Tmux.Version, status.Tmux.Source, verified)
	} else {
		p("tmux: not available (need %s or newer)", status.Tmux.MinVersion)
	}
	if status.Server.Running {
		p("server: running at %s, %s, %s", status.Server.Socket,
			cli.CountOf(status.Server.Sessions, "session", "sessions"),
			cli.CountOf(status.Server.Agents, "running agent", "running agents"))
		if status.Server.Binary != "" {
			p("server tmux: %s", status.Server.Binary)
		}
	} else {
		p("server: not running (the first tmux launch starts it)")
	}
	for _, problem := range status.Problems {
		p("")
		p("%s: %s", problem.Severity, problem.Message)
		if problem.Hint != "" {
			p("  → %s", problem.Hint)
		}
	}
}

// BackendCommand returns `amx backend`.
func BackendCommand() *cobra.Command { return backendCmd }

func init() {
	backendStatusCmd.Flags().BoolVar(&backendStatusJSON, "json", false, "emit the report as JSON")
	backendStatusCmd.Flags().BoolVar(&backendStatusCheck, "check", false, "exit 1 when launching on tmux would not work")
	backendStopCmd.Flags().BoolVar(&backendStopJSON, "json", false, "emit the result as JSON")
	backendStopCmd.Flags().BoolVar(&backendStopYes, "yes", false, "confirm ending every running agent")
	backendCmd.AddCommand(backendStatusCmd, backendStopCmd)
}
