package session

import (
	"fmt"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/flightlog"
	"github.com/spf13/cobra"
)

func newFlightMissionCommand(deps flightLogDependencies) *cobra.Command {
	root := &cobra.Command{Use: "mission", Short: "Create or update an explicit project outcome"}
	for _, action := range []string{"create", "update"} {
		var m flightlog.Mission
		var jsonOutput bool
		cmd := &cobra.Command{Use: action, Short: action + " a project mission", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				store, err := deps.store()
				if err != nil {
					return cli.CommandFailure(cmd, jsonOutput, "flight_log_unavailable", err)
				}
				if action == "update" {
					snapshot, err := store.Load(cli.CommandContext(cmd))
					if err != nil {
						return cli.CommandFailure(cmd, jsonOutput, "flight_log_unavailable", err)
					}
					report := flightlog.Build(snapshot, flightlog.Filters{Period: "all"}, deps.now(), nil)
					found := false
					for _, old := range report.Missions {
						if old.ID != m.ID {
							continue
						}
						if !cmd.Flags().Changed("title") {
							m.Title = old.Title
						}
						if !cmd.Flags().Changed("project") {
							m.Project = old.Project
						}
						if !cmd.Flags().Changed("status") {
							m.Status = old.Status
						}
						if !cmd.Flags().Changed("reference") {
							m.References = old.References
						}
						m.UpdatedAt = old.UpdatedAt
						found = true
						break
					}
					if !found {
						return cli.CommandFailure(cmd, jsonOutput, "mission_not_found", fmt.Errorf("mission not found: %s", m.ID))
					}
				}
				saved, err := store.SaveMission(cli.CommandContext(cmd), m, deps.now())
				if err != nil {
					return cli.CommandFailure(cmd, jsonOutput, "mission_update_failed", err)
				}
				if jsonOutput {
					return cli.WriteJSON(cmd, saved)
				}
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s · %s · %s\n", saved.ID, saved.Title, saved.Status)
				return err
			}}
		cli.AddJSONFlag(cmd, &jsonOutput)
		cmd.Flags().StringVar(&m.Title, "title", "", "concrete outcome")
		cmd.Flags().StringVar(&m.Project, "project", "", "project label")
		cmd.Flags().StringVar(&m.Status, "status", "briefed", "briefed, in_progress, review, or shipped (explicit completion)")
		cmd.Flags().StringArrayVar(&m.References, "reference", nil, "related conversation identity, worktree path, or ASL run ID; repeat to add")
		if action == "update" {
			cmd.Flags().StringVar(&m.ID, "id", "", "mission ID")
			_ = cmd.MarkFlagRequired("id")
		} else {
			_ = cmd.MarkFlagRequired("title")
			_ = cmd.MarkFlagRequired("project")
		}
		root.AddCommand(cmd)
	}
	return root
}
