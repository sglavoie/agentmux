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
			return cli.ConfigFailure(cmd, jsonOutput, err)
		}
		rows := make([]profileChoice, 0, len(cfg.Profiles))
		for _, profile := range cfg.Profiles {
			rows = append(rows, profileChoice{profile.Name, profile.EffectiveHarness()})
		}
		if jsonOutput {
			return cli.WriteJSON(cmd, rows)
		}
		out := cmd.OutOrStdout()
		// A pipe gets the tab-separated rows scripts read, and nothing when
		// there are none; a person gets a table, or says so.
		if !profilesIsTerminal(out) {
			for _, row := range rows {
				if _, err := fmt.Fprintf(out, "%s\t%s\n", row.Name, row.Harness); err != nil {
					return err
				}
			}
			return nil
		}
		if len(rows) == 0 {
			_, err := fmt.Fprintln(out, "No profiles are configured; `amx config setup` sets up the first one.")
			return err
		}
		table := make([][]string, 0, len(rows))
		for _, row := range rows {
			table = append(table, []string{row.Name, row.Harness})
		}
		return cli.WriteTable(out, []string{"PROFILE", "HARNESS"}, table)
	},
}

// profilesIsTerminal decides between the table and the tab-separated rows.
var profilesIsTerminal = cli.IsTerminal

func init() {
	cli.AddJSONFlag(configProfilesCmd, nil)
	configCmd.AddCommand(configProfilesCmd)
}
