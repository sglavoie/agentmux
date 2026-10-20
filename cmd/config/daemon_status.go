package config

import (
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/spf13/cobra"
)

var daemonSettingsHealth = func() ipc.Health { return ipc.NewClient().Health() }
var daemonSettingsLoad = cli.LoadRunnableConfig

type daemonSettingsStatus struct {
	Known   bool     `json:"known"`
	Changed []string `json:"changed"`
	Message string   `json:"message"`
}

var configDaemonStatusCmd = &cobra.Command{
	Use:   "daemon-status",
	Short: "Compare saved settings with the running daemon",
	Long: `Compares the daemon settings saved in config.toml with the ones the running
daemon applied when it started, and names the settings that differ. A daemon
reads its settings once, so a changed setting takes effect only after
` + "`amx daemon restart`" + `. The daemon is never started or restarted here.

With --json it reports {known, changed, message}: known is false when the
daemon is not running or cannot report its settings.`,
	Example: `  amx config daemon-status
  amx config daemon-status --json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOut, _ := cmd.Flags().GetBool("json")
		cfg, err := daemonSettingsLoad()
		if err != nil {
			return cli.CommandFailure(cmd, jsonOut, "config_invalid", err)
		}
		health := daemonSettingsHealth()
		status := daemonSettingsStatus{Changed: []string{}}
		switch {
		case !health.Alive:
			status.Message = "Daemon is not running. Start it to apply saved settings."
		case len(health.DaemonSettings) == 0:
			status.Message = "This daemon cannot report its applied settings. Update and restart it, then check again."
		default:
			changed, known := config.ChangedDaemonSettings(health.DaemonSettings, cfg.DaemonSettings())
			status.Known = known
			if !known {
				status.Message = "The daemon and CLI use different settings projections. Update both and check again."
			} else {
				status.Changed = changed
				status.Message = "Saved daemon settings are applied."
				if len(changed) > 0 {
					status.Message = strings.Join(changed, ", ") + " settings changed; restart daemon to apply."
				}
			}
		}
		if jsonOut {
			return cli.WriteJSON(cmd, status)
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), status.Message)
		return err
	},
}

func init() {
	configDaemonStatusCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	configCmd.AddCommand(configDaemonStatusCmd)
}
