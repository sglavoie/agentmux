package config

import (
	"fmt"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/spf13/cobra"
)

// Only launch identities are exposed to selectors, never profile environment,
// arguments, or home contents. Include profiles with no assigned project.
type profileChoice struct {
	Name    string `json:"name"`
	Harness string `json:"harness"`
}

var configProfilesCmd = &cobra.Command{
	Use:   "profiles",
	Short: "List configured launch profiles",
	Long: `Lists every configured profile with the harness it launches, one per line
as tab-separated name and harness, including profiles no project uses. Only
launch identities are shown: never a profile's environment, arguments, or
home directory.

With --json it reports an array of {name, harness}.`,
	Example: `  amx config profiles
  amx config profiles --json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		cfg, err := cli.LoadRunnableConfig()
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "config_invalid", err)
		}
		rows := make([]profileChoice, 0, len(cfg.Profiles))
		for _, profile := range cfg.Profiles {
			rows = append(rows, profileChoice{profile.Name, profile.EffectiveHarness()})
		}
		if jsonOutput {
			return cli.WriteJSON(cmd, rows)
		}
		for _, row := range rows {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", row.Name, row.Harness); err != nil {
				return err
			}
		}
		return nil
	},
}

func init() {
	configProfilesCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	configCmd.AddCommand(configProfilesCmd)
}
